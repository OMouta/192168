package mesh

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/OMouta/192168/protocol/ipc"
	"github.com/OMouta/192168/protocol/session"
)

// node is one daemon's worth of mesh, running.
type node struct {
	deviceID  string
	virtualIP netip.Addr
	keys      session.Keypair
	mesh      *Mesh
	states    *stateLog
}

// stateLog records what the mesh reported, so a test waits for a state instead
// of sleeping and hoping.
type stateLog struct {
	mu      sync.Mutex
	states  map[string]ipc.PeerState
	reasons map[string]ipc.PeerReason
	latency map[string]time.Duration
}

func newStateLog() *stateLog {
	return &stateLog{
		states:  map[string]ipc.PeerState{},
		reasons: map[string]ipc.PeerReason{},
		latency: map[string]time.Duration{},
	}
}

func (s *stateLog) set(deviceID string, state ipc.PeerState, reason ipc.PeerReason) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[deviceID] = state
	s.reasons[deviceID] = reason
}

func (s *stateLog) reasonOf(deviceID string) ipc.PeerReason {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reasons[deviceID]
}

func (s *stateLog) setLatency(deviceID string, latency time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latency[deviceID] = latency
}

func (s *stateLog) get(deviceID string) ipc.PeerState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.states[deviceID]
}

func (s *stateLog) latencyOf(deviceID string) (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, ok := s.latency[deviceID]
	return got, ok
}

func newNode(t *testing.T, deviceID, virtualIP string) *node {
	t.Helper()

	keys, err := session.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	states := newStateLog()
	address := netip.MustParseAddr(virtualIP)
	m, err := New(deviceID, address, keys, Events{
		PeerStateChanged:   states.set,
		PeerLatencyChanged: states.setLatency,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { m.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go m.Run(ctx)

	return &node{deviceID: deviceID, virtualIP: address, keys: keys, mesh: m, states: states}
}

func (n *node) endpoint() netip.AddrPort {
	return netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(n.mesh.LocalPort()))
}

// introduce tells each node about the other, which is what the coordination
// server does in the real thing.
func introduce(t *testing.T, a, b *node) {
	t.Helper()
	if err := a.mesh.AddPeer(b.deviceID, b.deviceID, b.virtualIP, b.keys.Public, b.endpoint()); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}
	if err := b.mesh.AddPeer(a.deviceID, a.deviceID, a.virtualIP, a.keys.Public, a.endpoint()); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}
}

func waitForState(t *testing.T, n *node, deviceID string, want ipc.PeerState) {
	t.Helper()
	waitForStateWithin(t, n, deviceID, want, 10*time.Second)
}

func waitForStateWithin(t *testing.T, n *node, deviceID string, want ipc.PeerState, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if n.states.get(deviceID) == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never saw %s reach %s, last was %s", n.deviceID, deviceID, want, n.states.get(deviceID))
}

// The whole point of the package: two daemons that have only been told about
// each other end up with an open encrypted link.
func TestTwoPeersOpenALink(t *testing.T) {
	// The lower device id opens the handshake, so the names decide who does.
	a := newNode(t, "dev_aaa", "10.69.0.1")
	b := newNode(t, "dev_bbb", "10.69.0.2")
	introduce(t, a, b)

	waitForState(t, a, b.deviceID, ipc.PeerDirect)
	waitForState(t, b, a.deviceID, ipc.PeerDirect)
}

