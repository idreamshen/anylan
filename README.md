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

| | Linux | Windows |
|---|---|---|
| **Client** | Root privileges (for creating the virtual network adapter) | Administrator shell + [OpenVPN TAP driver](https://community.openvpn.net/openvpn/wiki/ManagingWindowsTAPDrivers) installed |
| **Server** | Any Linux machine with a public UDP port | Not supported yet |

Download pre-built binaries from the
[Releases](https://github.com/idreamshen/anylan/releases) page, or build from
source (requires Go 1.21+):

```bash
make build
```

Windows client cross-compile:

```bash
make build-windows
```

### 1. Start the Server

On a machine reachable by all players:

```bash
./out/anylan-server --listen :4433 --tls-cert cert.pem --tls-key key.pem
```

For quick testing without real TLS certificates:

```bash
./out/anylan-server --listen :4433 --insecure-dev-cert --web 127.0.0.1:8080
```

The `--web` flag is optional but recommended: it exposes a status page at the
given address where you can see connected rooms and peers. Use `--web-token` if
the status page is reachable by anyone else.

### 2. Join a Room

Pick any room name. Everyone who uses the same name ends up on the same virtual
LAN.

**Linux:**

```bash
sudo ./out/anylan-client join \
  --server your-server-ip:4433 \
  --room my-room
```

For a server started with `--room-key`, clients must pass the same
`--room-key` value when joining.

**Windows** (run as Administrator):

```powershell
.\out\anylan-client.exe join `
  --server your-server-ip:4433 `
  --room my-room
```

Each client gets a virtual IP like `10.240.x.y`. Once everyone has joined,
launch your game and look for LAN/local games -- you should see each other.

### 3. Stop

Press `Ctrl-C` in the client terminal to leave the room and clean up.

## Client Options

| Flag | Default | Description |
|---|---|---|
| `--server` | *(required)* | Server address, e.g. `1.2.3.4:4433` |
| `--room` | *(required)* | Room code to join |
| `--room-key` | | Optional room access key required by the server |
| `--name` | | Display name shown to other players |
| `--dev` | `anylan0` | Virtual network adapter name (on Windows, the TAP adapter friendly name, e.g. `"Ethernet 3"`) |
| `--insecure-skip-verify` | `false` | Skip TLS certificate check (for testing only) |
| `--web` | | Start a local HTTP status page, e.g. `127.0.0.1:8081` |
| `--web-token` | | Bearer token required for the HTTP status page |

## Server Options

| Flag | Default | Description |
|---|---|---|
| `--listen` | `:4433` | UDP listen address |
| `--room-key` | | Optional room access key required from clients |
| `--tls-cert` | | TLS certificate file |
| `--tls-key` | | TLS private key file |
| `--insecure-dev-cert` | `false` | Use a throwaway self-signed certificate |
| `--pool` | `10.240.0.0/12` | IP pool for virtual addresses |
| `--mtu` | `1300` | MTU announced to clients |
| `--web` | | Start a local HTTP status page, e.g. `127.0.0.1:8080` |
| `--web-token` | | Bearer token required for the HTTP status page |

## Troubleshooting

**"Permission denied" on Linux** -- The client needs root to create a TAP
device. Run with `sudo`.

**Game doesn't see other players** -- Make sure everyone is in the same room
and that the game uses LAN/local discovery. Check that each client received an
IP (`ip addr show anylan0` on Linux, or `ipconfig` on Windows).

**Leftover network adapter after a crash** -- On Linux:
`sudo ip link delete anylan0`

## Building from Source

```bash
make test          # run the test suite
make build         # build client and server for the current platform
make build-windows # cross-compile Windows client (anylan-client.exe)
make clean         # remove built binaries
```

Or directly with Go:

```bash
go test ./...
go build -o out/anylan-client ./cmd/client
go build -o out/anylan-server ./cmd/server
```
