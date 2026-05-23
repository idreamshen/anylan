# anylan

`anylan` is a Go MVP for a ZeroTier-like virtual LAN aimed at LAN-discovery games.

The first version is intentionally small:

- Linux client only.
- Layer 2 TAP device.
- Central QUIC/UDP relay server.
- One room code equals one isolated broadcast domain.
- No P2P, NAT traversal, accounts, persistence, DHCP, or GUI.

## Build

```bash
go build ./cmd/anylan
go build ./cmd/anylan-server
```

## Local Development

Start a relay with a temporary self-signed certificate:

```bash
go run ./cmd/anylan-server --listen :4433 --insecure-dev-cert
```

Join from a Linux client:

```bash
sudo go run ./cmd/anylan -- join \
  --server 127.0.0.1:4433 \
  --room my-room \
  --dev anylan0 \
  --insecure-skip-verify
```

For production-like use, pass `--tls-cert` and `--tls-key` to the server and omit
`--insecure-skip-verify` on clients.

## Manual Acceptance

On two Linux machines:

1. Start `anylan-server` on a public UDP port.
2. Start `anylan join` on both clients with the same room code.
3. Confirm both TAP interfaces receive `10.240.x.y/24` addresses.
4. Ping the peer virtual IP.
5. Start a LAN-discovery game and verify room discovery or joining works.

## Tests

```bash
go test ./...
```
