# Architecture

## The one rule

The coordination server introduces peers to each other. It never carries game
traffic. Once two daemons have found each other, their session survives the
server going down.

## Concepts

Three things that are deliberately separate:

- **Device identity** — one per installation. A random device ID and a
  persistent key pair; the private key never leaves the machine.
- **Membership** — the relationship between a device and a group. Created by
  joining with the group password, then represented by a long-lived credential
  so the password is never needed again. Nicknames are per-group display
  labels, not identity.
- **Session** — exists only while connected. Carries the assigned virtual IP,
  the current endpoint candidates, and the peer list.

Only one group session is active at a time. Switching groups means fully
disconnecting first.

## Processes

```
+-----------------------------+
| WinUI 3 client (C#)         |
| UI, tray, settings          |
+--------------+--------------+
               | named pipe (JSON)
+--------------v--------------+
| Daemon (Go)                 |
| identity, credentials       |
| Wintun, virtual addressing  |
| STUN, NAT traversal         |
| peer sessions, encryption   |
| routing, reconnection       |
+--------------+--------------+
               | UDP, encrypted
            internet
```

The client sends intents — `ConnectGroup`, `Disconnect`, `JoinGroup` — and
renders the state it is pushed. It does not open sockets, punch NATs, or track
peer state. The daemon runs as its own process so closing the window does not
drop the tunnel.

## Connecting

1. Client sends `ConnectGroup`.
2. Daemon validates the stored membership credential and creates a session on
   the server.
3. Server assigns a virtual IP within the group's subnet (`10.69.0.0/24`).
4. Daemon brings up the Wintun adapter and local routes.
5. Daemon opens its UDP socket and discovers its public endpoint via STUN.
6. Daemon publishes the endpoint and receives the peer list.
7. Daemon and each peer hole-punch simultaneously, then run an authenticated
   handshake.
8. Established sessions become routes for the virtual adapter.

Every pairwise link is independent. Failing to reach one peer must never affect
the others.

## Routing

Connected members form a full mesh — fine at the intended scale of under ten
peers, and it keeps packet forwarding out of the middle.

The daemon maps each peer's virtual IP to a route. A route is an abstraction,
not a socket: only `Direct` exists today, but `Via(peer)` (peer-assisted) and
`Relay` are shaped into the protocol so adding them later is not a rewrite.
Anything forwarded carries a hop limit.

## Transport

Binary and versioned from day one — see `protocol/transport`. A fixed 20-byte
envelope (magic, version, type, sender, counter) precedes the payload and is
authenticated as additional data, so it can be read for routing without being
tamperable.

The counter drives both the AEAD nonce and replay rejection. All peer traffic
is encrypted end to end using an established construction — no custom
cryptography. An endpoint learned from the server proves nothing; only packets
that verify against the session keys are accepted.

## Coordination

The control plane is HTTPS plus a WebSocket carrying presence and endpoint
changes, so the daemon never polls. Losing the WebSocket does not tear down
established tunnels: during an outage, existing games keep working, but new
peers cannot be discovered and endpoint changes stop propagating.

Clients reach any deployment from a base URL alone by fetching
`/.well-known/192168`, which advertises the API URL, realtime URL, STUN
servers, and optional features. API, realtime, and STUN addresses are never
exposed as separate user-facing settings.

No server is baked into the client. The daemon starts with none configured and
waits to be told, so the first-run flow is the user typing in a server — the
same path a self-hoster uses, rather than a special case bolted onto a default.

## Network changes

Wi-Fi switches, sleep/resume, and public IP changes all invalidate NAT
mappings. On a detected change the daemon revalidates its socket, re-runs STUN,
publishes the new endpoint, and re-punches — without disturbing membership or
UI state.

## Broadcast discovery

The overlay is Layer 3, so UDP broadcast, multicast, and mDNS do not cross it.
Games where you type in the host's IP work; automatic server-browser discovery
may need selective broadcast replication later. That is not a blocker for the
first working version.

## Versioning

Discovery, transport, IPC, and realtime are versioned independently in
`protocol`, because a self-hosted server, a shipped client, and a peer's daemon
all update on their own schedules. Incompatibility must surface as a clear
message, not a mysterious failure.

## Logging

Structured logs with component, group, peer, session, endpoint, and state
transitions. Never log passwords, private keys, membership credentials, or
decrypted packet contents.
