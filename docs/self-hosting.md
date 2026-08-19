# Self-hosting

The Windows app talks to the hosted server by default, and a user reaches any
other one by typing its URL into Settings. Nobody rebuilds anything to point a
client at yours.

## Requirements

- A public HTTPS domain, with your reverse proxy terminating TLS.
- Docker, or a Go 1.26+ toolchain.

## Run it

```sh
cd deploy/docker
cp .env.example .env      # set NET192168_PUBLIC_URL
docker compose up -d
```

Point your reverse proxy at port 8080, then check that the discovery document
comes back:

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

Now open the app, enter `https://lan.example.com` as the server, test the
connection, and save.

## Configuration

| Variable | Required | Description |
| --- | --- | --- |
| `NET192168_PUBLIC_URL` | yes | Public base URL. Must match what users type in. HTTPS unless localhost. |
| `NET192168_ADDR` | no | Listen address. Defaults to `:8080`. |
| `NET192168_STUN` | no | Comma-separated STUN servers to advertise. Defaults to a public one. |
| `NET192168_DATABASE_URL` | no | Storage DSN. Defaults to local SQLite. |

The server builds the API and realtime URLs from `NET192168_PUBLIC_URL`, so you
have one address to get right, and it is the same one your users type in.

## Notes

- The server never forwards game traffic. It stays small and idle even with
  several groups connected.
- The server stores a verifier for each group password, never the password.
- Peers connect straight to each other, so the server going down does not drop
  a game already in progress.
