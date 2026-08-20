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
	deviceID string
	keys     session.Keypair
	mesh     *Mesh
	states   *stateLog
}

// stateLog records what the mesh reported, so a test waits for a state instead
// of sleeping and hoping.
type stateLog struct {
	mu      sync.Mutex
	states  map[string]ipc.PeerState
	latency map[string]time.Duration
}

func newStateLog() *stateLog {
	return &stateLog{states: map[string]ipc.PeerState{}, latency: map[string]time.Duration{}}
}

func (s *stateLog) set(deviceID string, state ipc.PeerState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[deviceID] = state
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

func newNode(t *testing.T, deviceID string) *node {
	t.Helper()

	keys, err := session.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	states := newStateLog()
	m, err := New(deviceID, keys, Events{
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

	return &node{deviceID: deviceID, keys: keys, mesh: m, states: states}
}

func (n *node) endpoint() netip.AddrPort {
	return netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(n.mesh.LocalPort()))
}

// introduce tells each node about the other, which is what the coordination
// server does in the real thing.
func introduce(t *testing.T, a, b *node, aIP, bIP string) {
	t.Helper()
	if err := a.mesh.AddPeer(b.deviceID, "b", netip.MustParseAddr(bIP), b.keys.Public, b.endpoint()); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}
	if err := b.mesh.AddPeer(a.deviceID, "a", netip.MustParseAddr(aIP), a.keys.Public, a.endpoint()); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}
}

func waitForState(t *testing.T, n *node, deviceID string, want ipc.PeerState) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
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
	a := newNode(t, "dev_aaa")
	b := newNode(t, "dev_bbb")
	introduce(t, a, b, "10.69.0.1", "10.69.0.2")

	waitForState(t, a, b.deviceID, ipc.PeerDirect)
	waitForState(t, b, a.deviceID, ipc.PeerDirect)
}

func TestPeersExchangePackets(t *testing.T) {
	a := newNode(t, "dev_aaa")
	b := newNode(t, "dev_bbb")
	introduce(t, a, b, "10.69.0.1", "10.69.0.2")

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

func TestLatencyIsMeasured(t *testing.T) {
	a := newNode(t, "dev_aaa")
	b := newNode(t, "dev_bbb")
	introduce(t, a, b, "10.69.0.1", "10.69.0.2")

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
	a := newNode(t, "dev_aaa")

	if err := a.mesh.Send(netip.MustParseAddr("10.69.0.9"), []byte("hello")); err == nil {
		t.Error("sending to an address nobody has succeeded")
	}
}

func TestSendingBeforeTheLinkOpensFails(t *testing.T) {
	a := newNode(t, "dev_aaa")
	b := newNode(t, "dev_bbb")

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
	a := newNode(t, "dev_aaa")
	b := newNode(t, "dev_bbb")

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
	a := newNode(t, "dev_aaa")
	b := newNode(t, "dev_bbb")
	introduce(t, a, b, "10.69.0.1", "10.69.0.2")
	waitForState(t, a, b.deviceID, ipc.PeerDirect)

	a.mesh.RemovePeer(b.deviceID)
	if err := a.mesh.Send(netip.MustParseAddr("10.69.0.2"), []byte("hello")); err == nil {
		t.Error("a removed peer still had a route")
	}
}

// What happens after a laptop changes network. The old session is useless to
// both sides, so the link has to be rebuilt rather than carried over.
func TestRestartLinksRebuildsAnOpenSession(t *testing.T) {
	a := newNode(t, "dev_aaa")
	b := newNode(t, "dev_bbb")
	introduce(t, a, b, "10.69.0.1", "10.69.0.2")

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
	a := newNode(t, "dev_aaa")

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
