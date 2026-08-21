// Package mesh is the peer-to-peer half of the daemon.
//
// One UDP socket carries everything: STUN queries that discover this machine's
// public address, hole-punch probes, handshakes, keepalives, and encrypted game
// packets. They share a port because a NAT mapping belongs to a port, and the
// address STUN reports is only true for the socket that asked.
//
// Every link is independent. One peer failing to punch through says nothing
// about the others.
package mesh

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/OMouta/192168/daemon/stun"
	"github.com/OMouta/192168/protocol"
	"github.com/OMouta/192168/protocol/ipc"
	"github.com/OMouta/192168/protocol/session"
	"github.com/OMouta/192168/protocol/transport"
)

const (
	// probeInterval is how often a punch attempt is repeated. Both sides have
	// to be sending at roughly the same time for a NAT to let the other in.
	probeInterval = 500 * time.Millisecond

	// keepaliveInterval is well inside the shortest NAT mapping lifetimes seen
	// in the wild, which are around 30 seconds for UDP.
	keepaliveInterval = 15 * time.Second

	// deadAfter is how long a link goes unheard before it is given up on.
	deadAfter = 45 * time.Second

	// handshakeRetry is how long an unanswered handshake is given before
	// another is sent. Long enough for a reply to cross a slow path and come
	// back, short enough that ten of them is a wait somebody will sit through.
	handshakeRetry = time.Second

	// maxHandshakes bounds how many times a link is retried before it is
	// reported as failed. The user is told rather than left watching a spinner.
	maxHandshakes = 10

	// maxRelayHandshakes is the same bound for an attempt through a relay,
	// where there is no NAT left to punch through. A relay that can carry the
	// link answers the first time, so this only has to survive a lost packet.
	maxRelayHandshakes = 3

	// initialHopLimit is how many peers a packet may pass through. One is what
	// this implements; the field on the wire holds more, so a longer path
	// would not be a change to the format.
	initialHopLimit = 1
)

// Events is how the mesh reports what changed. The core turns these into IPC
// events for the UI.
type Events struct {
	// PeerStateChanged fires when a link opens, fails, or starts over. The
	// reason is empty for a link with nothing in the way of it.
	PeerStateChanged func(deviceID string, state ipc.PeerState, reason ipc.PeerReason)
	// PeerLatencyChanged fires after a keepalive is answered.
	PeerLatencyChanged func(deviceID string, latency time.Duration)
}

// Mesh owns the socket and every peer link on it.
type Mesh struct {
	keys   session.Keypair
	device string
	log    *slog.Logger
	events Events

	// virtualIP is this device's address in the group, which is how a
	// forwarded packet says whether it has arrived or has further to go.
	virtualIP netip.Addr

	conn *net.UDPConn

	mu    sync.RWMutex
	peers map[string]*Peer         // by device id
	byIP  map[netip.Addr]*Peer     // by virtual ip, for outgoing packets
	byUDP map[netip.AddrPort]*Peer // by source address, for incoming packets

	// strangers are addresses packets arrived from that no peer is indexed
	// under. Each is logged once.
	//
	// Incoming packets are matched by source address, so a NAT that moves a
	// peer's mapping turns every packet from them into a stranger and the link
	// dies of silence with nothing said. Logging the address the first time it
	// appears says it. Logging every packet would not: this is the path game
	// traffic takes, so it would be thousands a second, and the log would roll
	// over and take the history worth reading with it.
	strangerMu sync.Mutex
	strangers  map[netip.AddrPort]struct{}

	// stunWaiters routes replies back to whoever asked, since they arrive on
	// the same socket as everything else.
	stunMu      sync.Mutex
	stunWaiters map[stun.TransactionID]chan netip.AddrPort

	// inbound carries decrypted IP packets up to the virtual adapter.
	inbound chan []byte
}

// New binds the socket every peer will use. Port zero lets the OS choose, and
// whatever it chooses is what STUN reports and what peers are told.
//
// virtualIP is this device's address in the group. It is only used to recognise
// a forwarded packet that has arrived, so a mesh without one still carries
// every direct link.
func New(deviceID string, virtualIP netip.Addr, keys session.Keypair, events Events, log *slog.Logger) (*Mesh, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, fmt.Errorf("mesh: bind: %w", err)
	}

	return &Mesh{
		keys:        keys,
		device:      deviceID,
		log:         log,
		events:      events,
		virtualIP:   virtualIP,
		conn:        conn,
		peers:       map[string]*Peer{},
		byIP:        map[netip.Addr]*Peer{},
		byUDP:       map[netip.AddrPort]*Peer{},
		strangers:   map[netip.AddrPort]struct{}{},
		stunWaiters: map[stun.TransactionID]chan netip.AddrPort{},
		inbound:     make(chan []byte, 256),
	}, nil
}

