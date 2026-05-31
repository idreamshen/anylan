#!/usr/bin/env bash
# manual-l2-test.sh — automated L2 smoke test for anylan across two real Linux
# machines and a local relay.
#
# Flow:
#   1. Build client + server, scp client to the two remotes.
#   2. Install any missing tooling on the remotes (apt).
#   3. Start the relay locally; start a WebUI-controlled client on each remote.
#   4. Discover the virtual IP/MAC of each peer.
#   5. Run a sequence of L2-level checks:
#        T1 baseline ICMP
#        T2 ARP broadcast (peer1 -> peer2)
#        T3 custom EtherType 0x88b5 unicast (peer1 -> peer2)
#        T4 cross-room isolation (restart peer2 in another room)
#   6. Tear everything down (unless --keep). Print a PASS/FAIL summary.
#
# Requires:
#   - go in PATH on the dev machine.
#   - passwordless ssh as root to both remotes.
#   - python3 (preinstalled) and root on both remotes for raw socket inject.
#
# Conservative defaults match AGENTS.md "Reusable Test Servers" section.

set -u
set -o pipefail

# ---------- defaults ----------
REMOTE1="root@192.168.89.175"
REMOTE2="root@192.168.89.152"
RELAY_IP=""
RELAY_PORT="4433"
ROOM=""
DEV="anylan0"
KEEP=0
SKIP_CSV=""

usage() {
  cat <<EOF
Usage: $0 [options]
  --remote1 USER@HOST    First peer SSH target (default: $REMOTE1)
  --remote2 USER@HOST    Second peer SSH target (default: $REMOTE2)
  --relay-ip IP          Relay bind/advertise IP (default: hostname -I first addr)
  --relay-port PORT      Relay UDP port (default: $RELAY_PORT)
  --room NAME            Room name (default: l2-test-<timestamp>)
  --dev NAME             TAP device name on remotes (default: $DEV)
  --skip T1,T2,...       Skip listed tests
  --keep                 Do not tear down relay/clients on exit
  -h, --help             Show this help
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --remote1)     REMOTE1="$2"; shift 2 ;;
    --remote2)     REMOTE2="$2"; shift 2 ;;
    --relay-ip)    RELAY_IP="$2"; shift 2 ;;
    --relay-port)  RELAY_PORT="$2"; shift 2 ;;
    --room)        ROOM="$2"; shift 2 ;;
    --dev)         DEV="$2"; shift 2 ;;
    --skip)        SKIP_CSV="$2"; shift 2 ;;
    --keep)        KEEP=1; shift ;;
    -h|--help)     usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage; exit 2 ;;
  esac
done

TS="$(date +%Y%m%d-%H%M%S)"
[ -z "$ROOM" ] && ROOM="l2-test-$TS"
[ -z "$RELAY_IP" ] && RELAY_IP="$(hostname -I | awk '{print $1}')"

LOGDIR="/tmp/anylan-l2-test-$TS"
mkdir -p "$LOGDIR"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CLIENT_BIN="/tmp/anylan-client"
SERVER_BIN="/tmp/anylan-server"
REMOTE_CLIENT="/tmp/anylan-client"
REMOTE_WEB="127.0.0.1:18081"

SSH_OPTS="-o StrictHostKeyChecking=no -o BatchMode=yes -o ConnectTimeout=5"

# ---------- logging ----------
log()  { printf '[%(%H:%M:%S)T] %s\n' -1 "$*"; }
warn() { printf '[%(%H:%M:%S)T] WARN: %s\n' -1 "$*" >&2; }
die()  { printf '[%(%H:%M:%S)T] ERROR: %s\n' -1 "$*" >&2; exit 2; }

skipped() {
  case ",$SKIP_CSV," in
    *",$1,"*) return 0 ;;
    *)        return 1 ;;
  esac
}

# ---------- result tracking ----------
declare -a RESULTS=()
record() {
  # record <id> <name> <status> <detail>
  RESULTS+=("$1|$2|$3|$4")
}

