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
		m.handleProbe(payload, from)
	case transport.MsgHandshakeInit:
		m.handleHandshakeInit(payload, from)
	case transport.MsgHandshakeResponse:
		m.handleHandshakeResponse(payload, from)
	case transport.MsgKeepalive:
		m.handleKeepalive(payload, from)
	case transport.MsgData:
		m.handleData(packet, payload, header.Counter, from)
	case transport.MsgClose:
		m.handleClose(from)
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

	m.sendTo(from, transport.MsgProbe, 0, 0, transport.Ping{Token: ping.Token, Reply: true}.Encode(nil))

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
func (m *Mesh) handleHandshakeInit(payload []byte, from netip.AddrPort) {
	init, err := transport.DecodeHandshakeInit(payload)
	if err != nil {
		return
	}

	m.mu.RLock()
	peer, known := m.peers[init.DeviceID]
	m.mu.RUnlock()
	if !known {
		m.log.Info("handshake from a device not in this group", "deviceId", init.DeviceID, "from", from.String())
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
		m.log.Warn("handshake used the wrong key for that device", "deviceId", init.DeviceID, "from", from.String())
		return
	}

	reply, open, err := responder.WriteResponse()
	if err != nil {
		m.log.Error("cannot answer a handshake", "error", err)
		return
	}

	// A peer whose NAT mapping moved will arrive from somewhere new. It has
	// proved who it is, so the link follows it.
	if peer.Endpoint() != from {
		peer.setEndpoint(from)
		m.reindex()
	}

	body, err := transport.HandshakeResponse{KeyExchange: reply}.Encode(nil)
	if err != nil {
		return
	}
	m.sendTo(from, transport.MsgHandshakeResponse, peer.sender, 0, body)

	peer.open(open)
	m.log.Info("link open", "deviceId", peer.DeviceID, "endpoint", from.String(), "role", "responder")
	m.report(peer)
	m.sendKeepalive(peer)
}

func (m *Mesh) handleHandshakeResponse(payload []byte, from netip.AddrPort) {
	response, err := transport.DecodeHandshakeResponse(payload)
	if err != nil {
		return
	}

	peer := m.peerAt(from)
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

	peer.open(open)
	m.log.Info("link open", "deviceId", peer.DeviceID, "endpoint", from.String(), "role", "initiator")
	m.report(peer)
	m.sendKeepalive(peer)
}

// handleKeepalive answers a liveness check, or records the round trip if this
// is the answer to ours.
func (m *Mesh) handleKeepalive(payload []byte, from netip.AddrPort) {
	ping, err := transport.DecodePing(payload)
	if err != nil {
		return
	}
	peer := m.peerAt(from)
	if peer == nil {
		return
	}

	if !ping.Reply {
		peer.heard()
		m.sendTo(from, transport.MsgKeepalive, peer.sender, 0, transport.Ping{Token: ping.Token, Reply: true}.Encode(nil))
		return
	}

	if latency, ok := peer.measure(ping.Token); ok && m.events.PeerLatencyChanged != nil {
		m.events.PeerLatencyChanged(peer.DeviceID, latency)
	}
}

// handleData decrypts a game packet and hands it to the adapter.
func (m *Mesh) handleData(packet, ciphertext []byte, counter uint64, from netip.AddrPort) {
	peer := m.peerAt(from)
	if peer == nil {
		return
	}

	plaintext, err := peer.accept(packet[:transport.HeaderSize], ciphertext, counter)
	if err != nil {
		return
	}

	select {
	case m.inbound <- plaintext:
	default:
		// The adapter is not keeping up. Dropping is what a real network does
		// when a queue fills, and a game recovers from it.
		m.log.Debug("inbound queue full, dropping a packet", "deviceId", peer.DeviceID)
	}
}

func (m *Mesh) handleClose(from netip.AddrPort) {
	peer := m.peerAt(from)
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
	if peer.session != nil || peer.handshake != nil || peer.handshakes >= maxHandshakes {
		peer.mu.Unlock()
		return
	}
	peer.handshakes++
	endpoint := peer.endpoint
	sender := peer.sender
	peer.mu.Unlock()

	if !endpoint.IsValid() {
		return
	}

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

	m.sendTo(endpoint, transport.MsgHandshakeInit, sender, 0, body)
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

	m.sendTo(endpoint, transport.MsgProbe, 0, 0, transport.Ping{Token: token}.Encode(nil))
}

func (m *Mesh) sendKeepalive(peer *Peer) {
	endpoint := peer.Endpoint()
	if !endpoint.IsValid() {
		return
	}

	token := randomToken()
	peer.expect(token)
	m.sendTo(endpoint, transport.MsgKeepalive, peer.sender, 0, transport.Ping{Token: token}.Encode(nil))
}

func (m *Mesh) sendTo(to netip.AddrPort, kind transport.MessageType, sender, counter uint64, body []byte) {
	packet := transport.Header{
		Version: protocol.TransportVersion,
		Type:    kind,
		Sender:  sender,
		Counter: counter,
	}.Encode(nil, body)

	if _, err := m.conn.WriteToUDPAddrPort(packet, to); err != nil {
		m.log.Debug("send failed", "to", to.String(), "error", err)
	}
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
				peer.mu.Unlock()

				switch {
				case silent:
					// Nothing authentic for long enough that the path is gone.
					// Starting over is cheaper than guessing why.
					m.log.Info("link went quiet, starting over", "deviceId", peer.DeviceID)
					peer.setEndpoint(peer.Endpoint())
					m.report(peer)
					m.punch(peer)

				case open:
					if keepalives {
						m.sendKeepalive(peer)
					}

				case state == ipc.PeerFailed:
					// Nothing until the server sends a new endpoint.

				case attempts >= maxHandshakes:
					if peer.fail() {
						m.log.Info("giving up on a peer", "deviceId", peer.DeviceID)
						m.report(peer)
					}

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