// LocalPort is the port peers will be told to send to.
func (m *Mesh) LocalPort() int {
	return m.conn.LocalAddr().(*net.UDPAddr).Port
}

// Inbound carries decrypted IP packets, ready to write to the adapter.
func (m *Mesh) Inbound() <-chan []byte { return m.inbound }

// noteStranger logs an address no peer is indexed under, the first time it is
// seen. Bounded, so a machine being sprayed with rubbish cannot grow this or
// fill the log with it.
func (m *Mesh) noteStranger(from netip.AddrPort) {
	const remember = 32

	m.strangerMu.Lock()
	_, seen := m.strangers[from]
	if !seen && len(m.strangers) < remember {
		m.strangers[from] = struct{}{}
	}
	m.strangerMu.Unlock()

	if !seen {
		m.log.Info("packet from an address no peer is at", "from", from.String())
	}
}

// forgetStranger drops an address once a peer is known to be there, so a NAT
// that moves again is reported again.
func (m *Mesh) forgetStranger(from netip.AddrPort) {
	m.strangerMu.Lock()
	delete(m.strangers, from)
	m.strangerMu.Unlock()
}

// Close drops the socket and every link on it.
func (m *Mesh) Close() error { return m.conn.Close() }

// Run reads the socket until ctx is cancelled, and keeps links alive while it
// does.
func (m *Mesh) Run(ctx context.Context) {
	go func() {
		<-ctx.Done()
		m.conn.Close()
	}()

	go m.maintain(ctx)

	buffer := make([]byte, transport.MaxDatagramSize)
	for {
		n, from, err := m.conn.ReadFromUDPAddrPort(buffer)
		if err != nil {
			if ctx.Err() == nil {
				m.log.Error("socket read failed", "error", err)
			}
			return
		}
		m.handle(buffer[:n], from)
	}
}

// PublicEndpoint asks each STUN server in turn until one answers.
//
// The answer is only true for this socket, which is why the query goes out on
// the socket peers use rather than a fresh one.
func (m *Mesh) PublicEndpoint(ctx context.Context, servers []string) (netip.AddrPort, error) {
	var lastErr error
	for _, server := range servers {
		addr, err := m.queryStun(ctx, server)
		if err == nil {
			m.log.Info("public endpoint", "endpoint", addr.String(), "via", server)
			return addr, nil
		}
		lastErr = err
		m.log.Info("stun server did not answer", "server", server, "error", err)
	}
	if lastErr == nil {
		lastErr = errors.New("no STUN servers configured")
	}
	return netip.AddrPort{}, fmt.Errorf("mesh: no public endpoint: %w", lastErr)
}

