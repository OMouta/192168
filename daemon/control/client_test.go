package control

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/OMouta/192168/daemon/identity"
	"github.com/OMouta/192168/protocol"
	"github.com/OMouta/192168/protocol/api"
	"github.com/OMouta/192168/protocol/auth"
	serverapi "github.com/OMouta/192168/server/api"
	"github.com/OMouta/192168/server/config"
	"github.com/OMouta/192168/server/storage"
)

// liveServer runs the real coordination server, so these tests prove the two
// halves agree rather than that the client matches a hand written stub.
func liveServer(t *testing.T) string {
	t.Helper()

	store, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	// The handler needs to know its own public URL, which is only known once
	// the listener has a port.
	srv := httptest.NewUnstartedServer(nil)
	url := "http://" + srv.Listener.Addr().String()
	srv.Config.Handler = serverapi.New(config.Config{
		PublicURL: url,
		STUN:      []string{"stun:stun.example.com:3478"},
	}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.Start()
	t.Cleanup(srv.Close)

	return url
}

// newDevice creates an identity and registers it, returning a ready client.
func newDevice(t *testing.T, url string) (*Client, *identity.Identity) {
	t.Helper()

	id, err := identity.Load(t.TempDir())
	if err != nil {
		t.Fatalf("identity.Load: %v", err)
	}
	c, err := Discover(t.Context(), url)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	token, err := c.Register(t.Context(), id.DeviceID, id.Name, id.PublicKey(), id.TransportKey(),
		func(publicKey, transportKey string, issuedAt time.Time, nonce string) string {
			return auth.SignRegister(id.Signing, id.DeviceID, publicKey, transportKey, issuedAt, nonce)
		})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := id.SetToken(url, token); err != nil {
		t.Fatalf("SetToken: %v", err)
	}
	c.SetToken(token)

	return c, id
}

func TestDiscover(t *testing.T) {
	url := liveServer(t)

	c, err := Discover(t.Context(), url)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	doc := c.Discovery()
	if doc.API != url+"/api" {
		t.Errorf("api = %q, want %q", doc.API, url+"/api")
	}
	// The STUN list has to arrive, since it is how the daemon learns where to
	// ask for its public address without anything being compiled in.
	if len(doc.STUN) != 1 || doc.STUN[0] != "stun:stun.example.com:3478" {
		t.Errorf("stun = %v", doc.STUN)
	}
}

func TestDiscoverRejectsAnIncompatibleServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(api.Discovery{
			Version:  protocol.DiscoveryVersion + 1,
			API:      "https://example.com/api",
			Realtime: "wss://example.com/realtime",
		})
	}))
	defer srv.Close()

	// A self-hosted server can be older or newer than the app, so this has to
	// be a clear message rather than a confusing failure later on.
	_, err := Discover(t.Context(), srv.URL)
	var e *Error
	if err == nil {
		t.Fatal("an incompatible server was accepted")
	}
	if !errors.As(err, &e) || e.Code != api.ErrVersionUnsupported {
		t.Fatalf("err = %v, want version_unsupported", err)
	}
}

func TestDiscoverOnAnUnreachableServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	_, err := Discover(t.Context(), url)
	var e *Error
	if err == nil || !errors.As(err, &e) || e.Code != "unreachable" {
		t.Fatalf("err = %v, want unreachable", err)
	}
}