func TestPeersExchangePackets(t *testing.T) {
	a := newNode(t, "dev_aaa", "10.69.0.1")
	b := newNode(t, "dev_bbb", "10.69.0.2")
	introduce(t, a, b)

	waitForState(t, a, b.deviceID, ipc.PeerDirect)
	waitForState(t, b, a.deviceID, ipc.PeerDirect)

	// This stands in for an IP packet off the virtual adapter.
	payload := []byte("a game packet, more or less")
	if err := a.mesh.Send(netip.MustParseAddr("10.69.0.2"), payload); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case got := <-b.mesh.Inbound():
		if !bytes.Equal(got, payload) {
			t.Errorf("received %q, want %q", got, payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the packet never arrived")
	}

	// And back the other way, which is a different key.
	reply := []byte("and the reply")
	if err := b.mesh.Send(netip.MustParseAddr("10.69.0.1"), reply); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case got := <-a.mesh.Inbound():
		if !bytes.Equal(got, reply) {
			t.Errorf("received %q, want %q", got, reply)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the reply never arrived")
	}
}

// A link opens with a handshake and a keepalive on it. Counting those would
// have every row claiming traffic before anybody had played anything.
func TestTrafficCountsGamePacketsOnly(t *testing.T) {
	a := newNode(t, "dev_aaa", "10.69.0.1")
	b := newNode(t, "dev_bbb", "10.69.0.2")
	introduce(t, a, b)

	waitForState(t, a, b.deviceID, ipc.PeerDirect)
	waitForState(t, b, a.deviceID, ipc.PeerDirect)

	if sent, received := link(t, a, b.deviceID).Traffic(); sent != 0 || received != 0 {
		t.Fatalf("an open link with no game on it counted %d sent and %d received", sent, received)
	}

	if err := a.mesh.Send(netip.MustParseAddr("10.69.0.2"), []byte("a game packet")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case <-b.mesh.Inbound():
	case <-time.After(5 * time.Second):
		t.Fatal("the packet never arrived")
	}

	if sent, received := link(t, a, b.deviceID).Traffic(); sent != 1 || received != 0 {
		t.Errorf("the sender counted %d sent and %d received, want 1 and 0", sent, received)
	}
	if sent, received := link(t, b, a.deviceID).Traffic(); sent != 0 || received != 1 {
		t.Errorf("the receiver counted %d sent and %d received, want 0 and 1", sent, received)
	}
}

// link finds one node's view of another.
func link(t *testing.T, n *node, deviceID string) *Peer {
	t.Helper()

	for _, peer := range n.mesh.Peers() {
		if peer.DeviceID == deviceID {
			return peer
		}
	}
	t.Fatalf("%s has no link to %s", n.deviceID, deviceID)
	return nil
}

// Being given up on is a dead end otherwise. Nothing retries a failed link on
// its own, so this is the only way back that does not take the whole group down
// with it.
func TestRetryingAPeerOpensItAgain(t *testing.T) {
	a := newNode(t, "dev_aaa", "10.69.0.1")
	b := newNode(t, "dev_bbb", "10.69.0.2")
	introduce(t, a, b)

	waitForState(t, a, b.deviceID, ipc.PeerDirect)
	waitForState(t, b, a.deviceID, ipc.PeerDirect)

	// What giving up leaves behind: no session, and nothing that will try again.
	peer := link(t, a, b.deviceID)
	peer.fail()
	if peer.State() != ipc.PeerFailed {
		t.Fatalf("state = %s, want failed", peer.State())
	}

	if !a.mesh.RetryPeer(b.deviceID) {
		t.Fatal("RetryPeer found no such peer")
	}
	// Retrying reports connecting on its way past, so the wait below is for the
	// link opening again rather than for the state it was already in.
	waitForState(t, a, b.deviceID, ipc.PeerDirect)
}

// A row that says Connecting forever and a row that is about to open look the
// same. The reason is what tells them apart, and waiting on somebody's address
// is the case a person can act on: it is their app that has not published one.
func TestAPeerWithNoAddressSaysSo(t *testing.T) {
	a := newNode(t, "dev_aaa", "10.69.0.1")
	b := newNode(t, "dev_bbb", "10.69.0.2")

	// Online and in the group, but nobody has told a where b is. This is what a
	// peer looks like between connecting and their first STUN round landing.
	// b is given a's address so that the link has only the one thing missing.
	if err := b.mesh.AddPeer(a.deviceID, a.deviceID, a.virtualIP, a.keys.Public, a.endpoint()); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}
	if err := a.mesh.AddPeer(b.deviceID, b.deviceID, b.virtualIP, b.keys.Public, netip.AddrPort{}); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}
	if got := a.states.reasonOf(b.deviceID); got != ipc.ReasonNoAddress {
		t.Errorf("reason = %q, want %q", got, ipc.ReasonNoAddress)
	}

	// The address arriving is the end of it, and the row should stop saying so.
	a.mesh.SetPeerEndpoint(b.deviceID, b.endpoint())
	waitForState(t, a, b.deviceID, ipc.PeerDirect)
	if got := a.states.reasonOf(b.deviceID); got != "" {
		t.Errorf("an open link still gives the reason %q", got)
	}
}

func TestAGivenUpLinkSaysThereWasNoRoute(t *testing.T) {
	a := newNode(t, "dev_aaa", "10.69.0.1")
	b := newNode(t, "dev_bbb", "10.69.0.2")
	introduce(t, a, b)

	waitForState(t, a, b.deviceID, ipc.PeerDirect)

	link(t, a, b.deviceID).fail()
	a.mesh.report(link(t, a, b.deviceID))

	if got := a.states.reasonOf(b.deviceID); got != ipc.ReasonNoRoute {
		t.Errorf("reason = %q, want %q", got, ipc.ReasonNoRoute)
	}
}