func (m *Mesh) queryStun(ctx context.Context, server string) (netip.AddrPort, error) {
	addr, err := resolveStun(server)
	if err != nil {
		return netip.AddrPort{}, err
	}

	req, err := stun.NewRequest()
	if err != nil {
		return netip.AddrPort{}, err
	}

	answer := make(chan netip.AddrPort, 1)
	m.stunMu.Lock()
	m.stunWaiters[req.ID] = answer
	m.stunMu.Unlock()
	defer func() {
		m.stunMu.Lock()
		delete(m.stunWaiters, req.ID)
		m.stunMu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	// UDP loses packets and a lost request looks exactly like a server that is
	// down, so ask more than once before believing it.
	for attempt := range 3 {
		if _, err := m.conn.WriteToUDPAddrPort(req.Packet, addr); err != nil {
			return netip.AddrPort{}, fmt.Errorf("mesh: send stun request: %w", err)
		}
		select {
		case got := <-answer:
			return got, nil
		case <-time.After(time.Duration(attempt+1) * 400 * time.Millisecond):
		case <-ctx.Done():
			return netip.AddrPort{}, ctx.Err()
		}
	}
	return netip.AddrPort{}, errors.New("no reply")
}

// AddPeer starts trying to reach a device. It returns once the attempt is
// under way, not once it succeeds.
func (m *Mesh) AddPeer(deviceID, nickname string, virtualIP netip.Addr, transportKey []byte, endpoint netip.AddrPort) error {
	if len(transportKey) != session.KeySize {
		return fmt.Errorf("mesh: peer %s has a %d byte key, want %d", deviceID, len(transportKey), session.KeySize)
	}

	sender, err := randomSender()
	if err != nil {
		return err
	}
	peer := newPeer(deviceID, nickname, virtualIP, transportKey, endpoint, sender)

	m.mu.Lock()
	if existing, ok := m.peers[deviceID]; ok {
		m.mu.Unlock()
		// An open link knows the address it is actually talking to, because the
		// handshake came from there. The address here is what the server was
		// told, which can be older and moving to it throws away a session that
		// works. A link that has genuinely moved arrives as an endpoint change.
		if existing.State() == ipc.PeerDirect {
			return nil
		}
		if endpoint.IsValid() && existing.setEndpoint(endpoint) {
			m.reindex()
			m.report(existing)
		}
		return nil
	}
	m.peers[deviceID] = peer
	m.mu.Unlock()

	m.reindex()
	m.log.Info("peer added", "deviceId", deviceID, "virtualIp", virtualIP.String(), "endpoint", endpoint.String())
	// Said now rather than at the first change, because somebody who joined
	// without an address yet has nothing to change until one arrives, and the
	// row would spend that time saying Connecting with no reason given.
	m.report(peer)
	m.punch(peer)
	return nil
}

// SetPeerEndpoint points a link at a new address, which is what happens when a
// peer's NAT mapping changes.
func (m *Mesh) SetPeerEndpoint(deviceID string, endpoint netip.AddrPort) {
	m.mu.RLock()
	peer, ok := m.peers[deviceID]
	m.mu.RUnlock()
	if !ok || !endpoint.IsValid() {
		return
	}

	if peer.setEndpoint(endpoint) {
		m.reindex()
		m.log.Info("peer endpoint changed", "deviceId", deviceID, "endpoint", endpoint.String())
		m.report(peer)
		m.punch(peer)
	}
}

// RestartLinks throws away every session and punches again, for when this
// machine's own address changed rather than a peer's.
//
// The mapping peers were sending to no longer exists, so their packets reach
// nobody and the keys on both sides are useless. Peers that had already been
// given up on are retried too: they were unreachable from an address this
// device no longer has, which says nothing about the new one.
func (m *Mesh) RestartLinks() {
	for _, peer := range m.Peers() {
		peer.restart()
		m.report(peer)
		m.punch(peer)
	}
}

// RetryPeer throws away one link and starts over, for a user who has done
// something about whatever was in the way.
//
// A link that has been given up on is not retried on its own: the punching
// stopped, the handshake budget is spent, and nothing changes that but a new
// endpoint from the server or a relay candidate turning up. This is the way to
// ask again without disconnecting from the whole group.
//
// It reports whether there was such a peer.
func (m *Mesh) RetryPeer(deviceID string) bool {
	m.mu.RLock()
	peer, ok := m.peers[deviceID]
	m.mu.RUnlock()
	if !ok {
		return false
	}

	m.log.Info("retrying a peer", "deviceId", deviceID)
	peer.restart()
	m.report(peer)
	m.punch(peer)
	m.beginHandshake(peer)
	return true
}

// RemovePeer forgets a device and everything about its link.
func (m *Mesh) RemovePeer(deviceID string) {
	m.mu.Lock()
	delete(m.peers, deviceID)
	m.mu.Unlock()
	m.reindex()
	m.log.Info("peer removed", "deviceId", deviceID)
}

// Peers is the current view of every link, for building UI state.
func (m *Mesh) Peers() []*Peer {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]*Peer, 0, len(m.peers))
	for _, peer := range m.peers {
		out = append(out, peer)
	}
	return out
}

// Send encrypts one IP packet and sends it to whoever owns the destination
// address.
func (m *Mesh) Send(destination netip.Addr, packet []byte) error {
	m.mu.RLock()
	peer, ok := m.byIP[destination]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("mesh: nobody has %s", destination)
	}
	return m.sendData(peer, packet)
}

// Broadcast sends one IP packet to every peer whose link is open, and reports
// how many got it.
//
// This is what a switch would have done with a broadcast frame. Nothing else
// can: every link here is its own encrypted tunnel to one device, so a packet
// addressed to the whole LAN has to be copied onto each of them by hand.
//
// A peer still handshaking is skipped rather than queued. Whatever sends
// discovery traffic repeats it on a timer, so the next one reaches them, and a
// queue would only deliver an announcement that had already gone stale.
func (m *Mesh) Broadcast(packet []byte) int {
	sent := 0
	for _, peer := range m.Peers() {
		err := m.sendData(peer, packet)
		switch {
		case err == nil:
			sent++
		case errors.Is(err, ErrNoSession), errors.Is(err, ErrNoPath):
			// Connecting, or given up on. Ordinary, and not worth a line per
			// packet on a path a game uses several times a second.
		default:
			m.log.Debug("cannot replicate a broadcast", "deviceId", peer.DeviceID, "error", err)
		}
	}
	return sent
}

