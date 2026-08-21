package mesh

import (
	"context"
	"net/netip"
	"time"

	"github.com/OMouta/192168/daemon/stun"
	"github.com/OMouta/192168/protocol"
	"github.com/OMouta/192168/protocol/ipc"
	"github.com/OMouta/192168/protocol/session"
	"github.com/OMouta/192168/protocol/transport"
)

// source is where a packet came from, and so where an answer goes back.
//
// A packet off the wire is known by the address it arrived from. A packet
// another peer forwarded has no address of its own: it is known by the virtual
// address in the forward header, and its answer goes back through whoever
// carried it.
type source struct {
	addr  netip.AddrPort
	peer  *Peer
	relay *Peer
}

// handle sorts one received datagram. STUN replies and peer packets share the
// socket and are told apart by their first two bits, so neither has to be
// parsed to rule out the other.
func (m *Mesh) handle(packet []byte, from netip.AddrPort) {
	if stun.Looks(packet) {
		m.handleStun(packet)
		return
	}

	header, payload, err := transport.Decode(packet)
	if err != nil {
		// Scanners and stray traffic reach any open port. Dropping quietly is
		// the right answer, and logging every one would be a gift to whoever
		// is scanning.
		return
	}

	switch header.Type {
	case transport.MsgProbe:
		// Punching is about this machine's own address, so a probe is only
		// ever meaningful off the wire.
		m.handleProbe(payload, from)
	case transport.MsgForward:
		m.handleForward(packet, payload, header.Counter, from)
	default:
		m.dispatch(header, packet, payload, source{addr: from, peer: m.peerAt(from)})
	}
}

// dispatch hands one packet to the handler for its type, whether it arrived
// down the wire or inside somebody else's forward.
func (m *Mesh) dispatch(header transport.Header, packet, payload []byte, src source) {
	switch header.Type {
	case transport.MsgHandshakeInit:
		m.handleHandshakeInit(payload, src)
	case transport.MsgHandshakeResponse:
		m.handleHandshakeResponse(payload, src)
	case transport.MsgKeepalive:
		m.handleKeepalive(payload, src)
	case transport.MsgData:
		m.handleData(packet, payload, header.Counter, src)
	case transport.MsgClose:
		m.handleClose(src)
	}
}

// handleForward takes a packet a peer sent us on somebody's behalf, and either
// delivers it or passes it on.
func (m *Mesh) handleForward(packet, ciphertext []byte, counter uint64, from netip.AddrPort) {
	relay := m.peerAt(from)
	if relay == nil {
		m.noteStranger(from)
		return
	}

	// Decrypting first is what proves the peer really sent this. Nobody else
	// can ask this daemon to carry anything.
	body, err := relay.accept(packet[:transport.HeaderSize], ciphertext, counter)
	if err != nil {
		return
	}

	carried, err := transport.DecodeForward(body)
	if err != nil {
		return
	}

	if carried.Destination == m.virtualIP {
		m.deliverForwarded(carried, relay)
		return
	}
	m.passOn(carried, relay)
}

// deliverForwarded opens a packet addressed to this device. What is inside is
// an ordinary packet from the peer at the source address, so it goes through
// the same handlers as one that arrived down the wire.
func (m *Mesh) deliverForwarded(carried transport.Forward, relay *Peer) {
	m.mu.RLock()
	peer := m.byIP[carried.Source]
	m.mu.RUnlock()
	if peer == nil || peer == relay {
		return
	}

	header, payload, err := transport.Decode(carried.Packet)
	if err != nil {
		return
	}
	m.dispatch(header, carried.Packet, payload, source{peer: peer, relay: relay})
}

// passOn carries a packet the rest of the way.
//
// Only a link this daemon holds directly is worth passing to. Handing one
// relayed packet to another relay would be a path nobody chose, and the hop
// limit is the backstop for the same idea: a packet that has run out of hops is
// dropped rather than passed round a ring forever.
func (m *Mesh) passOn(carried transport.Forward, from *Peer) {
	if carried.HopLimit == 0 {
		return
	}

	m.mu.RLock()
	target := m.byIP[carried.Destination]
	m.mu.RUnlock()
	if target == nil || target == from || target.State() != ipc.PeerDirect {
		return
	}

	carried.HopLimit--
	if err := m.forward(target, carried); err != nil {
		m.log.Debug("cannot carry a packet for a peer", "deviceId", target.DeviceID, "error", err)
	}
}