# ---------- cleanup ----------
RELAY_PID=""
cleanup() {
  if [ "$KEEP" = "1" ]; then
    log "--keep set; leaving relay (pid ${RELAY_PID:-?}) and remote clients running"
    log "logs: $LOGDIR"
    return
  fi
  log "cleaning up"
  for R in "$REMOTE1" "$REMOTE2"; do
    ssh $SSH_OPTS "$R" "python3 - <<'PY' >/dev/null 2>&1 || true
import urllib.request
req = urllib.request.Request('http://$REMOTE_WEB/api/leave', data=b'{}', headers={'Content-Type': 'application/json'}, method='POST')
urllib.request.urlopen(req, timeout=2).read()
PY
pkill -TERM -x anylan-client >/dev/null 2>&1 || true" || true
  done
  sleep 1
  for R in "$REMOTE1" "$REMOTE2"; do
    ssh $SSH_OPTS "$R" "ip link show $DEV >/dev/null 2>&1 && ip link delete $DEV || true" || true
  done
  if [ -n "$RELAY_PID" ] && kill -0 "$RELAY_PID" 2>/dev/null; then
    kill -TERM "$RELAY_PID" 2>/dev/null || true
    wait "$RELAY_PID" 2>/dev/null || true
  fi
  log "logs: $LOGDIR"
}
trap cleanup EXIT INT TERM

# ---------- 1. preflight ----------
log "=== preflight ==="
command -v go >/dev/null || die "go not in PATH"
command -v ssh >/dev/null || die "ssh not in PATH"
command -v scp >/dev/null || die "scp not in PATH"

for R in "$REMOTE1" "$REMOTE2"; do
  ssh $SSH_OPTS "$R" true >/dev/null 2>&1 \
    || die "cannot ssh to $R (need passwordless root)"
done

# Verify remotes can reach our relay IP.
for R in "$REMOTE1" "$REMOTE2"; do
  if ! ssh $SSH_OPTS "$R" "ping -c 1 -W 2 $RELAY_IP >/dev/null 2>&1"; then
    die "remote $R cannot reach relay IP $RELAY_IP; pass --relay-ip"
  fi
done

# ---------- 2. build + scp ----------
log "=== build ==="
( cd "$REPO_ROOT" && go build -o "$CLIENT_BIN" ./cmd/client ) || die "client build failed"
( cd "$REPO_ROOT" && go build -o "$SERVER_BIN" ./cmd/server ) || die "server build failed"

log "=== scp client to remotes ==="
for R in "$REMOTE1" "$REMOTE2"; do
  # Kill any leftover client first to avoid "text file busy".
  ssh $SSH_OPTS "$R" "pkill -TERM -x anylan-client >/dev/null 2>&1 || true; \
                        ip link show $DEV >/dev/null 2>&1 && ip link delete $DEV || true; \
                        sleep 1" || true
  scp $SSH_OPTS "$CLIENT_BIN" "$R:$REMOTE_CLIENT" >/dev/null \
    || die "scp to $R failed"
  ssh $SSH_OPTS "$R" "chmod +x $REMOTE_CLIENT" || die "chmod failed on $R"
done

