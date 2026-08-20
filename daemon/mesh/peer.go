package mesh

import (
	"errors"
	"net/netip"
	"sync"
	"time"

	"github.com/OMouta/192168/protocol/ipc"
	"github.com/OMouta/192168/protocol/session"
)

// Peer is one link to one other device.
//
// Links are independent. A peer that cannot be reached says nothing about the
// rest of the group, so everything here is per peer and nothing is shared but
// the socket.
type Peer struct {
	DeviceID  string
	Nickname  string
	VirtualIP netip.Addr

	// transportKey is the static key from the coordination server. A handshake
	// that does not carry this key is not this peer, whatever address it came
	// from.
	transportKey []byte

	mu sync.Mutex

	endpoint netip.AddrPort
	state    ipc.PeerState
	latency  time.Duration

	// sender is the id this side puts in its packet headers, and remoteSender
	// is what the peer puts in its own. They are unrelated random numbers.
	sender uint64

	handshake *session.Handshake
	session   *session.Session
	replay    session.ReplayWindow
	counter   uint64

	// probes and keepalives waiting for an echo, by token.
	pending map[uint64]time.Time

	lastHeard  time.Time
	lastProbe  time.Time
	handshakes int
}

// ErrNoSession means the link is not open yet, so there is nowhere to send.
var ErrNoSession = errors.New("mesh: no session with that peer")

func newPeer(deviceID, nickname string, virtualIP netip.Addr, transportKey []byte, endpoint netip.AddrPort, sender uint64) *Peer {
	return &Peer{
		DeviceID:     deviceID,
		Nickname:     nickname,
		VirtualIP:    virtualIP,
		transportKey: transportKey,
		endpoint:     endpoint,
		state:        ipc.PeerConnecting,
		sender:       sender,
		pending:      map[uint64]time.Time{},
	}
}

// State is what the UI is told about this link.
func (p *Peer) State() ipc.PeerState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// Latency is the last measured round trip, zero until one has been measured.
func (p *Peer) Latency() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.latency
}

// Endpoint is where packets are being sent, empty until the server says.
func (p *Peer) Endpoint() netip.AddrPort {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.endpoint
}

// setEndpoint points the link at a new address and starts over.
//
// A NAT mapping that changed means the old session's peer cannot answer at the
// new address, so the handshake runs again rather than trying to reuse keys the
// other side may have thrown away.
func (p *Peer) setEndpoint(endpoint netip.AddrPort) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.endpoint == endpoint {
		return false
	}
	p.endpoint = endpoint
	p.session = nil
	p.handshake = nil
	p.replay = session.ReplayWindow{}
	p.counter = 0
	p.state = ipc.PeerConnecting
	p.lastProbe = time.Time{}
	return true
}

// open records a finished handshake.
func (p *Peer) open(s *session.Session) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.session = s
	p.handshake = nil
	p.replay = session.ReplayWindow{}
	p.counter = 0
	p.state = ipc.PeerDirect
	p.lastHeard = time.Now()
}

// nextCounter hands out the number that becomes the AEAD nonce for one packet.
// Every packet on a session needs a different one.
func (p *Peer) nextCounter() (*session.Session, uint64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.session == nil {
		return nil, 0, ErrNoSession
	}
	counter := p.counter
	p.counter++
	return p.session, counter, nil
}

// accept decrypts a packet and reports whether it is new. A replay is dropped
// without disturbing the link.
func (p *Peer) accept(header, ciphertext []byte, counter uint64) ([]byte, error) {
	p.mu.Lock()
	current := p.session
	p.mu.Unlock()

	if current == nil {
		return nil, ErrNoSession
	}

	// Decrypting before checking the replay window is deliberate. The counter
	// on an unauthenticated packet is attacker controlled, and feeding it to
	// the window would let anyone slide it forward and lock out real traffic.
	plaintext, err := current.Open(nil, header, ciphertext, counter)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.replay.Accept(counter) {
		return nil, errors.New("mesh: replayed packet")
	}
	p.lastHeard = time.Now()
	return plaintext, nil
}

// heard notes that something authentic arrived, which is what keeps a link from
// being declared dead.
func (p *Peer) heard() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastHeard = time.Now()
}

// expect records a token this side is waiting to have echoed back.
func (p *Peer) expect(token uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// A link that never answers would otherwise collect tokens forever.
	if len(p.pending) > 32 {
		clear(p.pending)
	}
	p.pending[token] = time.Now()
}

// measure turns an echoed token into a round trip time. It reports false for a
// token this side never sent, so a peer cannot invent a latency.
func (p *Peer) measure(token uint64) (time.Duration, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	sentAt, ok := p.pending[token]
	if !ok {
		return 0, false
	}
	delete(p.pending, token)

	p.latency = time.Since(sentAt)
	p.lastHeard = time.Now()
	return p.latency, true
}

// fail marks the link unreachable so the UI stops saying it is connecting.
func (p *Peer) fail() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.state == ipc.PeerFailed {
		return false
	}
	p.state = ipc.PeerFailed
	p.session = nil
	p.handshake = nil
	return true
}
