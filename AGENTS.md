# AGENTS.md

## Project Facts

- `anylan` is a Go 1.22 QUIC/UDP relay for room-scoped virtual LANs, with a Vue/Vite/Vuetify WebUI embedded into both binaries.
- Entrypoints are `cmd/client` and `cmd/server`; `cmd/tapsetup` is Windows-only support code used by the MSI build.
- Client networking is platform-specific: Linux uses L2 TAP `anylan0`; Windows uses an OpenVPN/tap-windows6-compatible TAP adapter; macOS uses system `utun` in IP-layer mode, so Ethernet broadcast/multicast LAN discovery is not equivalent there.
- The server allocates one `/24` per room from `--pool` (default `10.240.0.0/12`) and relays frames through `internal/control` plus `internal/relay`.
- Wire compatibility lives in `internal/protocol`: message type IDs, `Version`, size limits, and join/peer-list JSON structs.

## Commands

- Full verification: `make test`.
- Current-platform binaries: `make build`.
- Cross-compiles: `make build-linux`, `make build-windows`, `make build-darwin`.
- Focused Go test: `go test ./internal/relay -run TestForwardUnicastsKnownDestination`.
- Build commands run `npm --prefix webui ci` and `npm --prefix webui run build` first; this refreshes embedded files under `internal/webui/dist` for `//go:embed`.
- If only editing Go code and embedded WebUI assets are already present, `go test ./...` is a faster unit-test shortcut than `make test`.
- Run `gofmt` on edited Go files.

## Local Runs

- Local dev relay: `go run ./cmd/server --listen :4433 --insecure-dev-cert --web 127.0.0.1:18080`.
- Client WebUI: `sudo go run ./cmd/client --web 0.0.0.0:18081` on Linux/macOS, Administrator shell on Windows.
- Client WebUI join requests need `insecure_skip_verify: true` when connecting to `--insecure-dev-cert` servers.
- Client settings persist to `os.UserConfigDir()/anylan/client.json`; tests that need isolation should use `NewControllerWithConfig` with a temp path.
- Cleanup leftover Linux TAP devices with `sudo ip link delete anylan0`.

## Testing Notes

- Unit and integration tests must not require root or real TAP devices; keep TAP behavior behind platform code or test pure helpers.
- Relay/server integration tests use loopback UDP sockets and in-process QUIC listeners.
- Rejection paths should write a protocol-level `JoinReject` when possible, not just close the QUIC connection.
- quic-go UDP receive-buffer warnings on Linux are performance warnings, not necessarily functional failures.

## WebUI And Packaging

- WebUI source is in `webui/src`; Vite outputs to `internal/webui/dist`, which is embedded by `internal/webui/server.go`.
- Server WebUI defaults to `:18080` in code; client WebUI defaults to `127.0.0.1:18081`.
- Windows MSI build is `./scripts/build-windows-msi.ps1` on Windows CI; it installs `rsrc`, downloads verified TAP driver artifacts, requires WiX 5.0.2, and honors optional `MSI_VERSION`.
- Docker image builds only the server binary and includes the embedded WebUI assets from the Node build stage.

## Real-Machine Smoke Tests

- Reusable Linux clients: `root@192.168.89.175` (`anylan-test1`) and `root@192.168.89.152` (`anylan-test2`).
- Reusable Windows clients: `idreamshen@192.168.89.77` (`gpu-4090-win`) and `idreamshen@192.168.89.131` (`gpu-3070-win`).
- The dev machine has been reachable as relay at `192.168.89.178`; verify with `hostname -I` before relying on it.
- Automated Linux L2 smoke test: `./scripts/manual-l2-test.sh`. It builds local binaries, copies the client to both Linux hosts, starts remote WebUIs, checks ICMP, ARP broadcast, EtherType `0x88b5`, and cross-room isolation, then cleans up unless `--keep` is passed.
- The L2 script requires local `go`, passwordless root SSH to both Linux hosts, and remote `iputils-arping`, `tcpdump`, and `python3`; it will `apt-get install` missing remote tools.

## Change Guidance

- Keep abstractions small; this repo is an MVP with narrow package boundaries.
- Protocol changes should be backward-conscious and covered in `internal/protocol` tests.
- Prefer focused tests around protocol framing, room allocation, MAC validation/learning, relay forwarding, WebUI API handlers, and platform-neutral TAP helpers.