func (m *Mesh) handleStun(packet []byte) {
	if len(packet) < 20 {
		return
	}
	id := stun.TransactionID(packet[8:20])

	m.stunMu.Lock()
	waiter, ok := m.stunWaiters[id]
	m.stunMu.Unlock()
	if !ok {
		return
	}

	addr, err := stun.ParseResponse(id, packet)
	if err != nil {
		return
	}
	select {
	case waiter <- addr:
	default:
	}
}

// handleProbe answers a punch attempt. A probe is unauthenticated, so it
// changes nothing: it is echoed and nothing else.
func (m *Mesh) handleProbe(payload []byte, from netip.AddrPort) {
	ping, err := transport.DecodePing(payload)
	if err != nil {
		return
	}

	if ping.Reply {
		// The other side heard us, so a path exists in at least one direction.
		if peer := m.peerAt(from); peer != nil {
			peer.measure(ping.Token)
			m.beginHandshake(peer)
		}
		return
	}

	m.sendTo(from, envelope(transport.MsgProbe, 0, 0, transport.Ping{Token: ping.Token, Reply: true}.Encode(nil)))

	// Their probe reaching us means our own may be getting through, so this is
	// the moment to try the handshake rather than waiting for the next tick.
	if peer := m.peerAt(from); peer != nil {
		m.beginHandshake(peer)
	}
}

// handleHandshakeInit answers a peer that opened a handshake with us.
//
// The claimed device id only decides which key to check against. What proves
// identity is the handshake verifying with the key the coordination server gave
// us for that device.
func (m *Mesh) handleHandshakeInit(payload []byte, src source) {
	init, err := transport.DecodeHandshakeInit(payload)
	if err != nil {
		return
	}

	m.mu.RLock()
	peer, known := m.peers[init.DeviceID]
	m.mu.RUnlock()
	if !known {
		m.log.Info("handshake from a device not in this group", "deviceId", init.DeviceID, "from", src.addr.String())
		return
	}

	responder, err := session.NewResponder(m.keys)
	if err != nil {
		m.log.Error("cannot start a handshake", "error", err)
		return
	}
	if err := responder.ReadInit(init.KeyExchange); err != nil {
		m.log.Info("handshake did not verify", "deviceId", init.DeviceID, "error", err)
		return
	}

	// This is the check that matters. Anything can claim a device id; only the
	// holder of that device's key can produce a message that opens with it.
	if !equalKeys(responder.PeerStatic(), peer.transportKey) {
		m.log.Warn("handshake used the wrong key for that device", "deviceId", init.DeviceID, "from", src.addr.String())
		return
	}

	reply, open, err := responder.WriteResponse()
	if err != nil {
		m.log.Error("cannot answer a handshake", "error", err)
		return
	}

	if src.relay != nil {
		// They could not reach us and found somebody who could. Answering the
		// same way is the only route known to work.
		peer.answerVia(src.relay)
	} else {
		// A peer whose NAT mapping moved will arrive from somewhere new. It has
		// proved who it is, so the link follows it.
		if peer.Endpoint() != src.addr {
			peer.setEndpoint(src.addr)
			m.reindex()
		}
		m.forgetStranger(src.addr)
	}

	body, err := transport.HandshakeResponse{KeyExchange: reply}.Encode(nil)
	if err != nil {
		return
	}
	m.send(peer, transport.MsgHandshakeResponse, peer.sender, 0, body)

	peer.open(open)
	m.log.Info("link open", "deviceId", peer.DeviceID, "route", routeOf(peer), "role", "responder")
	m.report(peer)
	m.sendKeepalive(peer)
}

func (m *Mesh) handleHandshakeResponse(payload []byte, src source) {
	response, err := transport.DecodeHandshakeResponse(payload)
	if err != nil {
		return
	}

	peer := src.peer
	if peer == nil {
		return
	}

	peer.mu.Lock()
	handshake := peer.handshake
	peer.mu.Unlock()
	if handshake == nil {
		return
	}

	open, err := handshake.ReadResponse(response.KeyExchange)
	if err != nil {
		m.log.Info("handshake reply did not verify", "deviceId", peer.DeviceID, "error", err)
		return
	}

	if src.relay != nil {
		peer.answerVia(src.relay)
	}

	peer.open(open)
	m.log.Info("link open", "deviceId", peer.DeviceID, "route", routeOf(peer), "role", "initiator")
	m.report(peer)
	m.sendKeepalive(peer)
}

