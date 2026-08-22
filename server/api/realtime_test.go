package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	papi "github.com/OMouta/192168/protocol/api"
)

// liveServer runs the handler over a real socket, which is what a WebSocket
// needs and httptest.NewRequest cannot give.
func liveServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	h := newTestServer(t)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, h
}

// listen opens the realtime channel for a session and returns a function that
// reads the next event, failing if none arrives in time.
func listen(t *testing.T, srv *httptest.Server, token, sessionID string) func(papi.EventType) papi.Event {
	t.Helper()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/realtime?session=" + sessionID
	conn, res, err := websocket.Dial(t.Context(), url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}},
	})
	if err != nil {
		status := 0
		if res != nil {
			status = res.StatusCode
		}
		t.Fatalf("dial realtime: %v (status %d)", err, status)
	}
	t.Cleanup(func() { conn.CloseNow() })

	// A subscriber is sent the peers already online before anything else, so a
	// test reads until the event it is actually waiting for.
	return func(want papi.EventType) papi.Event {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		for {
			_, payload, err := conn.Read(ctx)
			if err != nil {
				t.Fatalf("waiting for %s: %v", want, err)
			}
			var event papi.Event
			if err := json.Unmarshal(payload, &event); err != nil {
				t.Fatalf("decode event %q: %v", payload, err)
			}
			if event.Type == want {
				return event
			}
		}
	}
}

// connect creates a group session for a device and returns it.
func connect(t *testing.T, h *Server, token, groupID string) papi.CreateSessionResponse {
	t.Helper()
	var session papi.CreateSessionResponse
	if code := call(t, h, http.MethodPost, "/api/groups/"+groupID+"/sessions", token, nil, &session); code != http.StatusCreated {
		t.Fatalf("connect: status %d", code)
	}
	return session
}

// makeGroup registers a host, creates a group, and returns both.
func makeGroup(t *testing.T, h *Server) (device, papi.Membership) {
	t.Helper()
	host := register(t, h, "dev_host")

	var group papi.Membership
	if code := call(t, h, http.MethodPost, "/api/groups", host.token, papi.CreateGroupRequest{
		Name: "Friday Night",
	}, &group); code != http.StatusCreated {
		t.Fatalf("create group: status %d", code)
	}
	return host, group
}

func joinGroup(t *testing.T, h *Server, id, inviteCode string) device {
	t.Helper()
	guest := register(t, h, id)
	if code := call(t, h, http.MethodPost, "/api/groups/join-by-code", guest.token, papi.JoinByCodeRequest{
		Code: inviteCode,
	}, nil); code != http.StatusOK {
		t.Fatalf("join: status %d", code)
	}
	return guest
}

// The reason the channel exists: a device already connected has to hear about
// someone who joins after it, or its peer list is frozen at connect time.
func TestAConnectedDeviceHearsAboutALaterArrival(t *testing.T) {
	srv, h := liveServer(t)
	host, group := makeGroup(t, h)
	guest := joinGroup(t, h, "dev_guest", group.InviteCode)

	hostSession := connect(t, h, host.token, group.GroupID)
	next := listen(t, srv, host.token, hostSession.SessionID)

	guestSession := connect(t, h, guest.token, group.GroupID)

	event := next(papi.EventPeerOnline)
	var online papi.PeerOnlineData
	if err := json.Unmarshal(event.Data, &online); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if online.Peer.DeviceID != guest.id || online.Peer.VirtualIP != guestSession.VirtualIP {
		t.Errorf("peer = %+v", online.Peer)
	}
	if online.Peer.TransportKey == "" {
		t.Error("the peer arrived without a transport key, so it cannot be authenticated")
	}
}

func TestEndpointChangesReachTheGroup(t *testing.T) {
	srv, h := liveServer(t)
	host, group := makeGroup(t, h)
	guest := joinGroup(t, h, "dev_guest", group.InviteCode)

	hostSession := connect(t, h, host.token, group.GroupID)
	guestSession := connect(t, h, guest.token, group.GroupID)
	next := listen(t, srv, host.token, hostSession.SessionID)

	if code := call(t, h, http.MethodPut, "/api/sessions/"+guestSession.SessionID+"/endpoint", guest.token,
		papi.Endpoint{Protocol: "udp", Address: "198.51.100.20", Port: 44120}, nil); code != http.StatusNoContent {
		t.Fatalf("publish endpoint: status %d", code)
	}

	event := next(papi.EventPeerEndpointUpdated)
	var updated papi.PeerEndpointUpdatedData
	if err := json.Unmarshal(event.Data, &updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.DeviceID != guest.id || updated.Endpoint.Port != 44120 {
		t.Errorf("update = %+v", updated)
	}
}

func TestDisconnectingAnnouncesItself(t *testing.T) {
	srv, h := liveServer(t)
	host, group := makeGroup(t, h)
	guest := joinGroup(t, h, "dev_guest", group.InviteCode)

	hostSession := connect(t, h, host.token, group.GroupID)
	guestSession := connect(t, h, guest.token, group.GroupID)
	next := listen(t, srv, host.token, hostSession.SessionID)

	if code := call(t, h, http.MethodDelete, "/api/sessions/"+guestSession.SessionID, guest.token, nil, nil); code != http.StatusNoContent {
		t.Fatalf("disconnect: status %d", code)
	}

	event := next(papi.EventPeerOffline)
	var offline papi.PeerOfflineData
	if err := json.Unmarshal(event.Data, &offline); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if offline.DeviceID != guest.id {
		t.Errorf("deviceId = %q, want %q", offline.DeviceID, guest.id)
	}
}

func TestExpiredSessionsAreAnnounced(t *testing.T) {
	srv, h := liveServer(t)
	host, group := makeGroup(t, h)
	guest := joinGroup(t, h, "dev_guest", group.InviteCode)

	hostSession := connect(t, h, host.token, group.GroupID)
	connect(t, h, guest.token, group.GroupID)
	next := listen(t, srv, host.token, hostSession.SessionID)

	// A timeout of zero expires everything, which stands in for a client that
	// went away without saying so.
	ctx, cancel := context.WithCancel(t.Context())
	go h.ExpireSessions(ctx, 10*time.Millisecond, 0)
	defer cancel()

	// Reaching this without a timeout is the assertion: an expired session is
	// announced to the rest of the group.
	next(papi.EventPeerOffline)
}

func TestRealtimeNeedsAValidSessionYouOwn(t *testing.T) {
	srv, h := liveServer(t)
	host, group := makeGroup(t, h)
	stranger := joinGroup(t, h, "dev_stranger", group.InviteCode)

	hostSession := connect(t, h, host.token, group.GroupID)

	tests := []struct {
		name      string
		token     string
		sessionID string
	}{
		{"no session", host.token, ""},
		{"invented session", host.token, "ses_nope"},
		{"somebody else's session", stranger.token, hostSession.SessionID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/realtime?session=" + tt.sessionID
			conn, _, err := websocket.Dial(t.Context(), url, &websocket.DialOptions{
				HTTPHeader: http.Header{"Authorization": []string{"Bearer " + tt.token}},
			})
			if err == nil {
				conn.CloseNow()
				t.Error("the connection was accepted")
			}
		})
	}

	// And with no token at all.
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/realtime?session=" + hostSession.SessionID
	conn, _, err := websocket.Dial(t.Context(), url, nil)
	if err == nil {
		conn.CloseNow()
		t.Error("an unauthenticated connection was accepted")
	}
}
