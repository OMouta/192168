# Running the server on Railway

Railway is the least work of any host: it builds the Dockerfile, gives you HTTPS
and a domain, and needs one variable set.

## Setup

1. Create a Railway project from this repository.

2. Point the service at this config. Under Settings, Config as Code, set the
   path to `deploy/railway/railway.json`. It selects the Dockerfile, health
   checks `/api/health`, and only rebuilds when the server, the protocol, or the
   Dockerfile change.

3. Generate a domain under Settings, Networking.

4. Set the one required variable:

   ```
   NET192168_PUBLIC_URL = https://${{RAILWAY_PUBLIC_DOMAIN}}
   ```

   This is the address users type into the app, and the server refuses to start
   without it rather than advertising something wrong.

5. Add a volume mounted at `/data` and set:

   ```
   NET192168_DATABASE_URL = /data/192168.db
   ```

   Without a volume the database is part of the container, so every deploy
   forgets every group.

Nothing needs to be set for the port. Railway supplies `PORT` and the server
listens on it.

## Check it

```sh
curl https://your-app.up.railway.app/.well-known/192168
```

A compatible server answers with its API and realtime URLs and the STUN servers
it wants clients to use. Paste the domain into the app under Settings and press
Test.

## Railway cannot host the relay

Railway's public networking is HTTP and a TCP proxy. There is no UDP, and a
relay forwards UDP between peers, so it cannot live here.

That costs nothing today. The relay is not built, and normal operation does not
use one: peers talk straight to each other, and STUN comes from public servers.
If a relay is ever added it needs a host that gives you UDP ports, which means a
VPS. The coordination server can stay on Railway either way, because it only
ever speaks HTTP and WebSocket.

## Variables

| Variable | Required | Description |
| --- | --- | --- |
| `NET192168_PUBLIC_URL` | yes | Public base URL, normally `https://${{RAILWAY_PUBLIC_DOMAIN}}`. |
| `NET192168_DATABASE_URL` | no | Storage path. Set it to a volume or lose data on deploy. |
| `NET192168_STUN` | no | Comma-separated STUN servers to advertise instead of the default. |
| `NET192168_ADDR` | no | Listen address. Leave it unset so `PORT` is used. |
