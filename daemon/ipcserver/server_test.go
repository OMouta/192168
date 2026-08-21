package ipcserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/OMouta/192168/protocol/ipc"
)

// fakeHandler records what it was asked and returns what a test tells it to.
type fakeHandler struct {
	mu    sync.Mutex
	calls []string

	state    ipc.State
	groups   []ipc.Group
	group    ipc.Group
	testRes  ipc.TestServerResult
	server   string
	failWith error
}

func (h *fakeHandler) record(name string) {
	h.mu.Lock()
	h.calls = append(h.calls, name)
	h.mu.Unlock()
}

func (h *fakeHandler) recorded() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.calls...)
}

func (h *fakeHandler) GetState(context.Context) (ipc.State, error) {
	h.record("GetState")
	return h.state, h.failWith
}

func (h *fakeHandler) GetGroups(context.Context) ([]ipc.Group, error) {
	h.record("GetGroups")
	return h.groups, h.failWith
}

func (h *fakeHandler) CreateGroup(_ context.Context, p ipc.CreateGroupParams) (ipc.Group, error) {
	h.record("CreateGroup:" + p.Name + ":" + p.Password + ":" + p.Nickname)
	return h.group, h.failWith
}

func (h *fakeHandler) JoinGroup(_ context.Context, p ipc.JoinGroupParams) (ipc.Group, error) {
	h.record("JoinGroup:" + p.Group)
	return h.group, h.failWith
}

func (h *fakeHandler) LeaveGroup(_ context.Context, groupID string) error {
	h.record("LeaveGroup:" + groupID)
	return h.failWith
}

func (h *fakeHandler) Connect(_ context.Context, groupID string) error {
	h.record("Connect:" + groupID)
	return h.failWith
}

func (h *fakeHandler) Disconnect(context.Context) error {
	h.record("Disconnect")
	return h.failWith
}

func (h *fakeHandler) SetNickname(_ context.Context, p ipc.SetNicknameParams) error {
	h.record("SetNickname:" + p.GroupID + ":" + p.Nickname)
	return h.failWith
}

func (h *fakeHandler) RetryPeer(_ context.Context, p ipc.PeerParams) error {
	h.record("RetryPeer:" + p.DeviceID)
	return h.failWith
}

func (h *fakeHandler) RemoveMember(_ context.Context, p ipc.MemberParams) error {
	h.record("RemoveMember:" + p.GroupID + ":" + p.DeviceID)
	return h.failWith
}

func (h *fakeHandler) RenameGroup(_ context.Context, p ipc.RenameGroupParams) error {
	h.record("RenameGroup:" + p.GroupID + ":" + p.Name)
	return h.failWith
}

func (h *fakeHandler) SetGroupAppearance(_ context.Context, p ipc.SetGroupAppearanceParams) error {
	h.record("SetGroupAppearance:" + p.GroupID + ":" + p.Icon + ":" + p.Color)
	return h.failWith
}

func (h *fakeHandler) SetGroupPassword(_ context.Context, p ipc.SetGroupPasswordParams) error {
	h.record("SetGroupPassword:" + p.GroupID)
	return h.failWith
}

func (h *fakeHandler) TransferOwnership(_ context.Context, p ipc.MemberParams) error {
	h.record("TransferOwnership:" + p.GroupID + ":" + p.DeviceID)
	return h.failWith
}

func (h *fakeHandler) DeleteGroup(_ context.Context, groupID string) error {
	h.record("DeleteGroup:" + groupID)
	return h.failWith
}

func (h *fakeHandler) GetServer(context.Context) (string, error) {
	h.record("GetServer")
	return h.server, h.failWith
}

func (h *fakeHandler) SetServer(_ context.Context, url string) error {
	h.record("SetServer:" + url)
	return h.failWith
}

func (h *fakeHandler) TestServer(_ context.Context, url string) (ipc.TestServerResult, error) {
	h.record("TestServer:" + url)
	return h.testRes, h.failWith
}

func (h *fakeHandler) GetLanDiscovery(context.Context) (bool, error) {
	h.record("GetLanDiscovery")
	return true, h.failWith
}

func (h *fakeHandler) SetLanDiscovery(_ context.Context, enabled bool) error {
	h.record(fmt.Sprintf("SetLanDiscovery:%t", enabled))
	return h.failWith
}

func (h *fakeHandler) ResetSettings(context.Context) (string, error) {
	h.record("ResetSettings")
	return h.server, h.failWith
}

// session is a client connected to a server over an in-memory pipe.
type session struct {
	t      *testing.T
	server *Server
	conn   net.Conn
	reader *bufio.Reader
	nextID int
}

