# Self-hosting

The shipped Windows app defaults to the hosted server, and users reach any
other one by typing its URL into Settings. Nothing is rebuilt to point the
client at yours.

## Requirements

- A public HTTPS domain (TLS terminated by your reverse proxy).
- Docker, or a Go 1.26+ toolchain.

## Run it

```sh
cd deploy/docker
cp .env.example .env      # set NET192168_PUBLIC_URL
docker compose up -d
```

Point your reverse proxy at port 8080 and confirm the discovery document is
reachable:

```sh
curl https://lan.example.com/.well-known/192168
```

```json
{
  "version": 1,
  "api": "https://lan.example.com/api",
  "realtime": "wss://lan.example.com/realtime",
  "stun": ["stun:stun.l.google.com:19302"],
  "relay": null,
  "features": { "relay": false, "peerRouting": false }
}
```

Then open the app, enter `https://lan.example.com` as the server, test the
connection, and save.

## Configuration

| Variable | Required | Description |
| --- | --- | --- |
| `NET192168_PUBLIC_URL` | yes | Public base URL. Must match what users type in. HTTPS unless localhost. |
| `NET192168_ADDR` | no | Listen address. Defaults to `:8080`. |
| `NET192168_STUN` | no | Comma-separated STUN servers to advertise. Defaults to a public one. |
| `NET192168_DATABASE_URL` | no | Storage DSN. Defaults to local SQLite. |

The API and realtime URLs are derived from `NET192168_PUBLIC_URL`, so there is
one address to get right, and it is the same one your users type in.

## Notes

- The server does not forward game traffic. It stays small and idle even with
  several active groups.
- Group passwords are stored as verifiers, never in plaintext.
- Your users' peers connect directly to each other, so the server going down
  does not drop games already in progress.
