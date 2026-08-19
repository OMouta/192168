# Architecture

## The one rule

The coordination server introduces peers to each other. It never carries game
traffic. Once two daemons have found each other, their session survives the
server going down.

A game packet takes this path and touches nothing else:

```
Game -> Windows IP stack -> Wintun adapter -> daemon
     -> encrypted UDP over the internet ->
        daemon -> Wintun adapter -> Windows IP stack -> Game
```

## Concepts

Three things stay separate on purpose.

**Device identity.** One per installation. A random device ID and a persistent
key pair. The private key never leaves the machine.

**Membership.** The relationship between a device and a group. You create it by
joining with the group password, and from then on a long-lived credential stands
in for the password. Nicknames are per-group display labels and carry no
identity.

**Session.** Exists only while connected. Carries the assigned virtual IP, the
current endpoint candidates, and the peer list.

One group session is active at a time. Switching groups means disconnecting the
first one completely.

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

The client sends intents like `ConnectGroup`, `Disconnect`, and `JoinGroup`,
then draws whatever state comes back. It never opens a socket, punches a NAT, or
tracks peer state. The daemon is a separate process, so closing the window
leaves the tunnel up.

## Connecting

1. Client sends `ConnectGroup`.
2. Daemon validates the stored membership credential and creates a session on
   the server.
3. Server assigns a virtual IP within the group's subnet (`10.69.0.0/24`).
4. Daemon brings up the Wintun adapter and local routes.
5. Daemon opens its UDP socket and finds its public endpoint through STUN.
6. Daemon publishes the endpoint and receives the peer list.
7. Daemon and each peer hole-punch at the same time, then run an authenticated
   handshake.
8. Established sessions become routes for the virtual adapter.

Every pairwise link stands alone. Failing to reach one peer must never affect
the others.

## Routing

Connected members form a full mesh. That is fine under ten peers and keeps
packet forwarding out of the middle.

The daemon maps each peer's virtual IP to a route, and a route is not the same
thing as a socket. Only `Direct` exists today. `Via(peer)` and `Relay` already
have a place in the protocol, so adding them later will not mean a rewrite.
Anything forwarded carries a hop limit.

## Transport

Binary and versioned from day one. See `protocol/transport`. A fixed 20-byte
envelope of magic, version, type, sender, and counter sits in front of the
payload. The AEAD authenticates it as additional data, so a daemon can read it
for routing and an attacker still cannot change it.

The counter feeds the AEAD nonce and the replay check. Peer traffic is encrypted
end to end with an established construction. We do not write our own crypto.

An endpoint learned from the server proves nothing on its own. A peer accepts
only packets that verify against the session keys.

## Coordination

The control plane is HTTPS plus a WebSocket that carries presence and endpoint
changes, so the daemon never polls. Losing the WebSocket leaves established
tunnels alone. Games in progress keep working. What stops is discovering new
peers and learning about endpoint changes.

A client reaches any deployment from a base URL alone by fetching
`/.well-known/192168`, which advertises the API URL, realtime URL, STUN servers,
and optional features. Those addresses never appear as separate settings a user
has to fill in.

The client points at the hosted deployment out of the box and reaches any other
by URL, so a self-hosted server runs the same code path as the default one.

## Network changes

Wi-Fi switches, sleep and resume, and public IP changes all invalidate NAT
mappings. When the daemon detects one it revalidates its socket, re-runs STUN,
publishes the new endpoint, and punches again. Membership and UI state stay
where they are.

## Broadcast discovery

The overlay is Layer 3, so UDP broadcast, multicast, and mDNS do not cross it.
Games where you type in the host's IP work today. Automatic server-browser
discovery may need selective broadcast replication later, and that can wait
until the first version works.

## Versioning

Discovery, transport, IPC, and realtime carry separate version numbers in
`protocol`, because a self-hosted server, a shipped client, and a peer's daemon
all update on their own schedules. When they disagree the user should get a
clear message rather than a mysterious failure.

## Logging

Structured logs with component, group, peer, session, endpoint, and state
transitions. Never log passwords, private keys, membership credentials, or
decrypted packet contents.