// handleKeepalive answers a liveness check, or records the round trip if this
// is the answer to ours.
func (m *Mesh) handleKeepalive(payload []byte, src source) {
	ping, err := transport.DecodePing(payload)
	if err != nil {
		return
	}
	peer := src.peer
	if peer == nil {
		m.noteStranger(src.addr)
		return
	}

	if !ping.Reply {
		peer.heard()
		// Answered the way it came rather than the way the link is pointed.
		// A keepalive carries no proof of who sent it, so letting one move a
		// link would let any member of the group put itself in the middle.
		m.replyTo(src, transport.MsgKeepalive, peer.sender, 0, transport.Ping{Token: ping.Token, Reply: true}.Encode(nil))
		return
	}

	if latency, ok := peer.measure(ping.Token); ok && m.events.PeerLatencyChanged != nil {
		m.events.PeerLatencyChanged(peer.DeviceID, latency)
	}
}

// handleData decrypts a game packet and hands it to the adapter.
func (m *Mesh) handleData(packet, ciphertext []byte, counter uint64, src source) {
	peer := src.peer
	if peer == nil {
		m.noteStranger(src.addr)
		return
	}

	plaintext, err := peer.accept(packet[:transport.HeaderSize], ciphertext, counter)
	if err != nil {
		return
	}
	peer.received.Add(1)
	if src.relay != nil {
		peer.answerVia(src.relay)
	}

	select {
	case m.inbound <- plaintext:
	default:
		// The adapter is not keeping up. Dropping is what a real network does
		// when a queue fills, and a game recovers from it.
		m.log.Debug("inbound queue full, dropping a packet", "deviceId", peer.DeviceID)
	}
}

func (m *Mesh) handleClose(src source) {
	peer := src.peer
	if peer == nil {
		return
	}
	if peer.fail() {
		m.log.Info("peer closed the link", "deviceId", peer.DeviceID)
		m.report(peer)
	}
}

// beginHandshake starts the key exchange, if this side is the one that should.
//
// Both peers punch, but only one opens the handshake. The lower device id does
// it, so two crossing handshakes never produce two sessions for one link.
func (m *Mesh) beginHandshake(peer *Peer) {
	if peer.DeviceID < m.device {
		return
	}

	peer.mu.Lock()
	reachable := peer.endpoint.IsValid() || peer.via != nil
	// An attempt already under way is left alone until it has had time to be
	// answered, because the reply only verifies against the state that sent it.
	// Left alone forever, though, a handshake nobody answers is a link that
	// never retries and never gives up, and a spinner that never stops.
	waiting := peer.handshake != nil && time.Since(peer.lastHandshake) < handshakeRetry
	busy := peer.session != nil || waiting || peer.handshakes >= handshakeBudget(peer.via)
	// A peer with no endpoint yet is waiting, not failing. Counting an attempt
	// here would give up on somebody nobody has tried to reach.
	if busy || !reachable {
		peer.mu.Unlock()
		return
	}
	peer.handshakes++
	peer.lastHandshake = time.Now()
	sender := peer.sender
	peer.mu.Unlock()

	initiator, err := session.NewInitiator(m.keys, peer.transportKey)
	if err != nil {
		m.log.Error("cannot start a handshake", "deviceId", peer.DeviceID, "error", err)
		return
	}
	message, err := initiator.WriteInit()
	if err != nil {
		m.log.Error("cannot write a handshake", "deviceId", peer.DeviceID, "error", err)
		return
	}

	body, err := transport.HandshakeInit{DeviceID: m.device, KeyExchange: message}.Encode(nil)
	if err != nil {
		return
	}

	peer.mu.Lock()
	peer.handshake = initiator
	peer.mu.Unlock()

	m.send(peer, transport.MsgHandshakeInit, sender, 0, body)
}

// handshakeBudget is how many attempts a link gets before it is given up on,
// which depends on what it is attempting.
func handshakeBudget(via *Peer) int {
	if via != nil {
		return maxRelayHandshakes
	}
	return maxHandshakes
}

// tryRelay asks the next peer that has not been asked yet to carry a link two
// NATs would not open on their own. It reports whether there was one.
//
// Any peer with an open direct link is a candidate: it can reach us, and
// whether it can also reach the other one is a question only trying answers.
// Lowest device id first, so the order is the same every time rather than
// whatever the map felt like.
func (m *Mesh) tryRelay(peer *Peer) bool {
	var relay *Peer
	for _, candidate := range m.Peers() {
		if candidate == peer || candidate.State() != ipc.PeerDirect || peer.triedRelay(candidate.DeviceID) {
			continue
		}
		if relay == nil || candidate.DeviceID < relay.DeviceID {
			relay = candidate
		}
	}
	if relay == nil {
		return false
	}

	m.log.Info("asking a peer to carry a link", "deviceId", peer.DeviceID, "via", relay.DeviceID)
	peer.relayThrough(relay)
	m.report(peer)
	m.beginHandshake(peer)
	return true
}

