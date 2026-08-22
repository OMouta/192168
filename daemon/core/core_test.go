package core

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/OMouta/192168/daemon/identity"
	"github.com/OMouta/192168/daemon/ipcserver"
	"github.com/OMouta/192168/protocol/api"
	"github.com/OMouta/192168/protocol/invite"
	"github.com/OMouta/192168/protocol/ipc"
	serverapi "github.com/OMouta/192168/server/api"
	serverconfig "github.com/OMouta/192168/server/config"
	"github.com/OMouta/192168/server/storage"
)

// recorder collects the events a core emits, so a test can wait for the one it
// expects instead of sleeping.
type recorder struct {
	mu     sync.Mutex
	events []ipc.Event
	waits  []chan struct{}
}

func (r *recorder) Broadcast(name ipc.EventName, data any) {
	r.mu.Lock()
	r.events = append(r.events, ipc.Event{Event: name})
	_ = data
	waits := r.waits
	r.waits = nil
	r.mu.Unlock()

	for _, ch := range waits {
		close(ch)
	}
}

// waitFor blocks until an event of this name has been seen.
func (r *recorder) waitFor(t *testing.T, name ipc.EventName) {
	t.Helper()
	deadline := time.After(5 * time.Second)

	for {
		r.mu.Lock()
		for _, e := range r.events {
			if e.Event == name {
				r.mu.Unlock()
				return
			}
		}
		ch := make(chan struct{})
		r.waits = append(r.waits, ch)
		r.mu.Unlock()

		select {
		case <-ch:
		case <-deadline:
			t.Fatalf("no %s event arrived", name)
		}
	}
}

// liveServer runs the real coordination server.
func liveServer(t *testing.T) string {
	return liveServerWithStun(t, "stun:stun.example.com:3478")
}