func newSession(t *testing.T, handler Handler) *session {
	t.Helper()

	server := New(handler, slog.New(slog.NewTextHandler(io.Discard, nil)))
	listener := newPipeListener()
	t.Cleanup(func() { listener.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go server.Serve(ctx, listener)

	conn, err := listener.dial()
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	s := &session{t: t, server: server, conn: conn, reader: bufio.NewReader(conn)}
	// The connection has to be registered before a test broadcasts, and
	// accepting happens on another goroutine.
	s.waitForClient()
	return s
}

func (s *session) waitForClient() {
	s.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.server.mu.Lock()
		n := len(s.server.clients)
		s.server.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	s.t.Fatal("the server never registered the client")
}

// send writes a request and returns its id.
func (s *session) send(method ipc.Method, params any) string {
	s.t.Helper()
	s.nextID++
	id := "req_" + strconv.Itoa(s.nextID)

	req := ipc.Request{ID: id, Method: method}
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			s.t.Fatalf("marshal params: %v", err)
		}
		req.Params = encoded
	}

	line, err := json.Marshal(req)
	if err != nil {
		s.t.Fatalf("marshal request: %v", err)
	}
	if _, err := s.conn.Write(append(line, '\n')); err != nil {
		s.t.Fatalf("write request: %v", err)
	}
	return id
}

// readLine reads one message, whatever kind it is.
func (s *session) readLine() []byte {
	s.t.Helper()
	s.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, err := s.reader.ReadBytes('\n')
	if err != nil {
		s.t.Fatalf("read: %v", err)
	}
	return line
}

// call sends a request and returns its response.
func (s *session) call(method ipc.Method, params any) ipc.Response {
	s.t.Helper()
	id := s.send(method, params)

	var res ipc.Response
	if err := json.Unmarshal(s.readLine(), &res); err != nil {
		s.t.Fatalf("decode response: %v", err)
	}
	if res.ID != id {
		s.t.Fatalf("response id = %q, want %q", res.ID, id)
	}
	return res
}