// punch fires a probe at a peer. Both sides doing this at once is what opens a
// path through two NATs.
func (m *Mesh) punch(peer *Peer) {
	endpoint := peer.Endpoint()
	if !endpoint.IsValid() {
		return
	}

	token := randomToken()
	peer.expect(token)

	peer.mu.Lock()
	peer.lastProbe = time.Now()
	peer.mu.Unlock()

	m.sendTo(endpoint, envelope(transport.MsgProbe, 0, 0, transport.Ping{Token: token}.Encode(nil)))
}

func (m *Mesh) sendKeepalive(peer *Peer) {
	token := randomToken()
	peer.expect(token)
	m.send(peer, transport.MsgKeepalive, peer.sender, 0, transport.Ping{Token: token}.Encode(nil))
}

// send puts a packet on a peer's link, whichever way that link runs.
func (m *Mesh) send(peer *Peer, kind transport.MessageType, sender, counter uint64, body []byte) {
	if err := m.deliver(peer, envelope(kind, sender, counter, body)); err != nil {
		m.log.Debug("send failed", "deviceId", peer.DeviceID, "error", err)
	}
}

// replyTo answers a packet the way it came, without committing the link to
// that route.
func (m *Mesh) replyTo(src source, kind transport.MessageType, sender, counter uint64, body []byte) {
	packet := envelope(kind, sender, counter, body)

	if src.relay == nil {
		m.sendTo(src.addr, packet)
		return
	}
	if err := m.forward(src.relay, transport.Forward{
		HopLimit:    initialHopLimit,
		Source:      m.virtualIP,
		Destination: src.peer.VirtualIP,
		Packet:      packet,
	}); err != nil {
		m.log.Debug("reply failed", "deviceId", src.peer.DeviceID, "error", err)
	}
}

// sendTo writes to an address rather than to a link, which is what a probe
// needs: it is asking whether there is a link there at all.
func (m *Mesh) sendTo(to netip.AddrPort, packet []byte) {
	if _, err := m.conn.WriteToUDPAddrPort(packet, to); err != nil {
		m.log.Debug("send failed", "to", to.String(), "error", err)
	}
}

func envelope(kind transport.MessageType, sender, counter uint64, body []byte) []byte {
	return transport.Header{
		Version: protocol.TransportVersion,
		Type:    kind,
		Sender:  sender,
		Counter: counter,
	}.Encode(nil, body)
}

// routeOf describes where a link's packets go, for the log.
func routeOf(peer *Peer) string {
	if relay := peer.Via(); relay != nil {
		return "via " + relay.DeviceID
	}
	return peer.Endpoint().String()
}

// maintain keeps links alive and retries the ones that are not open yet.
func (m *Mesh) maintain(ctx context.Context) {
	ticker := time.NewTicker(probeInterval)
	defer ticker.Stop()

	lastKeepalive := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			keepalives := now.Sub(lastKeepalive) >= keepaliveInterval
			if keepalives {
				lastKeepalive = now
			}

			for _, peer := range m.Peers() {
				peer.mu.Lock()
				state := peer.state
				open := peer.session != nil
				silent := open && !peer.lastHeard.IsZero() && now.Sub(peer.lastHeard) > deadAfter
				attempts := peer.handshakes
				budget := handshakeBudget(peer.via)
				relayed := peer.via != nil
				peer.mu.Unlock()

				switch {
				case silent:
					// Nothing authentic for long enough that the path is gone.
					// Starting over is cheaper than guessing why.
					m.log.Info("link went quiet, starting over", "deviceId", peer.DeviceID)
					peer.restart()
					m.report(peer)
					m.punch(peer)

				case open:
					if keepalives {
						m.sendKeepalive(peer)
					}

				case state == ipc.PeerFailed:
					// Nothing until the server sends a new endpoint, or until
					// somebody who was still connecting when this was given up
					// on turns out to be able to carry it.
					m.tryRelay(peer)

				case attempts >= budget:
					if m.tryRelay(peer) {
						break
					}
					if peer.fail() {
						m.log.Info("giving up on a peer", "deviceId", peer.DeviceID)
						m.report(peer)
					}

				case relayed:
					// A relay is a path that already works, so there is
					// nothing to punch through to it.
					m.beginHandshake(peer)

				default:
					m.punch(peer)
					m.beginHandshake(peer)
				}
			}
		}
	}
}

func (m *Mesh) peerAt(from netip.AddrPort) *Peer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byUDP[from]
}

func equalKeys(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
