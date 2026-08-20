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

**Membership.** A device's place in a group. You create it by joining with the
group password. After that the device token gets you back in.

**Session.** Exists only while connected. Holds the assigned virtual IP, the
current endpoint candidates, and the peer list.

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
3. Server assigns a virtual IP in the group's subnet, `10.69.0.0/24`.
4. Daemon brings up the Wintun adapter and local routes.
5. Daemon opens its UDP socket and finds its public endpoint through STUN.
6. Daemon publishes that endpoint and reads the peer list.
7. Daemon and each peer punch at the same time, then run the handshake.
8. Open sessions become routes for the adapter.

Links are independent. One peer failing says nothing about the rest.

## Routing

Members form a full mesh. Under ten peers that is cheap, and nothing in the
middle forwards packets.

The daemon maps each peer's virtual IP to a route. A route is not a socket. Only
`Direct` exists. `Via(peer)` and `Relay` have places in the protocol and hop
limits reserved, and neither is implemented.

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

The overlay is Layer 3. UDP broadcast, multicast, and mDNS do not cross it. Games
where you type the host's IP work. Server browsers that scan the LAN find
nothing, and fixing that means replicating broadcast packets to every peer.

## Versioning

Discovery, transport, IPC, and realtime carry separate version numbers in
`protocol`. A self-hosted server, a shipped client, and a peer's daemon update on
their own schedules. When they disagree the user gets a clear message.

## Logging

Structured logs with component, group, peer, session, endpoint, and state
transitions. Never log passwords, private keys, tokens, or decrypted packets.