# ---------- 3. install dependencies on remotes ----------
log "=== install remote dependencies (iputils-arping tcpdump python3) ==="
for R in "$REMOTE1" "$REMOTE2"; do
  ssh $SSH_OPTS "$R" "
    set -e
    need=
    command -v arping  >/dev/null || need=\"\$need iputils-arping\"
    command -v tcpdump >/dev/null || need=\"\$need tcpdump\"
    command -v python3 >/dev/null || need=\"\$need python3\"
    if [ -n \"\$need\" ]; then
      echo \"installing on \$(hostname):\$need\"
      DEBIAN_FRONTEND=noninteractive apt-get update -qq
      DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \$need
    fi
  " >>"$LOGDIR/install-$(echo "$R" | tr '@.' '__').log" 2>&1 \
    || die "dependency install failed on $R; see $LOGDIR/install-*.log"
done

# ---------- 4. start relay ----------
log "=== start relay on $RELAY_IP:$RELAY_PORT ==="
"$SERVER_BIN" --listen ":$RELAY_PORT" --insecure-dev-cert \
  >"$LOGDIR/relay.log" 2>&1 &
RELAY_PID=$!
sleep 1
if ! kill -0 "$RELAY_PID" 2>/dev/null; then
  die "relay failed to start; see $LOGDIR/relay.log"
fi
# Confirm socket bound.
for _ in 1 2 3 4 5; do
  if ss -ulnp 2>/dev/null | grep -q ":$RELAY_PORT"; then break; fi
  sleep 0.5
done
ss -ulnp 2>/dev/null | grep -q ":$RELAY_PORT" \
  || die "relay not listening on udp:$RELAY_PORT; see $LOGDIR/relay.log"
log "relay pid=$RELAY_PID log=$LOGDIR/relay.log"

# ---------- 5. start clients ----------
start_client() {
  # start_client <remote> <name> <room>
  local R="$1" NAME="$2" RM="$3"
  if ! ssh $SSH_OPTS "$R" "pgrep -x anylan-client >/dev/null"; then
    ssh -n -f $SSH_OPTS "$R" "nohup $REMOTE_CLIENT --web $REMOTE_WEB >/tmp/anylan-client.log 2>&1 </dev/null &" \
      || die "failed to start client WebUI on $R"
  fi
  wait_for_web "$R" || die "client WebUI did not start on $R"
  ssh $SSH_OPTS "$R" "timeout 15 python3 - '$RELAY_IP:$RELAY_PORT' '$RM' '$NAME' '$DEV' <<'PY'
import json, sys, urllib.request
payload = {
    'server': sys.argv[1],
    'room': sys.argv[2],
    'display_name': sys.argv[3],
    'device_name': sys.argv[4],
    'insecure_skip_verify': True,
    'prioritize_virtual_adapter': True,
}
req = urllib.request.Request(
    'http://$REMOTE_WEB/api/join',
    data=json.dumps(payload).encode(),
    headers={'Content-Type': 'application/json'},
    method='POST',
)
urllib.request.urlopen(req, timeout=10).read()
PY
  " || { fetch_client_log "$R" "$NAME"; die "failed to join client on $R; see $LOGDIR/$NAME.log"; }
}

wait_for_web() {
  local R="$1" tries=0
  while [ $tries -lt 20 ]; do
    if ssh $SSH_OPTS "$R" "python3 - <<'PY' >/dev/null 2>&1
import urllib.request
urllib.request.urlopen('http://$REMOTE_WEB/api/status', timeout=1).read()
PY
    "; then
      return 0
    fi
    sleep 0.5
    tries=$((tries+1))
  done
  return 1
}

wait_for_tap() {
  # wait_for_tap <remote> -> echoes "IP MAC", or empty on failure.
  local R="$1" tries=0
  while [ $tries -lt 30 ]; do
    local out
    out=$(ssh $SSH_OPTS "$R" "
      ip -br addr show $DEV 2>/dev/null | awk '{print \$3}' | head -1
      ip -br link show $DEV 2>/dev/null | awk '{print \$3}' | head -1
    " 2>/dev/null)
    local ip mac
    ip=$(echo "$out"  | sed -n '1p' | cut -d/ -f1)
    mac=$(echo "$out" | sed -n '2p')
    if [ -n "$ip" ] && [ -n "$mac" ]; then
      echo "$ip $mac"
      return 0
    fi
    sleep 0.5
    tries=$((tries+1))
  done
  return 1
}

stop_client() {
  local R="$1"
  ssh $SSH_OPTS "$R" "python3 - <<'PY' >/dev/null 2>&1 || true
import urllib.request
req = urllib.request.Request('http://$REMOTE_WEB/api/leave', data=b'{}', headers={'Content-Type': 'application/json'}, method='POST')
urllib.request.urlopen(req, timeout=2).read()
PY
  " || true
  sleep 1
  ssh $SSH_OPTS "$R" "ip link show $DEV >/dev/null 2>&1 && ip link delete $DEV || true" || true
}

fetch_client_log() {
  local R="$1" tag="$2"
  ssh $SSH_OPTS "$R" "cat /tmp/anylan-client.log" \
    >"$LOGDIR/$tag.log" 2>/dev/null || true
}

log "=== start client on $REMOTE1 (room=$ROOM) ==="
start_client "$REMOTE1" "peer1" "$ROOM"
log "=== start client on $REMOTE2 (room=$ROOM) ==="
start_client "$REMOTE2" "peer2" "$ROOM"

PEER1_INFO=$(wait_for_tap "$REMOTE1") \
  || { fetch_client_log "$REMOTE1" "peer1"; die "peer1 TAP did not come up; see $LOGDIR/peer1.log"; }
PEER2_INFO=$(wait_for_tap "$REMOTE2") \
  || { fetch_client_log "$REMOTE2" "peer2"; die "peer2 TAP did not come up; see $LOGDIR/peer2.log"; }
IP1=$(echo "$PEER1_INFO" | awk '{print $1}'); MAC1=$(echo "$PEER1_INFO" | awk '{print $2}')
IP2=$(echo "$PEER2_INFO" | awk '{print $1}'); MAC2=$(echo "$PEER2_INFO" | awk '{print $2}')

log "peer1: $REMOTE1  IP=$IP1  MAC=$MAC1"
log "peer2: $REMOTE2  IP=$IP2  MAC=$MAC2"

# Helper: run tcpdump on peer2 in background expecting >=1 packet within 3s.
# Returns: 0 if a packet matched, 1 if timeout (no match), 2 on error.
capture_on_peer2() {
  local filter="$1" outfile="$2"
  ssh $SSH_OPTS "$REMOTE2" "timeout 4 tcpdump -i $DEV -y EN10MB -e -nn -l -c 1 $filter" \
    >"$outfile" 2>>"$outfile.err"
  local rc=$?
  # tcpdump exits 0 if it captured -c packets; timeout exits 124 if it killed
  # tcpdump (no packets captured before timeout). On older tcpdump, a captured
  # packet may still yield "rc=0" via timeout.
  case $rc in
    0)   return 0 ;;
    124) return 1 ;;
    *)   return 2 ;;
  esac
}