// liveServerWithStun runs the real coordination server, advertising whichever
// STUN server the test wants daemons to use.
func liveServerWithStun(t *testing.T, stunServer string) string {
	t.Helper()

	store, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	srv := httptest.NewUnstartedServer(nil)
	url := "http://" + srv.Listener.Addr().String()
	srv.Config.Handler = serverapi.New(serverconfig.Config{
		PublicURL: url,
		STUN:      []string{stunServer},
	}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.Start()
	t.Cleanup(srv.Close)

	return url
}

// newCore builds a core pointed at a server, with its own data directory.
func newCore(t *testing.T, serverURL string) (*Core, *recorder) {
	t.Helper()

	dir := t.TempDir()
	id, err := identity.Load(dir)
	if err != nil {
		t.Fatalf("identity.Load: %v", err)
	}

	events := &recorder{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if testing.Verbose() {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	c, err := New(t.Context(), id, dir, serverURL, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.SetEvents(events)
	t.Cleanup(c.Close)

	return c, events
}

// named picks what a core is called. Without it a device answers to the machine
// it runs on, which differs per machine.
func named(t *testing.T, c *Core, nickname string) {
	t.Helper()
	if err := c.SetNickname(t.Context(), ipc.SetNicknameParams{Nickname: nickname}); err != nil {
		t.Fatalf("SetNickname: %v", err)
	}
}

// The whole point of the core: a click on Connect ends with a virtual IP.
func TestConnectingToAGroup(t *testing.T) {
	url := liveServer(t)
	c, events := newCore(t, url)
	ctx := t.Context()
	named(t, c, "Tiago")

	group, err := c.CreateGroup(ctx, ipc.CreateGroupParams{
		Name: "Friday Night",
	})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	if err := c.Connect(ctx, group.GroupID); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Connect returns before the work is done, so the answer comes as an event.
	events.waitFor(t, ipc.EventGroupConnected)

	state, err := c.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.Connection != ipc.StateConnected {
		t.Fatalf("state = %+v", state)
	}
	if state.VirtualIP != "10.69.0.1" {
		t.Errorf("virtual ip = %q, want 10.69.0.1", state.VirtualIP)
	}
	if state.GroupName != "Friday Night" || state.Nickname != "Tiago" {
		t.Errorf("state = %+v", state)
	}
	if !state.ServerOnline {
		t.Error("the server is answering but the state says otherwise")
	}

	groups, err := c.GetGroups(ctx)
	if err != nil {
		t.Fatalf("GetGroups: %v", err)
	}
	if len(groups) != 1 || !groups[0].Active {
		t.Errorf("groups = %+v", groups)
	}

	if err := c.Disconnect(ctx); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	events.waitFor(t, ipc.EventGroupDisconnected)

	state, _ = c.GetState(ctx)
	if state.Connection != ipc.StateDisconnected || state.VirtualIP != "" {
		t.Errorf("state after disconnect = %+v", state)
	}
}

func TestPeersArriveKnownButNotYetReachable(t *testing.T) {
	url := liveServer(t)
	host, hostEvents := newCore(t, url)
	guest, guestEvents := newCore(t, url)
	ctx := t.Context()
	named(t, host, "Tiago")

	group, err := host.CreateGroup(ctx, ipc.CreateGroupParams{
		Name: "Friday Night",
	})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if _, err := guest.JoinGroup(ctx, ipc.JoinGroupParams{Code: group.InviteCode}); err != nil {
		t.Fatalf("JoinGroup: %v", err)
	}

	if err := host.Connect(ctx, group.GroupID); err != nil {
		t.Fatalf("host Connect: %v", err)
	}
	hostEvents.waitFor(t, ipc.EventGroupConnected)

	if err := guest.Connect(ctx, group.GroupID); err != nil {
		t.Fatalf("guest Connect: %v", err)
	}
	guestEvents.waitFor(t, ipc.EventGroupConnected)

	state, _ := guest.GetState(ctx)
	if len(state.Peers) != 1 {
		t.Fatalf("peers = %+v", state.Peers)
	}
	peer := state.Peers[0]
	if peer.Nickname != "Tiago" || peer.VirtualIP != "10.69.0.1" {
		t.Errorf("peer = %+v", peer)
	}
	// No tunnel exists yet, and saying anything else here would be a lie the UI
	// would repeat.
	if peer.State != ipc.PeerConnecting {
		t.Errorf("peer state = %q, want %q", peer.State, ipc.PeerConnecting)
	}
}

// Somebody in the group who is not connected still has an address. It used to
// be blank until they showed up.
func TestAMemberWhoIsAwayHasAnAddress(t *testing.T) {
	url := liveServer(t)
	host, hostEvents := newCore(t, url)
	guest, _ := newCore(t, url)
	ctx := t.Context()

	group, err := host.CreateGroup(ctx, ipc.CreateGroupParams{
		Name: "Friday Night",
	})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if _, err := guest.JoinGroup(ctx, ipc.JoinGroupParams{Code: group.InviteCode}); err != nil {
		t.Fatalf("JoinGroup: %v", err)
	}

	// Only the host connects.
	if err := host.Connect(ctx, group.GroupID); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	hostEvents.waitFor(t, ipc.EventGroupConnected)

	// The member list arrives after the session does, so this waits for it.
	peers := awaitPeers(t, host, 1)
	if peers[0].State != ipc.PeerOffline {
		t.Errorf("peer state = %q, want %q", peers[0].State, ipc.PeerOffline)
	}
	if peers[0].VirtualIP != "10.69.0.2" {
		t.Errorf("peer address = %q, want 10.69.0.2", peers[0].VirtualIP)
	}
}

// awaitPeers waits for the peer list to reach a size. The daemon fills that
// list from a call it makes on its own, so there is nothing else to wait on.
func awaitPeers(t *testing.T, c *Core, want int) []ipc.PeerView {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)

	for {
		state, err := c.GetState(t.Context())
		if err != nil {
			t.Fatalf("GetState: %v", err)
		}
		if len(state.Peers) == want {
			return state.Peers
		}
		if time.Now().After(deadline) {
			t.Fatalf("peers = %+v, want %d of them", state.Peers, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSwitchingGroupsDisconnectsTheFirst(t *testing.T) {
	url := liveServer(t)
	c, events := newCore(t, url)
	ctx := t.Context()

	first, err := c.CreateGroup(ctx, ipc.CreateGroupParams{Name: "Friday Night"})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	second, err := c.CreateGroup(ctx, ipc.CreateGroupParams{Name: "BeamNG"})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	if err := c.Connect(ctx, first.GroupID); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	events.waitFor(t, ipc.EventGroupConnected)

	if err := c.Connect(ctx, second.GroupID); err != nil {
		t.Fatalf("Connect to the second group: %v", err)
	}
	events.waitFor(t, ipc.EventGroupDisconnected)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, _ := c.GetState(ctx)
		if state.Connection == ipc.StateConnected && state.GroupID == second.GroupID {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	state, _ := c.GetState(ctx)
	t.Fatalf("never landed on the second group: %+v", state)
}

func TestConnectingToAGroupYouAreNotInFails(t *testing.T) {
	url := liveServer(t)
	host, _ := newCore(t, url)
	stranger, strangerEvents := newCore(t, url)
	ctx := t.Context()

	group, err := host.CreateGroup(ctx, ipc.CreateGroupParams{
		Name: "Friday Night",
	})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	if err := stranger.Connect(ctx, group.GroupID); err != nil {
		t.Fatalf("Connect returned an error before it started: %v", err)
	}
	strangerEvents.waitFor(t, ipc.EventStateChanged)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, _ := stranger.GetState(ctx)
		if state.Connection == ipc.StateDisconnected && state.Message != "" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	state, _ := stranger.GetState(ctx)
	t.Fatalf("the failure was never reported: %+v", state)
}

func TestAnInviteThatOpensNothingIsReportedToTheUser(t *testing.T) {
	url := liveServer(t)
	host, _ := newCore(t, url)
	guest, _ := newCore(t, url)
	ctx := t.Context()

	if _, err := host.CreateGroup(ctx, ipc.CreateGroupParams{
		Name: "Friday Night",
	}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	_, err := guest.JoinGroup(ctx, ipc.JoinGroupParams{Code: "nosuchco"})
	var failure *ipcserver.Failure
	if err == nil || !errors.As(err, &failure) {
		t.Fatalf("err = %v, want a failure", err)
	}
	if failure.Code != api.ErrInviteInvalid {
		t.Errorf("code = %q, want %q", failure.Code, api.ErrInviteInvalid)
	}
	if failure.Message == "" {
		t.Error("the failure has nothing to show the user")
	}
}

func TestServerSettings(t *testing.T) {
	url := liveServer(t)
	c, _ := newCore(t, url)
	ctx := t.Context()

	got, err := c.GetServer(ctx)
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if got != url {
		t.Errorf("server = %q, want %q", got, url)
	}

	result, err := c.TestServer(ctx, url)
	if err != nil {
		t.Fatalf("TestServer: %v", err)
	}
	if !result.Reachable || result.Version != 1 {
		t.Errorf("result = %+v", result)
	}

	// A server that is not there is an answer, not an error. The user asked a
	// question and deserves one back.
	result, err = c.TestServer(ctx, "http://localhost:1")
	if err != nil {
		t.Fatalf("TestServer on a dead address: %v", err)
	}
	if result.Reachable || result.Message == "" {
		t.Errorf("result = %+v", result)
	}

	// Addresses the daemon will not use are refused outright.
	for _, bad := range []string{"", "http://example.com", "gopher://example.com"} {
		if _, err := c.TestServer(ctx, bad); err == nil {
			t.Errorf("TestServer(%q) was accepted", bad)
		}
	}
}

func TestChangingServerPersistsAndDisconnects(t *testing.T) {
	first := liveServer(t)
	second := liveServer(t)

	dir := t.TempDir()
	id, err := identity.Load(dir)
	if err != nil {
		t.Fatalf("identity.Load: %v", err)
	}
	events := &recorder{}
	c, err := New(t.Context(), id, dir, first, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.SetEvents(events)
	ctx := t.Context()

	group, err := c.CreateGroup(ctx, ipc.CreateGroupParams{Name: "Friday Night"})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if err := c.Connect(ctx, group.GroupID); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	events.waitFor(t, ipc.EventGroupConnected)

	if err := c.SetServer(ctx, second); err != nil {
		t.Fatalf("SetServer: %v", err)
	}
	state, _ := c.GetState(ctx)
	if state.Connection != ipc.StateDisconnected {
		t.Errorf("changing servers left a connection: %+v", state)
	}
	c.Close()

	// The choice has to survive a restart, and the device has to register with
	// the new server, since a token is only good where it was issued.
	reloaded, err := identity.Load(dir)
	if err != nil {
		t.Fatalf("identity.Load: %v", err)
	}
	again, err := New(context.Background(), reloaded, dir, first, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer again.Close()

	got, _ := again.GetServer(context.Background())
	if got != second {
		t.Errorf("server after restart = %q, want %q", got, second)
	}
	if _, err := again.GetGroups(context.Background()); err != nil {
		t.Fatalf("GetGroups against the new server: %v", err)
	}
	if reloaded.ServerURL != second {
		t.Errorf("the device registered with %q, want %q", reloaded.ServerURL, second)
	}
}

func TestDisconnectIsIdempotent(t *testing.T) {
	url := liveServer(t)
	c, _ := newCore(t, url)
	ctx := t.Context()

	// It runs on the way into several other things, so calling it when nothing
	// is connected has to be fine.
	for range 3 {
		if err := c.Disconnect(ctx); err != nil {
			t.Fatalf("Disconnect: %v", err)
		}
	}
}

func TestARevokedTokenIsRecoveredFrom(t *testing.T) {
	url := liveServer(t)
	c, _ := newCore(t, url)
	ctx := t.Context()

	if _, err := c.GetGroups(ctx); err != nil {
		t.Fatalf("GetGroups: %v", err)
	}

	// A self-hosted server whose database was reset looks exactly like this,
	// and it should not take a reinstall to recover.
	if err := c.identity.SetToken(url, "no-longer-valid"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}
	c.mu.Lock()
	c.client.SetToken("no-longer-valid")
	c.mu.Unlock()

	if _, err := c.GetGroups(ctx); err != nil {
		t.Fatalf("GetGroups after the token went bad: %v", err)
	}
	if c.identity.Token == "no-longer-valid" {
		t.Error("the daemon kept using the dead token")
	}
}

// A rename has to reach whoever is looking at you now, not wait for them to
// reconnect, and it has to be written down.
func TestRenamingReachesTheGroupAndIsRemembered(t *testing.T) {
	url := liveServer(t)
	host, hostEvents := newCore(t, url)
	guest, guestEvents := newCore(t, url)
	ctx := t.Context()
	named(t, host, "Tiago")
	named(t, guest, "Joao")

	group, err := host.CreateGroup(ctx, ipc.CreateGroupParams{
		Name: "Friday Night",
	})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if _, err := guest.JoinGroup(ctx, ipc.JoinGroupParams{Code: group.InviteCode}); err != nil {
		t.Fatalf("JoinGroup: %v", err)
	}

	if err := host.Connect(ctx, group.GroupID); err != nil {
		t.Fatalf("host Connect: %v", err)
	}
	hostEvents.waitFor(t, ipc.EventGroupConnected)
	if err := guest.Connect(ctx, group.GroupID); err != nil {
		t.Fatalf("guest Connect: %v", err)
	}
	guestEvents.waitFor(t, ipc.EventGroupConnected)

	named(t, guest, "João")
	waitForPeerNickname(t, host, "João")

	// The guest's own state says so too.
	state, _ := guest.GetState(ctx)
	if state.Nickname != "João" {
		t.Errorf("state = %+v", state)
	}
	if guest.identity.Nickname != "João" {
		t.Errorf("the name was not written down, identity = %q", guest.identity.Nickname)
	}
}

func waitForPeerNickname(t *testing.T, c *Core, want string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	var last ipc.State
	for time.Now().Before(deadline) {
		state, err := c.GetState(t.Context())
		if err != nil {
			t.Fatalf("GetState: %v", err)
		}
		last = state
		for _, peer := range state.Peers {
			if peer.Nickname == want {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no peer was renamed to %q, last state was %+v", want, last)
}

// The owner is handed a link, not a code. A link is what people send.
func TestTheOwnerGetsALinkToSend(t *testing.T) {
	url := liveServer(t)
	owner, _ := newCore(t, url)
	friend, _ := newCore(t, url)
	ctx := t.Context()

	group, err := owner.CreateGroup(ctx, ipc.CreateGroupParams{Name: "Friday Night"})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	want := url + invite.Path + group.InviteCode
	if group.InviteLink != want {
		t.Errorf("link = %q, want %q", group.InviteLink, want)
	}

	// The link works as well as the code, and it is what gets pasted.
	joined, err := friend.JoinGroup(ctx, ipc.JoinGroupParams{Code: group.InviteLink})
	if err != nil {
		t.Fatalf("JoinGroup with a link: %v", err)
	}
	if joined.GroupID != group.GroupID {
		t.Fatalf("joined %q, want %q", joined.GroupID, group.GroupID)
	}
	// A member is not handed one.
	if joined.InviteCode != "" || joined.InviteLink != "" {
		t.Errorf("a member was handed an invite: %+v", joined)
	}

	// Seeing what a link opens takes no membership.
	preview, err := friend.GetInvite(ctx, ipc.InviteParams{Code: group.InviteLink})
	if err != nil {
		t.Fatalf("GetInvite: %v", err)
	}
	if !preview.Found || preview.GroupName != "Friday Night" || preview.Members != 2 {
		t.Errorf("preview = %+v", preview)
	}

	// A code that opens nothing is an answer, not a failure. Half-typed codes
	// are invalid on the way to being valid.
	missing, err := friend.GetInvite(ctx, ipc.InviteParams{Code: "nosuchco"})
	if err != nil {
		t.Fatalf("GetInvite for an invented code: %v", err)
	}
	if missing.Found {
		t.Errorf("an invented code found something: %+v", missing)
	}
}

// Replacing a code retires the one that was given out.
func TestResettingTheCodeStopsTheOldLink(t *testing.T) {
	url := liveServer(t)
	owner, _ := newCore(t, url)
	stranger, _ := newCore(t, url)
	ctx := t.Context()

	group, err := owner.CreateGroup(ctx, ipc.CreateGroupParams{Name: "Friday Night"})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	fresh, err := owner.ResetInvite(ctx, ipc.GroupParams{GroupID: group.GroupID})
	if err != nil {
		t.Fatalf("ResetInvite: %v", err)
	}
	if fresh.Code == group.InviteCode || fresh.Link == "" {
		t.Fatalf("reset = %+v, old code was %q", fresh, group.InviteCode)
	}

	if _, err := stranger.JoinGroup(ctx, ipc.JoinGroupParams{Code: group.InviteLink}); err == nil {
		t.Error("the retired link still worked")
	}
	if _, err := stranger.JoinGroup(ctx, ipc.JoinGroupParams{Code: fresh.Link}); err != nil {
		t.Errorf("the new link did not work: %v", err)
	}
}