func TestRetryingSomebodyWhoIsNotHere(t *testing.T) {
	a := newNode(t, "dev_aaa", "10.69.0.1")
	if a.mesh.RetryPeer("dev_nobody") {
		t.Error("RetryPeer claimed to have retried a peer it does not have")
	}
}

func TestLatencyIsMeasured(t *testing.T) {
	a := newNode(t, "dev_aaa", "10.69.0.1")
	b := newNode(t, "dev_bbb", "10.69.0.2")
	introduce(t, a, b)

	waitForState(t, a, b.deviceID, ipc.PeerDirect)

	// A keepalive goes out as soon as a link opens, so a number should appear
	// without waiting for the interval.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := a.states.latencyOf(b.deviceID); ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no latency was ever measured")
}

func TestSendingToNobodyFails(t *testing.T) {
	a := newNode(t, "dev_aaa", "10.69.0.1")

	if err := a.mesh.Send(netip.MustParseAddr("10.69.0.9"), []byte("hello")); err == nil {
		t.Error("sending to an address nobody has succeeded")
	}
}

func TestSendingBeforeTheLinkOpensFails(t *testing.T) {
	a := newNode(t, "dev_aaa", "10.69.0.1")
	b := newNode(t, "dev_bbb", "10.69.0.2")

	// Known peer, no handshake yet, because nothing is listening back.
	unreachable := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), 1)
	if err := a.mesh.AddPeer(b.deviceID, "b", netip.MustParseAddr("10.69.0.2"), b.keys.Public, unreachable); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	if err := a.mesh.Send(netip.MustParseAddr("10.69.0.2"), []byte("hello")); err == nil {
		t.Error("a packet was sent over a link that is not open")
	}
}

func TestAPeerWithTheWrongKeyNeverOpens(t *testing.T) {
	a := newNode(t, "dev_aaa", "10.69.0.1")
	b := newNode(t, "dev_bbb", "10.69.0.2")

	// The coordination server is what says which key belongs to which device.
	// A device answering at the right address with the wrong key is exactly
	// what this check exists to stop.
	stranger, err := session.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	if err := a.mesh.AddPeer(b.deviceID, "b", netip.MustParseAddr("10.69.0.2"), stranger.Public, b.endpoint()); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}
	if err := b.mesh.AddPeer(a.deviceID, "a", netip.MustParseAddr("10.69.0.1"), a.keys.Public, a.endpoint()); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	// Long enough for several punches and handshake attempts.
	time.Sleep(3 * time.Second)

	if got := a.states.get(b.deviceID); got == ipc.PeerDirect {
		t.Error("a link opened against the wrong key")
	}
	if got := b.states.get(a.deviceID); got == ipc.PeerDirect {
		t.Error("the responder opened a link with a key it was not given")
	}
}

func TestRemovingAPeerStopsRouting(t *testing.T) {
	a := newNode(t, "dev_aaa", "10.69.0.1")
	b := newNode(t, "dev_bbb", "10.69.0.2")
	introduce(t, a, b)
	waitForState(t, a, b.deviceID, ipc.PeerDirect)

	a.mesh.RemovePeer(b.deviceID)
	if err := a.mesh.Send(netip.MustParseAddr("10.69.0.2"), []byte("hello")); err == nil {
		t.Error("a removed peer still had a route")
	}
}

// A broadcast goes to everyone whose link is open, and nobody else. The peer
// that is still connecting has no session to encrypt with, and skipping it has
// to be quiet rather than an error, because a game sends discovery traffic
// continuously.
func TestBroadcastReachesEveryOpenLink(t *testing.T) {
	a := newNode(t, "dev_aaa", "10.69.0.1")
	b := newNode(t, "dev_bbb", "10.69.0.2")
	introduce(t, a, b)
	waitForState(t, a, b.deviceID, ipc.PeerDirect)
	waitForState(t, b, a.deviceID, ipc.PeerDirect)

	// A third device that will never answer, so its link stays unopened.
	nowhere := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), 1)
	var absent [32]byte
	if err := a.mesh.AddPeer("dev_ccc", "c", netip.MustParseAddr("10.69.0.3"), absent[:], nowhere); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	announcement := []byte("[MOTD]a world[/MOTD][AD]25565[/AD]")
	if sent := a.mesh.Broadcast(announcement); sent != 1 {
		t.Errorf("broadcast reached %d peers, want 1", sent)
	}

	select {
	case got := <-b.mesh.Inbound():
		if !bytes.Equal(got, announcement) {
			t.Errorf("received %q, want %q", got, announcement)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the broadcast never arrived")
	}
}

