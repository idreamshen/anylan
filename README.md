# anylan

Play LAN-only games with friends over the internet -- no port forwarding, no
VPN accounts, no complicated setup.

anylan creates a virtual local network between players. Everyone who joins the
same **room** appears on the same LAN, so games that rely on local network
discovery (e.g. "LAN game" lobbies) just work.

## How It Works

One person runs the **server** (or you use a shared one). Each player runs the
**client** and joins the same room code. anylan sets up a virtual network
adapter on each machine and bridges all traffic through the server. To the game
it looks like everyone is on the same local network.

```
  Player A ──┐                ┌── Player B
             ├── anylan ──────┤
  Player C ──┘   server       └── Player D
```

## Quick Start

### Prerequisites

| | Linux | macOS | Windows |
|---|---|---|---|
| **Client** | Root privileges (for creating the virtual network adapter) | Root privileges; uses native `utun` without third-party drivers | Administrator shell + [OpenVPN TAP driver](https://community.openvpn.net/openvpn/wiki/ManagingWindowsTAPDrivers) installed |
| **Server** | Any Linux machine with a public UDP port | Not supported yet | Not supported yet |

Download pre-built binaries from the
[Releases](https://github.com/idreamshen/anylan/releases) page, or build from
source (requires Go 1.21+):

```bash
make build
```

Windows client cross-compile:

```bash
make build-windows
# writes out/anylan-client-windows-amd64.exe
```

Darwin/macOS client cross-compile:

```bash
make build-darwin
# writes out/anylan-client-darwin-amd64 and out/anylan-client-darwin-arm64
```

### 1. Start the Server

On a machine reachable by all players:

```bash
./out/anylan-server --listen :4433 --tls-cert cert.pem --tls-key key.pem
```

For quick testing without real TLS certificates:

```bash
./out/anylan-server --listen :4433 --insecure-dev-cert --web 127.0.0.1:18080
```

The `--web` flag is optional but recommended: it exposes a status page at the
given address where you can see connected rooms and peers. Use `--web-token` if
the status page is reachable by anyone else.

### 2. Join a Room

Pick any room name. Everyone who uses the same name ends up on the same virtual
LAN.

Start the client control WebUI, then open <http://127.0.0.1:18081> and enter the
server host, server port, room code, display name, and virtual adapter.

**Linux:**

```bash
sudo ./out/anylan-client
```

**Windows** (run as Administrator):

```powershell
.\out\anylan-client-windows-amd64.exe
```

**macOS**:

```bash
sudo ./out/anylan-client
```

macOS uses the built-in `utun` driver. This avoids third-party kernel/network
drivers, but it is an IP-layer mode: direct virtual-IP connectivity is
supported, while LAN auto-discovery that depends on true Ethernet broadcast or
multicast may not work for every game.

To bind the WebUI to another address or port:

```bash
sudo ./out/anylan-client --web 0.0.0.0:18081 --web-token change-me
```

Each client gets a virtual IP like `10.240.x.y`. Once everyone has joined,
launch your game and look for LAN/local games -- you should see each other.

### 3. Stop

Click **Leave** in the client WebUI, or press `Ctrl-C` in the client terminal to
stop the client and clean up.

## Client Options

| Flag | Default | Description |
|---|---|---|
| `--web` | `127.0.0.1:18081` | Client control WebUI listen address |
| `--web-token` | | Bearer token required for the client WebUI |

## Server Options

| Flag | Default | Description |
|---|---|---|
| `--listen` | `:4433` | UDP listen address |
| `--tls-cert` | | TLS certificate file |
| `--tls-key` | | TLS private key file |
| `--insecure-dev-cert` | `false` | Use a throwaway self-signed certificate |
| `--pool` | `10.240.0.0/12` | IP pool for virtual addresses |
| `--mtu` | `1300` | MTU announced to clients |
| `--web` | | Start a local HTTP status page, e.g. `127.0.0.1:18080` |
| `--web-token` | | Bearer token required for the HTTP status page |

## Troubleshooting

**"Permission denied" on Linux** -- The client needs root to create a TAP
device. Run with `sudo`.

**"Permission denied" on macOS** -- The client needs root to create and
configure a `utun` interface. Run with `sudo`.

**Game doesn't see other players** -- Make sure everyone is in the same room
and that the game uses LAN/local discovery. Check that each client received an
IP (`ip addr show anylan0` on Linux, `ifconfig utunX` on macOS, or `ipconfig`
on Windows). On macOS, try joining by the peer's virtual IP if automatic LAN
discovery does not show the room.

**Leftover network adapter after a crash** -- On Linux:
`sudo ip link delete anylan0`

## Building from Source

```bash
make test          # run the test suite
make build         # build client and server for the current platform
make build-linux   # cross-compile Linux binaries (client/server amd64)
make build-windows # cross-compile Windows client (amd64)
make build-darwin  # cross-compile Darwin/macOS clients (amd64 + arm64)
make clean         # remove built binaries
```

Cross-compiled outputs are named by role, OS, and architecture:
`anylan-client-linux-amd64`, `anylan-server-linux-amd64`,
`anylan-client-windows-amd64.exe`, `anylan-client-darwin-amd64`, and
`anylan-client-darwin-arm64`.

Or directly with Go:

```bash
go test ./...
go build -o out/anylan-client ./cmd/client
go build -o out/anylan-server ./cmd/server
```
