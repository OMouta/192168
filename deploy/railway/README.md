# Running the server on Railway

Railway builds the Dockerfile and gives you HTTPS and a domain. Setup is one
variable and a volume.

## Setup

1. Create a Railway project from this repository.

2. Under Settings, Config as Code, set the path to
   `deploy/railway/railway.json`. It picks the Dockerfile, health checks
   `/api/health`, and rebuilds only when the server, the protocol, or the
   Dockerfile change.

3. Generate a domain under Settings, Networking.

4. Set:

   ```
   NET192168_PUBLIC_URL = https://${{RAILWAY_PUBLIC_DOMAIN}}
   ```

   The server exits at startup without this rather than guess at its own
   address.

5. Add a volume mounted at `/data` and set:

   ```
   NET192168_DATABASE_URL = /data/192168.db
   ```

   Skip this and every deploy wipes the groups.

Leave the port alone. Railway sets `PORT` and the server listens on it.

## Check it

```sh
curl https://your-app.up.railway.app/.well-known/192168
```

You should get back the API and realtime URLs and a list of STUN servers. Then
paste the domain into the app under Settings and press Test.

## No UDP here

Railway does HTTP and a TCP proxy. A relay forwards UDP between peers, so it
cannot run on Railway.

Nothing needs it right now. There is no relay, peers connect directly, and STUN
comes from public servers. If that changes, the relay goes on a VPS with real
UDP ports and the coordination server stays here.

## Variables

| Variable | Required | Description |
| --- | --- | --- |
| `NET192168_PUBLIC_URL` | yes | Public base URL, normally `https://${{RAILWAY_PUBLIC_DOMAIN}}`. |
| `NET192168_DATABASE_URL` | no | Storage path. Point it at a volume or lose data on deploy. |
| `NET192168_STUN` | no | Comma-separated STUN servers to advertise instead of the default. |
| `NET192168_ADDR` | no | Listen address. Leave unset so `PORT` is used. |
