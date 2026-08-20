# Self-hosting

Users type your server's URL into Settings. The app is not rebuilt for it.

## Requirements

- A public HTTPS domain, with your reverse proxy terminating TLS.
- Docker, or a Go 1.26+ toolchain.

## Railway

A couple of variables and a volume. See [deploy/railway](../deploy/railway/README.md).

Railway has no UDP. If a relay is ever built it will not run there.

## Docker anywhere else

```sh
cd deploy/docker
cp .env.example .env      # set NET192168_PUBLIC_URL
docker compose up -d
```

Point your reverse proxy at port 8080. Then check the discovery document:

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

Open the app, enter `https://lan.example.com` as the server, press Test, save.

## Configuration

| Variable | Required | Description |
| --- | --- | --- |
| `NET192168_PUBLIC_URL` | yes | Public base URL. Must match what users type in. HTTPS unless localhost. |
| `NET192168_ADDR` | no | Listen address. Falls back to `PORT`, then `:8080`. |
| `NET192168_STUN` | no | Comma-separated STUN servers to advertise. Defaults to a public one. |
| `NET192168_DATABASE_URL` | no | Storage path or DSN. Defaults to SQLite in the working directory. |

The API and realtime URLs come from `NET192168_PUBLIC_URL`. Set that one wrong
and clients will be sent to an address that does not answer.

## What running this signs you up for

Game traffic never touches it. The server hands out addresses and says who is
online, so a group of six is a few requests an hour and one WebSocket each.

It stores an Argon2id verifier per group password, never the password.

If it goes down, games already running keep going. Nobody new can connect until
it is back.