// What happens after a laptop changes network. The old session is useless to
// both sides, so the link has to be rebuilt rather than carried over.
func TestRestartLinksRebuildsAnOpenSession(t *testing.T) {
	a := newNode(t, "dev_aaa", "10.69.0.1")
	b := newNode(t, "dev_bbb", "10.69.0.2")
	introduce(t, a, b)

	waitForState(t, a, b.deviceID, ipc.PeerDirect)
	waitForState(t, b, a.deviceID, ipc.PeerDirect)

	a.mesh.RestartLinks()

	// Sending on keys that were just thrown away has to fail rather than
	// produce a packet the other side would drop as a replay.
	if err := a.mesh.Send(netip.MustParseAddr("10.69.0.2"), []byte("too early")); err == nil {
		t.Error("Send succeeded on a link that was just restarted")
	}

	// And the link comes back on its own, because the peers still know each
	// other and the punch runs again.
	waitForState(t, a, b.deviceID, ipc.PeerDirect)

	payload := []byte("after the move")
	if err := a.mesh.Send(netip.MustParseAddr("10.69.0.2"), payload); err != nil {
		t.Fatalf("Send after restart: %v", err)
	}
	select {
	case got := <-b.mesh.Inbound():
		if !bytes.Equal(got, payload) {
			t.Errorf("received %q, want %q", got, payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing arrived after the link was rebuilt")
	}
}

// A peer already given up on is retried, because it was unreachable from an
// address this device no longer has.
func TestRestartLinksRetriesAFailedPeer(t *testing.T) {
	a := newNode(t, "dev_aaa", "10.69.0.1")

	// Nothing is listening at this address, so the link runs out of attempts.
	dead := netip.MustParseAddr("10.69.0.9")
	var key [32]byte
	if err := a.mesh.AddPeer("dev_zzz", "z", dead, key[:], netip.MustParseAddrPort("127.0.0.1:9")); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}
	waitForState(t, a, "dev_zzz", ipc.PeerFailed)

	a.mesh.RestartLinks()

	if got := a.states.get("dev_zzz"); got != ipc.PeerConnecting {
		t.Errorf("a failed peer stayed %s after a restart, want %s", got, ipc.PeerConnecting)
	}
}

// The point of peer-assisted routing: two people whose NATs will not open to
// each other still end up on the same network, because somebody they can both
// reach carries the link.
//
// A and C are pointed at addresses nothing answers on, which is what a NAT
// neither side can punch looks like from here. Both reach B, so once A has
// spent its direct attempts it asks B to carry the rest.
func TestALinkOpensThroughAnotherPeer(t *testing.T) {
	// The lower device id opens the handshake, so A is the one that runs out
	// of direct attempts and goes looking for a way round.
	a := newNode(t, "dev_aaa", "10.69.0.1")
	b := newNode(t, "dev_bbb", "10.69.0.2")
	c := newNode(t, "dev_ccc", "10.69.0.3")

	introduce(t, a, b)
	introduce(t, b, c)

	// Documentation addresses, so the probes leave and nothing answers. A
	// closed port on this machine would answer with an ICMP refusal, which is
	// friendlier than the NAT this is standing in for.
	nowhere := netip.MustParseAddrPort("192.0.2.1:9")
	if err := a.mesh.AddPeer(c.deviceID, c.deviceID, c.virtualIP, c.keys.Public, nowhere); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}
	if err := c.mesh.AddPeer(a.deviceID, a.deviceID, a.virtualIP, a.keys.Public, nowhere); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	// Long enough for the direct attempts to be spent first, since going
	// through somebody else is the fallback and not the first choice.
	waitForStateWithin(t, a, c.deviceID, ipc.PeerIndirect, 30*time.Second)
	waitForStateWithin(t, c, a.deviceID, ipc.PeerIndirect, 30*time.Second)

	// And the link carries a packet, which is the only thing a user cares
	// about. B moves it without ever holding the keys that opened it.
	payload := []byte("through a friend")
	if err := a.mesh.Send(c.virtualIP, payload); err != nil {
		t.Fatalf("Send over a relayed link: %v", err)
	}
	select {
	case got := <-c.mesh.Inbound():
		if !bytes.Equal(got, payload) {
			t.Errorf("received %q, want %q", got, payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing arrived over the relayed link")
	}

	// The link that carried it is still the ordinary direct one it was.
	if got := a.states.get(b.deviceID); got != ipc.PeerDirect {
		t.Errorf("the relay's own link is %s, want %s", got, ipc.PeerDirect)
	}
}
