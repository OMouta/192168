package mesh

import (
	"errors"
	"net/netip"
	"sync"
	"sync/atomic"
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

	// via is the peer carrying this link's packets, nil while it is direct.
	// The keys are still this link's own, so a relay moves bytes it cannot
	// read.
	via *Peer

	// relaysTried names the peers already asked to carry this link, so a group
	// of six does not spend forever asking the same one.
	relaysTried map[string]bool

	// sender is the id this side puts in its packet headers, and remoteSender
	// is what the peer puts in its own. They are unrelated random numbers.
	sender uint64

	handshake *session.Handshake
	session   *session.Session
	replay    session.ReplayWindow
	counter   uint64

	// probes and keepalives waiting for an echo, by token.
	pending map[uint64]time.Time

	lastHeard     time.Time
	lastProbe     time.Time
	lastHandshake time.Time
	handshakes    int

	// sent and received count data packets each way. Atomic because every game
	// packet takes this path and the numbers are read once a second.
	sent     atomic.Uint64
	received atomic.Uint64
}

// ErrNoSession means the link is not open yet, so there is nowhere to send.
var ErrNoSession = errors.New("mesh: no session with that peer")

// ErrNoPath means the peer has neither an address of its own nor anybody
// carrying for it, so the packet has nowhere to go.
var ErrNoPath = errors.New("mesh: no path to that peer")

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
		relaysTried:  map[string]bool{},
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

// status is the link's state together with what is in the way of it, which is
// the part somebody can occasionally do something about.
//
// A link with no address to aim at is waiting rather than failing, and the two
// look identical from a row that only says Connecting. That distinction is the
// whole point of this.
func (p *Peer) status() (ipc.PeerState, ipc.PeerReason) {
	p.mu.Lock()
	defer p.mu.Unlock()

	switch {
	case p.state == ipc.PeerFailed:
		return p.state, ipc.ReasonNoRoute
	case p.state == ipc.PeerConnecting && !p.endpoint.IsValid() && p.via == nil:
		return p.state, ipc.ReasonNoAddress
	}
	return p.state, ""
}

// Traffic is how many data packets this link has carried each way. Handshakes
// and keepalives are the app talking to itself, so they are left out.
func (p *Peer) Traffic() (sent, received uint64) {
	return p.sent.Load(), p.received.Load()
}

// Endpoint is where packets are being sent, empty until the server says.
func (p *Peer) Endpoint() netip.AddrPort {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.endpoint
}

// setEndpoint points the link at a new address and starts over.
//
// A NAT mapping that changed means the peer cannot answer at the old address,
// so the handshake runs again rather than reusing keys the other side may have
// thrown away.
func (p *Peer) setEndpoint(endpoint netip.AddrPort) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.endpoint == endpoint {
		return false
	}
	p.endpoint = endpoint
	p.resetLocked()
	p.goDirectLocked()
	return true
}

// restart throws away a link and tries again at the same address, for when it
// has gone quiet rather than moved.
func (p *Peer) restart() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resetLocked()
	p.goDirectLocked()
}

// Via is the peer carrying this link, nil while it is direct.
func (p *Peer) Via() *Peer {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.via
}

// relayThrough asks a peer to carry this link and starts the handshake over, so
// the keys are agreed along the path that will carry them.
func (p *Peer) relayThrough(relay *Peer) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.resetLocked()
	p.via = relay
	p.relaysTried[relay.DeviceID] = true
}

// answerVia routes this link back the way a packet from it arrived.
//
// It is only called for packets that have proved they are from this peer, so a
// group member cannot put itself in the middle of somebody else's link by
// claiming to have carried one.
func (p *Peer) answerVia(relay *Peer) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.via == relay {
		return
	}
	p.via = relay
	p.relaysTried[relay.DeviceID] = true
	if p.session != nil {
		p.state = ipc.PeerIndirect
	}
}

// triedRelay reports whether a peer has already been asked to carry this link.
func (p *Peer) triedRelay(deviceID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.relaysTried[deviceID]
}

// goDirectLocked forgets the relays, for when something changed that makes a
// direct path worth trying again from scratch.
func (p *Peer) goDirectLocked() {
	p.via = nil
	clear(p.relaysTried)
}

func (p *Peer) resetLocked() {
	p.session = nil
	p.handshake = nil
	p.replay = session.ReplayWindow{}
	p.counter = 0
	p.state = ipc.PeerConnecting
	p.lastProbe = time.Time{}
	p.lastHeard = time.Time{}
	p.lastHandshake = time.Time{}
	p.handshakes = 0
	clear(p.pending)
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
	if p.via != nil {
		p.state = ipc.PeerIndirect
	}
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