// sendData puts one IP packet on one open link.
func (m *Mesh) sendData(peer *Peer, packet []byte) error {
	current, counter, err := peer.nextCounter()
	if err != nil {
		return err
	}

	header := transport.Header{
		Version: protocol.TransportVersion,
		Type:    transport.MsgData,
		Sender:  peer.sender,
		Counter: counter,
	}
	out := header.Encode(nil, nil)
	out, err = current.Seal(out, out[:transport.HeaderSize], packet, counter)
	if err != nil {
		return err
	}

	if err := m.deliver(peer, out); err != nil {
		return err
	}
	peer.sent.Add(1)
	return nil
}

// deliver puts one finished packet on a link, down the wire when the link has a
// path of its own and through a relay when it does not.
//
// Everything addressed to a peer goes through here, which is what makes a
// relayed link behave like any other: the handshake, the keepalives, the
// latency and the game traffic all take the same road without knowing which
// road it is.
func (m *Mesh) deliver(peer *Peer, packet []byte) error {
	if relay := peer.Via(); relay != nil {
		return m.forward(relay, transport.Forward{
			HopLimit:    initialHopLimit,
			Source:      m.virtualIP,
			Destination: peer.VirtualIP,
			Packet:      packet,
		})
	}

	endpoint := peer.Endpoint()
	if !endpoint.IsValid() {
		return ErrNoPath
	}
	_, err := m.conn.WriteToUDPAddrPort(packet, endpoint)
	return err
}

// forward hands a packet to the peer that will carry it. The forward header is
// sealed with the session shared with that peer, so it can be trusted to have
// come from us and nobody can turn this daemon into a reflector.
func (m *Mesh) forward(relay *Peer, carried transport.Forward) error {
	body, err := carried.Encode(nil)
	if err != nil {
		return err
	}

	current, counter, err := relay.nextCounter()
	if err != nil {
		return err
	}

	out := transport.Header{
		Version: protocol.TransportVersion,
		Type:    transport.MsgForward,
		Sender:  relay.sender,
		Counter: counter,
	}.Encode(nil, nil)
	out, err = current.Seal(out, out[:transport.HeaderSize], body, counter)
	if err != nil {
		return err
	}

	endpoint := relay.Endpoint()
	if !endpoint.IsValid() {
		return ErrNoPath
	}
	_, err = m.conn.WriteToUDPAddrPort(out, endpoint)
	return err
}

// reindex rebuilds the lookup tables. Peers change rarely and a group is under
// ten, so rebuilding beats keeping several maps in step by hand.
func (m *Mesh) reindex() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.byIP = make(map[netip.Addr]*Peer, len(m.peers))
	m.byUDP = make(map[netip.AddrPort]*Peer, len(m.peers))
	for _, peer := range m.peers {
		if peer.VirtualIP.IsValid() {
			m.byIP[peer.VirtualIP] = peer
		}
		if endpoint := peer.Endpoint(); endpoint.IsValid() {
			m.byUDP[endpoint] = peer
		}
	}
}

func (m *Mesh) report(peer *Peer) {
	if m.events.PeerStateChanged != nil {
		state, reason := peer.status()
		m.events.PeerStateChanged(peer.DeviceID, state, reason)
	}
}

func randomSender() (uint64, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, fmt.Errorf("mesh: sender id: %w", err)
	}
	return binary.BigEndian.Uint64(raw[:]), nil
}

func randomToken() uint64 {
	token, err := randomSender()
	if err != nil {
		return uint64(time.Now().UnixNano())
	}
	return token
}

// resolveStun turns "stun:host:port" into an address.
func resolveStun(server string) (netip.AddrPort, error) {
	host := server
	if after, ok := cutPrefix(host, "stun:"); ok {
		host = after
	}
	if after, ok := cutPrefix(host, "stuns:"); ok {
		host = after
	}

	name, port, err := net.SplitHostPort(host)
	if err != nil {
		name, port = host, "3478"
	}

	addrs, err := net.LookupHost(name)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("mesh: resolve %s: %w", name, err)
	}
	for _, candidate := range addrs {
		addr, err := netip.ParseAddr(candidate)
		if err != nil || !addr.Is4() {
			continue
		}
		p, err := net.LookupPort("udp", port)
		if err != nil {
			return netip.AddrPort{}, fmt.Errorf("mesh: bad stun port %q: %w", port, err)
		}
		return netip.AddrPortFrom(addr, uint16(p)), nil
	}
	return netip.AddrPort{}, fmt.Errorf("mesh: %s has no IPv4 address", name)
}

func cutPrefix(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return s, false
}
