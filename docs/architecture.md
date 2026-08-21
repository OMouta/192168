# Architecture

## The one rule

The coordination server introduces peers. It never carries game traffic. Once
two daemons have found each other, their session survives the server going down.

A game packet takes this path and touches nothing else:

```
Game -> Windows IP stack -> Wintun adapter -> daemon
     -> encrypted UDP over the internet ->
        daemon -> Wintun adapter -> Windows IP stack -> Game
```

## Concepts

Three things stay separate.

**Device identity.** One per installation. A random device ID and two key pairs.
The private halves never leave the machine.

**Membership.** A device's place in a group, and the virtual IP it holds there.
You create it by joining with the group password. After that the device token
gets you back in.

**Session.** Exists only while connected. Holds the current endpoint candidates
and the peer list.

One session at a time. Switching groups disconnects the first one first.

## Identity and passwords

A device has two keys. Ed25519 signs its registration, which proves it holds the
key it is registering. Curve25519 is the static key peers run the Noise
handshake against. The registration signature covers both.

Registration is the only unauthenticated write in the API, so it carries a
timestamp and a nonce. The server rejects anything stale or repeated and hands
back a bearer token. Every later request carries that token.

The group password stays on the machine. The daemon runs Argon2id over it and
sends the result, called the proof. The salt is the group name, so two people
typing the same password produce the same proof. The server runs Argon2id over
the proof with a random salt and stores that.

Steal the database and you get an offline guessing problem. Intercept a proof and
you are in the group, so the control plane refuses anything but TLS.

There is no membership credential. The token identifies the device and the
server knows its groups. Revoking a membership is a row on the server, so it
takes effect while the device is online.

The password crosses the named pipe in the clear. The daemon owns all
cryptography and the UI never implements a KDF, so the pipe is ACLed to the
current user.

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

The client sends intents like `ConnectGroup` and draws whatever state comes
back. It never opens a socket, punches a NAT, or tracks peer state. The daemon
is its own process, so closing the window leaves the tunnel up.

## Connecting

1. Client sends `ConnectGroup`.
2. Daemon opens a session on the server with its device token.
3. Server answers with the address this membership holds.
4. Daemon brings up the Wintun adapter and local routes.
5. Daemon opens its UDP socket and finds its public endpoint through STUN.
6. Daemon publishes that endpoint and reads the peer list.
7. Daemon and each peer punch at the same time, then run the handshake.
8. Open sessions become routes for the adapter.

Links are independent. One peer failing says nothing about the rest.

## Addressing

An address belongs to a membership. The server hands one out when a device
joins, the lowest free host address in the group's subnet, `10.69.0.0/24`, and
it stays that device's for as long as it is a member. Connecting takes no
address and disconnecting gives none back.

That is what makes a host worth writing down. Somebody who ran a server last
night is at the same address tonight, whoever else connected first, and their
friends can read it off the members list while they are still offline.

Leaving a group frees the address for the next person to join. The row keeps
the value while it is revoked, so somebody who leaves and comes back takes the
same one again unless a newcomer claimed it in between. A group with no free
addresses left refuses new members rather than new connections.

Addresses are unique inside a group and nowhere else. Only one group is active
at a time, so two groups reusing a range never collide on the local routes.

## Routing

Members form a full mesh. Under ten peers that is cheap.

The daemon maps each peer's virtual IP to a route. A route is not a socket.
`Direct` is a peer's own address. `Via(peer)` is somebody else carrying for
them. `Relay`, a hosted server doing the same, has a place in the protocol and
is not implemented.

## Peer-assisted routing

Two NATs sometimes will not open to each other however long both sides punch.
When that happens the group is usually still one hop apart: both of them can
reach somebody, even if they cannot reach each other.

A link that has spent its direct attempts asks each peer with an open link, in
turn, to carry it. What crosses that peer is `MsgForward`: the two virtual
addresses, a hop limit, and the packet itself. The packet inside is an ordinary
transport packet between the two ends, so the handshake, the keepalives, the
latency and the game traffic all take the road without knowing which road it
is, and the peer in the middle moves bytes it does not hold the keys to.

The hop limit is what stops a mistake becoming a packet that circles forever. A
daemon only passes a packet to a link it holds directly, so one hop is all this
arranges; the field carries more, so a longer path would not be a change to the
format.

A relayed packet is a full packet inside a second envelope, which is 45 bytes
past what an ordinary path carries without fragmenting. A full size packet is
therefore split once on its way through. That is what the fallback costs, and it
is a better deal than two people who cannot reach each other at all.

## Transport

Binary and versioned. See `protocol/transport`. A 20-byte envelope of magic,
version, type, sender, and counter sits in front of the payload. The AEAD signs
the envelope as additional data, so a daemon reads it for routing and an
attacker cannot change it.

The magic keeps its top two bits set. STUN shares the socket and its messages
start with two zero bits, so the two can be told apart without parsing.

Noise IK over Curve25519, ChaCha20-Poly1305, and BLAKE2s. The header counter is
the AEAD nonce and the replay window's input, so a receiver can tell a replay
from a reordered packet. We do not write our own crypto.

An endpoint from the server proves nothing. A peer accepts only what verifies
against the session keys.

## Coordination

HTTPS plus a WebSocket carrying presence and endpoint changes. The daemon never
polls. Losing the WebSocket leaves tunnels alone and games running. What stops is
hearing about new peers and endpoint changes.

A client reaches any deployment from a base URL by fetching
`/.well-known/192168`, which returns the API URL, realtime URL, STUN servers, and
feature flags. A user never types those separately.

## Network changes

Wi-Fi switches, sleep and resume, and a new public IP all break NAT mappings. The
daemon revalidates its socket, re-runs STUN, publishes the new endpoint, and
punches again. Membership and UI state are untouched.

## Broadcast discovery

The overlay is Layer 3, so nothing carries a broadcast on its own: there is no
shared segment for one to reach and no switch to flood a multicast to. A game
where you type the host's IP works either way. A game with a LAN list needs two
things, and both are behind one switch in Settings, on by default.

The first is replication. A packet leaving the adapter for the group's broadcast
address, for `255.255.255.255`, or for any multicast group is copied onto every
open link instead of being routed to one peer. Copies only ever go out, so there
is no loop: a replicated packet arriving from a peer is written to the adapter
and no further.

The second is which interface Windows picks. Multicast leaves by one interface,
the one with the best route for `224.0.0.0/4`, and a group is joined on that same
one. Every interface has that route, so an app that does not name one, which is
nearly all of them, would announce over the Wi-Fi card and never reach the
adapter at all. The daemon gives the adapter a metric of 1 while connected, so
the tunnel wins.

That is the invasive half, and it is why there is a switch. Winning multicast
means winning it for everything, so mDNS and SSDP go down the tunnel too and
nearby speakers, printers and TVs stop being found until you disconnect. Nothing
else moves: the adapter carries no default route, so ordinary internet traffic
never consults it.

## Versioning

Discovery, transport, IPC, and realtime carry separate version numbers in
`protocol`. A self-hosted server, a shipped client, and a peer's daemon update on
their own schedules. When they disagree the user gets a clear message.

## Logging

Structured logs with component, group, peer, session, endpoint, and state
transitions. Never log passwords, private keys, tokens, or decrypted packets.
