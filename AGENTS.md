# AGENTS.md

Guidance for coding agents working in this repository.

## Project Overview

`anylan` is a small Go MVP for a ZeroTier-like virtual LAN aimed at LAN-discovery games.

The current design is intentionally narrow:

- Linux client only.
- Layer 2 TAP device on the client.
- Central QUIC/UDP relay server.
- One room code maps to one isolated broadcast domain.
- No P2P, NAT traversal, accounts, persistence, DHCP, or GUI.

## Repository Layout

- `cmd/client`: client CLI. Currently supports `join`.
- `cmd/server`: control + relay server CLI.
- `internal/client`: QUIC client session and TAP frame bridge.
- `internal/server`: QUIC server listener, TLS setup, and lifecycle.
- `internal/control`: server-side join control flow and stream handling.
- `internal/relay`: room manager, peer allocation, MAC learning, and frame forwarding.
- `internal/protocol`: wire message framing and JSON control messages.
- `internal/tap`: Linux TAP device creation and interface configuration.

## Build and Test

Run the full test suite before finishing code changes:

```bash
make test
```

Build both binaries:

```bash
make build
```

Cross-compile the Windows client:

```bash
make build-windows
```

The client uses Linux TAP devices and `ip` commands, so real client runs require Linux and root privileges. Unit tests should not require root.

## Local Manual Run

Start a local relay with an ephemeral self-signed certificate:

```bash
go run ./cmd/server --listen :4433 --insecure-dev-cert --web 0.0.0.0:8080
```

Join from a Linux client:

```bash
sudo go run ./cmd/client -- join \
  --server 127.0.0.1:4433 \
  --room my-room \
  --dev anylan0 \
  --insecure-skip-verify
```

For production-like TLS testing, pass `--tls-cert` and `--tls-key` to the server and omit `--insecure-skip-verify` on clients.

## Manual Acceptance

For end-to-end verification, use two Linux client machines:

1. Start `anylan-server` on a reachable UDP port.
2. Start `anylan-client join` on both clients with the same room code.
3. Confirm both TAP interfaces receive `10.240.x.y/24` addresses.
4. Ping the peer virtual IP in both directions.
5. Start a LAN-discovery game and verify discovery or direct joining works.

Useful client checks:

```bash
ip -br addr show anylan0
ping <peer-virtual-ip>
```

Cleanup if a TAP device is left behind:

```bash
sudo ip link delete anylan0
```

## Reusable Test Servers

The user has provided two Linux servers for recurring real-machine client tests:

- `192.168.89.175` (`anylan-test1`), SSH as `root`.
- `192.168.89.152` (`anylan-test2`), SSH as `root`.

Two Windows machines are also available (passwordless SSH as `idreamshen`):

- `192.168.89.77` (`gpu-4090-win`), SSH as `idreamshen`.
- `192.168.89.131` (`gpu-3070-win`), SSH as `idreamshen`.

Use the current development machine as the relay server when appropriate. In the
current lab network, it has been reachable from the test servers at
`192.168.89.178`; verify with `hostname -I` before relying on that address.

Typical real-machine smoke test flow:

```bash
go build -o /tmp/anylan-client ./cmd/client
go build -o /tmp/anylan-server ./cmd/server

scp /tmp/anylan-client root@192.168.89.175:/tmp/anylan-client
scp /tmp/anylan-client root@192.168.89.152:/tmp/anylan-client

/tmp/anylan-server --listen :4433 --insecure-dev-cert
```

Then, in separate sessions:

```bash
ssh -tt root@192.168.89.175 '/tmp/anylan-client join --server <relay-ip>:4433 --room manual-smoke --dev anylan0 --name test1 --insecure-skip-verify'
ssh -tt root@192.168.89.152 '/tmp/anylan-client join --server <relay-ip>:4433 --room manual-smoke --dev anylan0 --name test2 --insecure-skip-verify'
```

Confirm both clients receive `10.240.x.y/24` addresses, then ping each virtual IP
from the other test server. Stop the client sessions with `Ctrl-C` and confirm no
`/tmp/anylan-client join` process or `anylan0` device is left behind.

### Automated L2 smoke test

For a scripted version of the above two-machine flow that also verifies L2
specifics (ARP broadcast flood, custom EtherType `0x88b5` forwarding, cross-room
isolation), run:

```bash
./scripts/manual-l2-test.sh
```

Defaults match the two test servers above and use the local dev machine as the
relay. Override with `--remote1`, `--remote2`, `--relay-ip`, `--relay-port`,
`--room`, or `--dev`. Pass `--keep` to leave the relay and remote clients
running after the run for follow-up debugging.

The script requires:

- `go` in `PATH` on the dev machine.
- Passwordless `ssh` as root to both remotes.
- `iputils-arping`, `tcpdump`, and `python3` on both remotes; the script will
  `apt-get install` them automatically if missing.

Output is a per-test `PASS/FAIL/SKIP` table; per-test logs land in
`/tmp/anylan-l2-test-<timestamp>/`. Exit code is `0` if all tests pass.

## Implementation Notes

- Keep protocol changes backward-conscious. `internal/protocol` defines message types, size limits, and the protocol version.
- Do not put TAP-specific behavior into tests that must run without root.
- Relay tests should use loopback UDP sockets and in-process QUIC listeners.
- Rejection paths should write a protocol-level `JoinReject` when possible, rather than only closing the QUIC connection.
- The relay may log quic-go UDP receive buffer warnings on Linux. These are performance warnings, not necessarily functional failures.

## Style

- Follow existing small-package Go style.
- Keep abstractions minimal; this repo is intentionally an MVP.
- Run `gofmt` on edited Go files.
- Prefer focused tests around protocol, room allocation, and relay forwarding behavior.