# Helper: run tcpdump on peer2 for fixed 3s, return 0 if NO packets matched.
capture_silence_on_peer2() {
  local filter="$1" outfile="$2"
  # -c 1 causes tcpdump to exit immediately upon capture; otherwise timeout
  # kills it after 3s. We invert: success means timeout fired with no capture.
  ssh $SSH_OPTS "$REMOTE2" "timeout 3 tcpdump -i $DEV -y EN10MB -e -nn -l -c 1 $filter" \
    >"$outfile" 2>>"$outfile.err"
  local rc=$?
  case $rc in
    124) return 0 ;;   # timeout -> no packet -> silence -> PASS
    0)   return 1 ;;   # captured a packet -> leak -> FAIL
    *)   return 2 ;;
  esac
}

# ---------- 6. tests ----------

run_T1() {
  log "--- T1 baseline ICMP (peer1 ping peer2) ---"
  local out
  out=$(ssh $SSH_OPTS "$REMOTE1" "ping -c 3 -W 2 -I $DEV $IP2" 2>&1 \
        | tee "$LOGDIR/T1.log")
  if echo "$out" | grep -q "0% packet loss"; then
    record T1 "baseline ICMP" PASS "3/3 replies"
  else
    local loss
    loss=$(echo "$out" | grep -oE '[0-9]+% packet loss' | head -1)
    record T1 "baseline ICMP" FAIL "${loss:-no reply}"
  fi
}

run_T2() {
  log "--- T2 ARP broadcast (arping peer1 -> peer2) ---"
  # Start capture on peer2 in background. arping fires while capture is open.
  ( capture_on_peer2 "'arp and ether src $MAC1'" "$LOGDIR/T2.pcap.log" ) &
  local cap_pid=$!
  sleep 0.3
  ssh $SSH_OPTS "$REMOTE1" "arping -I $DEV -c 1 -w 2 $IP2" \
    >"$LOGDIR/T2-arping.log" 2>&1 || true
  wait "$cap_pid"
  local rc=$?
  case $rc in
    0) record T2 "ARP broadcast flood" PASS "captured 1 ARP request from peer1" ;;
    1) record T2 "ARP broadcast flood" FAIL "no ARP from peer1 reached peer2" ;;
    *) record T2 "ARP broadcast flood" FAIL "tcpdump errored (see $LOGDIR/T2.pcap.log.err)" ;;
  esac
}

# Python3 raw-socket EtherType injector. Runs on peer1.
make_inject_script() {
  local dst_hex="${MAC2//:/}"
  local src_hex="${MAC1//:/}"
  cat <<PYEOF
import socket, sys
ETHERTYPE = 0x88b5
DEV = "$DEV"
DST = bytes.fromhex("$dst_hex")
SRC = bytes.fromhex("$src_hex")
PAYLOAD = b"anylan-l2-test-frame"
frame = DST + SRC + ETHERTYPE.to_bytes(2, "big") + PAYLOAD
if len(frame) < 60:
    frame += b"\\x00" * (60 - len(frame))
s = socket.socket(socket.AF_PACKET, socket.SOCK_RAW, 0)
s.bind((DEV, 0))
n = s.send(frame)
print("sent", n, "bytes")
PYEOF
}