func TestEveryMethodReachesTheHandler(t *testing.T) {
	h := &fakeHandler{
		state:   ipc.State{Connection: ipc.StateConnected, VirtualIP: "10.69.0.1"},
		groups:  []ipc.Group{{GroupID: "grp_1", Name: "Friday Night"}},
		group:   ipc.Group{GroupID: "grp_1", Name: "Friday Night", Nickname: "Tiago"},
		server:  "https://api.192168.lol",
		testRes: ipc.TestServerResult{Reachable: true, Version: 1},
	}
	s := newSession(t, h)

	if res := s.call(ipc.MethodGetState, nil); !res.OK {
		t.Fatalf("GetState failed: %+v", res.Err)
	} else {
		var state ipc.State
		if err := json.Unmarshal(res.Result, &state); err != nil {
			t.Fatalf("decode state: %v", err)
		}
		if state.VirtualIP != "10.69.0.1" {
			t.Errorf("state = %+v", state)
		}
	}

	if res := s.call(ipc.MethodGetGroups, nil); !res.OK {
		t.Fatalf("GetGroups failed: %+v", res.Err)
	} else {
		var result ipc.GetGroupsResult
		if err := json.Unmarshal(res.Result, &result); err != nil {
			t.Fatalf("decode groups: %v", err)
		}
		if len(result.Groups) != 1 || result.Groups[0].Name != "Friday Night" {
			t.Errorf("groups = %+v", result.Groups)
		}
	}

	if res := s.call(ipc.MethodCreateGroup, ipc.CreateGroupParams{
		Name: "Friday Night", Password: "hunter2", Nickname: "Tiago",
	}); !res.OK {
		t.Fatalf("CreateGroup failed: %+v", res.Err)
	} else {
		var result ipc.GroupResult
		if err := json.Unmarshal(res.Result, &result); err != nil {
			t.Fatalf("decode group: %v", err)
		}
		if result.Group.GroupID != "grp_1" {
			t.Errorf("group = %+v", result.Group)
		}
	}

	for _, call := range []struct {
		method ipc.Method
		params any
	}{
		{ipc.MethodJoinGroup, ipc.JoinGroupParams{Group: "Friday Night", Password: "hunter2", Nickname: "João"}},
		{ipc.MethodLeaveGroup, ipc.GroupParams{GroupID: "grp_1"}},
		{ipc.MethodConnect, ipc.GroupParams{GroupID: "grp_1"}},
		{ipc.MethodDisconnect, nil},
		{ipc.MethodSetNickname, ipc.SetNicknameParams{GroupID: "grp_1", Nickname: "T"}},
		{ipc.MethodGetServer, nil},
		{ipc.MethodSetServer, ipc.ServerParams{URL: "https://lan.example.com"}},
		{ipc.MethodTestServer, ipc.ServerParams{URL: "https://lan.example.com"}},
		{ipc.MethodGetLanDiscovery, nil},
		{ipc.MethodSetLanDiscovery, ipc.LanDiscoveryParams{Enabled: false}},
		{ipc.MethodResetSettings, nil},
	} {
		if res := s.call(call.method, call.params); !res.OK {
			t.Errorf("%s failed: %+v", call.method, res.Err)
		}
	}

	want := []string{
		"GetState",
		"GetGroups",
		"CreateGroup:Friday Night:hunter2:Tiago",
		"JoinGroup:Friday Night",
		"LeaveGroup:grp_1",
		"Connect:grp_1",
		"Disconnect",
		"SetNickname:grp_1:T",
		"GetServer",
		"SetServer:https://lan.example.com",
		"TestServer:https://lan.example.com",
		"GetLanDiscovery",
		"SetLanDiscovery:false",
		"ResetSettings",
	}
	got := h.recorded()
	if len(got) != len(want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAFailureKeepsItsCodeAndMessage(t *testing.T) {
	h := &fakeHandler{failWith: &Failure{Code: "invalid_password", Message: "That password is not right."}}
	s := newSession(t, h)

	res := s.call(ipc.MethodJoinGroup, ipc.JoinGroupParams{Group: "Friday Night"})
	if res.OK {
		t.Fatal("a failing handler reported success")
	}
	if res.Err.Code != "invalid_password" || res.Err.Message != "That password is not right." {
		t.Errorf("error = %+v", res.Err)
	}
}

func TestAnUnexpectedErrorIsNotShownToTheUser(t *testing.T) {
	// Anything that is not a Failure is a bug, and its text could be a socket
	// number or a query. The user gets something else.
	h := &fakeHandler{failWith: errors.New("dial tcp 10.0.0.1:5432: connection refused")}
	s := newSession(t, h)

	res := s.call(ipc.MethodGetState, nil)
	if res.OK {
		t.Fatal("a failing handler reported success")
	}
	if res.Err.Code != "internal" {
		t.Errorf("code = %q, want internal", res.Err.Code)
	}
	if res.Err.Message == "dial tcp 10.0.0.1:5432: connection refused" {
		t.Error("the raw error text was sent to the client")
	}
}

func TestAnUnknownMethodIsAnswered(t *testing.T) {
	s := newSession(t, &fakeHandler{})

	// A newer client against an older daemon lands here and must not hang.
	res := s.call("Teleport", nil)
	if res.OK {
		t.Fatal("an invented method reported success")
	}
	if res.Err.Code != "unknown_method" {
		t.Errorf("code = %q, want unknown_method", res.Err.Code)
	}
}

func TestMalformedParamsAreRejectedNotIgnored(t *testing.T) {
	h := &fakeHandler{}
	s := newSession(t, h)

	// Params of the wrong shape for the method.
	id := "req_bad"
	line, err := json.Marshal(ipc.Request{ID: id, Method: ipc.MethodConnect, Params: json.RawMessage(`"not-an-object"`)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := s.conn.Write(append(line, '\n')); err != nil {
		t.Fatalf("write: %v", err)
	}

	var res ipc.Response
	if err := json.Unmarshal(s.readLine(), &res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if res.OK || res.Err.Code != "bad_request" {
		t.Errorf("response = %+v", res)
	}
	if calls := h.recorded(); len(calls) != 0 {
		t.Errorf("the handler was called with bad params: %v", calls)
	}
}

func TestEventsReachAConnectedClient(t *testing.T) {
	s := newSession(t, &fakeHandler{})

	s.server.Broadcast(ipc.EventPeerStateChanged, ipc.PeerStateChangedData{
		DeviceID: "dev_2", State: ipc.PeerDirect,
	})

	var event ipc.Event
	if err := json.Unmarshal(s.readLine(), &event); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if event.Event != ipc.EventPeerStateChanged {
		t.Fatalf("event = %q", event.Event)
	}
	var data ipc.PeerStateChangedData
	if err := event.UnmarshalData(&data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if data.DeviceID != "dev_2" || data.State != ipc.PeerDirect {
		t.Errorf("data = %+v", data)
	}
}

func TestEventsReachEveryClient(t *testing.T) {
	// A tray process and a window can both be connected, and both have to see
	// the same events.
	h := &fakeHandler{}
	server := New(h, slog.New(slog.NewTextHandler(io.Discard, nil)))
	listener := newPipeListener()
	defer listener.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go server.Serve(ctx, listener)

	var readers []*bufio.Reader
	for range 3 {
		conn, err := listener.dial()
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		readers = append(readers, bufio.NewReader(conn))
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		server.mu.Lock()
		n := len(server.clients)
		server.mu.Unlock()
		if n == 3 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	server.Broadcast(ipc.EventGroupDisconnected, ipc.GroupDisconnectedData{GroupID: "grp_1"})

	for i, reader := range readers {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatalf("client %d read: %v", i, err)
		}
		var event ipc.Event
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("client %d decode: %v", i, err)
		}
		if event.Event != ipc.EventGroupDisconnected {
			t.Errorf("client %d got %q", i, event.Event)
		}
	}
}

func TestServeStopsWhenTheContextIsCancelled(t *testing.T) {
	server := New(&fakeHandler{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	listener := newPipeListener()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v, want nil on cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not stop")
	}
}
