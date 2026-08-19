# 192168

192168 puts you and your friends on the same LAN from different networks, so you
can play LAN games together.

Pick a nickname, create or join a private group, hit Connect. Everyone in the
group gets a virtual LAN IP. Games that let you type in a host address work
without anything else.

> The server introduces peers. The clients create the LAN.

Traffic goes straight between peers over encrypted UDP. The coordination server
handles group membership, presence, and endpoint exchange, then gets out of the
way. It never sees a game packet.

Status: early development. Not usable yet.

## What it is not

Not a privacy VPN. Not an exit node. Not a Tailscale or ZeroTier competitor, and
not built for hundreds of peers. It is built for four to six friends who want to
play a LAN game, on Windows.

## How it works

```
Game -> Windows IP stack -> Wintun adapter -> daemon
     -> encrypted UDP over the internet ->
        daemon -> Wintun adapter -> Windows IP stack -> Game
```

Two processes on the client.

**WinUI 3 client** (C#). The UI, tray, and settings. Presentation only.

**Daemon** (Go). Device identity, membership credentials, the virtual adapter,
STUN, NAT traversal, encryption, peer sessions, routing. It decides what the
connection state is, and the UI shows what it reports. Closing the window does
not drop a tunnel.

They talk over a named pipe. See [docs/architecture.md](docs/architecture.md).

## Layout

```
protocol/   shapes shared across process and machine boundaries
  transport/  binary peer-to-peer UDP wire format
  ipc/        local client <-> daemon control protocol
  api/        control-plane HTTP and WebSocket payloads
daemon/     Go networking daemon (Windows)
server/     Go coordination server
client/     WinUI 3 desktop client
deploy/     Docker deployment for the server
docs/       architecture and self-hosting
```

A name cannot start with a digit in an environment variable, a C# namespace, or
a Go identifier. Those read `Net192168` and `NET192168_`. Everything a user sees
is `192168`, including the app, the well-known path, and the virtual adapter.

## Development

Requires Go 1.26+ and the .NET 10 SDK.

```sh
go test ./...
go build ./daemon/cmd/192168-daemon
go build ./server/cmd/192168-server

dotnet build client/windows/192168.slnx -p:Platform=x64
```

Run the server locally:

```sh
NET192168_PUBLIC_URL=http://localhost:8080 go run ./server/cmd/192168-server
curl http://localhost:8080/.well-known/192168
```

Plain HTTP only works against localhost. Everything else has to be HTTPS.

## Servers

The app talks to `https://api.192168.lol` by default. To use a different one,
type its URL into Settings. The client reads the API, realtime, and STUN
addresses from that server's discovery document, so pointing the shipped binary
at another deployment never means rebuilding it.

Running your own takes a Docker compose file and a domain. See
[docs/self-hosting.md](docs/self-hosting.md).

## License

MIT. See [LICENSE](LICENSE).