run_T3() {
  log "--- T3 custom EtherType 0x88b5 (peer1 -> peer2) ---"
  local script_b64
  script_b64=$(make_inject_script | base64 -w0)
  ( capture_on_peer2 "'ether proto 0x88b5'" "$LOGDIR/T3.pcap.log" ) &
  local cap_pid=$!
  sleep 0.3
  ssh $SSH_OPTS "$REMOTE1" "echo $script_b64 | base64 -d | python3 -" \
    >"$LOGDIR/T3-inject.log" 2>&1 || true
  wait "$cap_pid"
  local rc=$?
  case $rc in
    0)
      if grep -q "0x88b5\|88b5 " "$LOGDIR/T3.pcap.log" 2>/dev/null; then
        record T3 "custom EtherType 0x88b5" PASS "frame delivered with EtherType 0x88b5"
      else
        record T3 "custom EtherType 0x88b5" PASS "tcpdump captured (filter matched)"
      fi
      ;;
    1) record T3 "custom EtherType 0x88b5" FAIL "no 0x88b5 frame reached peer2" ;;
    *) record T3 "custom EtherType 0x88b5" FAIL "tcpdump errored (see $LOGDIR/T3.pcap.log.err)" ;;
  esac
}

run_T4() {
  log "--- T4 cross-room isolation (peer2 rejoined to a different room) ---"
  local OTHER_ROOM="$ROOM-iso"
  log "stopping peer2 and rejoining room=$OTHER_ROOM"
  stop_client "$REMOTE2"
  start_client "$REMOTE2" "peer2-iso" "$OTHER_ROOM"
  local INFO
  INFO=$(wait_for_tap "$REMOTE2") \
    || { fetch_client_log "$REMOTE2" "peer2-iso"; record T4 "cross-room isolation" FAIL "peer2 did not rejoin"; return; }
  local NEW_IP NEW_MAC
  NEW_IP=$(echo "$INFO"  | awk '{print $1}')
  NEW_MAC=$(echo "$INFO" | awk '{print $2}')
  log "peer2 (isolated): IP=$NEW_IP MAC=$NEW_MAC"

  ( capture_silence_on_peer2 "'ether src $MAC1'" "$LOGDIR/T4.pcap.log" ) &
  local cap_pid=$!
  sleep 0.3
  # Spam broadcast ARP from peer1; if any leaks across rooms peer2 sees it.
  ssh $SSH_OPTS "$REMOTE1" "arping -I $DEV -c 3 -w 2 -b $IP2 || true" \
    >"$LOGDIR/T4-arping.log" 2>&1 || true
  # Also fire a custom-ethertype frame which is room-internal too.
  local script_b64
  script_b64=$(make_inject_script | base64 -w0)
  ssh $SSH_OPTS "$REMOTE1" "echo $script_b64 | base64 -d | python3 -" \
    >"$LOGDIR/T4-inject.log" 2>&1 || true
  wait "$cap_pid"
  local rc=$?
  case $rc in
    0) record T4 "cross-room isolation" PASS "no frames from peer1 reached isolated peer2" ;;
    1) record T4 "cross-room isolation" FAIL "frame from peer1 leaked across rooms" ;;
    *) record T4 "cross-room isolation" FAIL "tcpdump errored (see $LOGDIR/T4.pcap.log.err)" ;;
  esac
}

for tid in T1 T2 T3 T4; do
  if skipped "$tid"; then
    record "$tid" "(skipped)" SKIP "--skip"
    continue
  fi
  case "$tid" in
    T1) run_T1 ;;
    T2) run_T2 ;;
    T3) run_T3 ;;
    T4) run_T4 ;;
  esac
done

# Collect client logs for the record.
fetch_client_log "$REMOTE1" "peer1"
fetch_client_log "$REMOTE2" "peer2"

# ---------- 7. report ----------
echo
echo "=== anylan L2 smoke test ==="
printf "relay  : %s:%s  (pid %s, log %s/relay.log)\n" "$RELAY_IP" "$RELAY_PORT" "$RELAY_PID" "$LOGDIR"
printf "room   : %s\n" "$ROOM"
printf "peer1  : %-22s IP=%-15s MAC=%s\n" "$REMOTE1" "$IP1" "$MAC1"
printf "peer2  : %-22s IP=%-15s MAC=%s\n" "$REMOTE2" "$IP2" "$MAC2"
echo

pass=0; fail=0; skip=0
for entry in "${RESULTS[@]}"; do
  IFS='|' read -r id name status detail <<<"$entry"
  printf "%-3s %-34s %-5s  %s\n" "$id" "$name" "$status" "$detail"
  case "$status" in
    PASS) pass=$((pass+1)) ;;
    FAIL) fail=$((fail+1)) ;;
    SKIP) skip=$((skip+1)) ;;
  esac
done

echo
printf "result: %d PASS, %d FAIL, %d SKIP    logs: %s\n" "$pass" "$fail" "$skip" "$LOGDIR"

if [ $fail -gt 0 ]; then exit 1; fi
exit 0
