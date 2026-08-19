# 192168

192168 puts you and your friends on the same LAN, from different networks, so
you can play LAN games together.

You pick a nickname, create or join a private group, hit Connect, and everyone
in the group gets a virtual LAN IP. Games that let you connect to a host by IP
just work.

> The server introduces peers. The clients create the LAN.

Traffic goes **directly between peers** over encrypted UDP. The coordination
server only handles group membership, presence, and endpoint exchange — it
never sees your game traffic, and it is not in the data path.

**Status: early development.** Not usable yet.

## What it is not

Not a privacy VPN, not an exit node, not a Tailscale/ZeroTier competitor, and
not built for hundreds of peers. It is built for 4–6 friends who want to play a
LAN game, and Windows is the only client platform.

## How it works

```
Game -> Windows IP stack -> Wintun adapter -> daemon
     -> encrypted UDP over the internet ->
        daemon -> Wintun adapter -> Windows IP stack -> Game
```

Two processes on the client:

- **WinUI 3 client** (C#) — the UI, tray, and settings. Presentation only.
- **Daemon** (Go) — device identity, membership credentials, virtual adapter,
  STUN, NAT traversal, encryption, peer sessions, routing. The daemon is the
  source of truth for connection state, and peer sessions keep running when the
  UI is closed.

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

A name cannot start with a digit in an environment variable, a C# namespace,
or a Go identifier, so those use `Net192168` / `NET192168_`. Everything
user-visible — the app, the well-known path, the virtual adapter — is `192168`.

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

Plain HTTP is only accepted for localhost; everything else must be HTTPS.

## Servers

There is no default server baked into the app. On first run you type in the
server you were given or the one you run yourself, and the client learns the
API, realtime, and STUN endpoints from its discovery document. That is the only
address a user ever sees, and pointing the shipped client at a different
deployment never requires a rebuild.

Running your own is a Docker compose file and a domain — see
[docs/self-hosting.md](docs/self-hosting.md).

## License

MIT. See [LICENSE](LICENSE).