// The flow the daemon runs when a user hits Connect.
func TestFullFlowAgainstTheRealServer(t *testing.T) {
	url := liveServer(t)
	host, hostID := newDevice(t, url)
	guest, guestID := newDevice(t, url)

	group, err := host.CreateGroup(t.Context(), NewGroup{Name: "Friday Night", Password: "hunter2"})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if group.GroupID == "" || group.Subnet == "" {
		t.Fatalf("group = %+v", group)
	}

	joined, err := guest.JoinGroup(t.Context(), "friday night", "hunter2")
	if err != nil {
		t.Fatalf("JoinGroup: %v", err)
	}
	if joined.GroupID != group.GroupID {
		t.Fatalf("joined %q, want %q", joined.GroupID, group.GroupID)
	}

	groups, err := guest.Groups(t.Context())
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %+v", groups)
	}

	hostSession, err := host.Connect(t.Context(), group.GroupID)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if hostSession.VirtualIP != "10.69.0.1" {
		t.Errorf("host ip = %q, want 10.69.0.1", hostSession.VirtualIP)
	}

	if err := host.PublishEndpoint(t.Context(), hostSession.SessionID, api.Endpoint{
		Address: "203.0.113.50", Port: 51821,
	}); err != nil {
		t.Fatalf("PublishEndpoint: %v", err)
	}

	guestSession, err := guest.Connect(t.Context(), group.GroupID)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if len(guestSession.Peers) != 1 {
		t.Fatalf("peers = %+v", guestSession.Peers)
	}

	// The peer the guest sees has to carry the host's real transport key, or
	// the handshake has nothing to authenticate against.
	peer := guestSession.Peers[0]
	if peer.DeviceID != hostID.DeviceID {
		t.Errorf("peer device = %q, want %q", peer.DeviceID, hostID.DeviceID)
	}
	if peer.TransportKey != hostID.TransportKey() {
		t.Errorf("peer transport key = %q, want %q", peer.TransportKey, hostID.TransportKey())
	}
	if peer.Endpoint == nil || peer.Endpoint.Port != 51821 {
		t.Errorf("peer endpoint = %+v", peer.Endpoint)
	}

	if err := guest.Heartbeat(t.Context(), guestSession.SessionID); err != nil {
		t.Errorf("Heartbeat: %v", err)
	}
	if err := guest.SetNickname(t.Context(), "Joao"); err != nil {
		t.Errorf("SetNickname: %v", err)
	}
	if err := guest.Disconnect(t.Context(), guestSession.SessionID); err != nil {
		t.Errorf("Disconnect: %v", err)
	}
	if err := guest.LeaveGroup(t.Context(), group.GroupID); err != nil {
		t.Errorf("LeaveGroup: %v", err)
	}

	// Leaving means leaving, and the guest identity is still valid otherwise.
	if _, err := guest.Connect(t.Context(), group.GroupID); err == nil {
		t.Error("connecting after leaving worked")
	}
	if guestID.Token == "" {
		t.Error("the guest lost its token")
	}
}

func TestErrorsCarryTheServersCode(t *testing.T) {
	url := liveServer(t)
	host, _ := newDevice(t, url)
	guest, _ := newDevice(t, url)

	if _, err := host.CreateGroup(t.Context(), NewGroup{Name: "Friday Night", Password: "hunter2"}); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	tests := []struct {
		name string
		call func() error
		want string
	}{
		{
			name: "wrong password",
			call: func() error {
				_, err := guest.JoinGroup(t.Context(), "Friday Night", "wrong")
				return err
			},
			want: api.ErrInvalidPassword,
		},
		{
			name: "duplicate group name",
			call: func() error {
				_, err := host.CreateGroup(t.Context(), NewGroup{Name: "Friday Night", Password: "hunter2"})
				return err
			},
			want: api.ErrGroupNameTaken,
		},
		{
			name: "session that does not exist",
			call: func() error {
				return host.Disconnect(t.Context(), "ses_nope")
			},
			want: api.ErrSessionInvalid,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			var e *Error
			if err == nil || !errors.As(err, &e) {
				t.Fatalf("err = %v, want a control error", err)
			}
			if e.Code != tt.want {
				t.Errorf("code = %q, want %q", e.Code, tt.want)
			}
			if e.Message == "" {
				t.Error("the error has no message to show the user")
			}
		})
	}
}

func TestABadTokenIsRecognisable(t *testing.T) {
	url := liveServer(t)
	c, _ := newDevice(t, url)

	// A revoked or stale token has to be told apart from other failures, since
	// the answer is to register again rather than to retry.
	c.SetToken("not-a-real-token")
	_, err := c.Groups(t.Context())
	if err == nil {
		t.Fatal("a junk token was accepted")
	}
	if !IsUnauthorized(err) {
		t.Errorf("err = %v, want unauthorized", err)
	}
}
