package api

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/OMouta/192168/protocol"
	papi "github.com/OMouta/192168/protocol/api"
	"github.com/OMouta/192168/protocol/auth"
	"github.com/OMouta/192168/protocol/session"
	"github.com/OMouta/192168/server/config"
	"github.com/OMouta/192168/server/storage"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	store, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	cfg := config.Config{PublicURL: "https://api.192168.lol", STUN: []string{"stun:stun.example.com:3478"}}
	return New(cfg, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// call sends a request and decodes the response into out when out is not nil.
func call(t *testing.T, h http.Handler, method, path, token string, body, out any) int {
	t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req := httptest.NewRequest(method, path, reader)
	req.RemoteAddr = "203.0.113.10:5000"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if out != nil && rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s %s response %q: %v", method, path, rec.Body.String(), err)
		}
	}
	return rec.Code
}

// device is a registered test device with everything needed to make requests.
type device struct {
	id    string
	token string
}

func register(t *testing.T, h http.Handler, id string) device {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	keys, err := session.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	publicKey := auth.EncodePublicKey(priv.Public().(ed25519.PublicKey))
	transportKey := auth.EncodePublicKey(ed25519.PublicKey(keys.Public))
	issuedAt := time.Now()
	nonce := "nonce-" + id

	req := papi.RegisterDeviceRequest{
		DeviceID:     id,
		PublicKey:    publicKey,
		TransportKey: transportKey,
		DeviceName:   id + "-PC",
		IssuedAt:     issuedAt.Unix(),
		Nonce:        nonce,
		Signature:    auth.SignRegister(priv, id, publicKey, transportKey, issuedAt, nonce),
	}

	var res papi.RegisterDeviceResponse
	if code := call(t, h, http.MethodPost, "/api/devices/register", "", req, &res); code != http.StatusCreated {
		t.Fatalf("register %s: status %d", id, code)
	}
	if res.DeviceToken == "" {
		t.Fatalf("register %s returned no token", id)
	}
	return device{id: id, token: res.DeviceToken}
}

func TestRegistrationRejectsAReplay(t *testing.T) {
	h := newTestServer(t)

	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	publicKey := auth.EncodePublicKey(priv.Public().(ed25519.PublicKey))
	issuedAt := time.Now()

	req := papi.RegisterDeviceRequest{
		DeviceID:     "dev_1",
		PublicKey:    publicKey,
		TransportKey: "transport",
		IssuedAt:     issuedAt.Unix(),
		Nonce:        "nonce-1",
		Signature:    auth.SignRegister(priv, "dev_1", publicKey, "transport", issuedAt, "nonce-1"),
	}

	if code := call(t, h, http.MethodPost, "/api/devices/register", "", req, nil); code != http.StatusCreated {
		t.Fatalf("first registration: status %d", code)
	}
	// The same signed message a second time is someone replaying a capture.
	if code := call(t, h, http.MethodPost, "/api/devices/register", "", req, nil); code != http.StatusUnauthorized {
		t.Errorf("replayed registration: status %d, want 401", code)
	}
}

func TestRegistrationRejectsBadSignaturesAndStaleClocks(t *testing.T) {
	h := newTestServer(t)

	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	publicKey := auth.EncodePublicKey(priv.Public().(ed25519.PublicKey))

	now := time.Now()
	stale := now.Add(-2 * auth.RegisterMaxSkew)

	tests := []struct {
		name string
		req  papi.RegisterDeviceRequest
		want int
	}{
		{
			name: "unsigned",
			req: papi.RegisterDeviceRequest{
				DeviceID: "dev_a", PublicKey: publicKey, TransportKey: "t",
				IssuedAt: now.Unix(), Nonce: "n1", Signature: "",
			},
			want: http.StatusUnauthorized,
		},
		{
			name: "signed for a different device",
			req: papi.RegisterDeviceRequest{
				DeviceID: "dev_b", PublicKey: publicKey, TransportKey: "t",
				IssuedAt: now.Unix(), Nonce: "n2",
				Signature: auth.SignRegister(priv, "dev_other", publicKey, "t", now, "n2"),
			},
			want: http.StatusUnauthorized,
		},
		{
			name: "clock too far off",
			req: papi.RegisterDeviceRequest{
				DeviceID: "dev_c", PublicKey: publicKey, TransportKey: "t",
				IssuedAt: stale.Unix(), Nonce: "n3",
				Signature: auth.SignRegister(priv, "dev_c", publicKey, "t", stale, "n3"),
			},
			want: http.StatusBadRequest,
		},
		{
			name: "missing fields",
			req:  papi.RegisterDeviceRequest{DeviceID: "dev_d"},
			want: http.StatusBadRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code := call(t, h, http.MethodPost, "/api/devices/register", "", tt.req, nil); code != tt.want {
				t.Errorf("status %d, want %d", code, tt.want)
			}
		})
	}
}

func TestEndpointsNeedAToken(t *testing.T) {
	h := newTestServer(t)

	for _, path := range []string{"/api/groups"} {
		if code := call(t, h, http.MethodGet, path, "", nil, nil); code != http.StatusUnauthorized {
			t.Errorf("GET %s without a token: status %d, want 401", path, code)
		}
		if code := call(t, h, http.MethodGet, path, "not-a-real-token", nil, nil); code != http.StatusUnauthorized {
			t.Errorf("GET %s with a junk token: status %d, want 401", path, code)
		}
	}
}

// The flow a real client walks: register, create a group, have a friend join,
// both connect, exchange endpoints, disconnect.
func TestGroupAndSessionFlow(t *testing.T) {
	h := newTestServer(t)
	host := register(t, h, "dev_host")
	guest := register(t, h, "dev_guest")

	proof := auth.DeriveGroupProof("hunter2", "Friday Night")

	var created papi.Membership
	if code := call(t, h, http.MethodPost, "/api/groups", host.token, papi.CreateGroupRequest{
		Name: "Friday Night", PasswordProof: proof, Nickname: "Tiago",
	}, &created); code != http.StatusCreated {
		t.Fatalf("create group: status %d", code)
	}
	if created.GroupID == "" || created.Subnet != DefaultSubnet {
		t.Fatalf("membership = %+v", created)
	}

	// The name is matched loosely, so what someone types finds the group.
	var joined papi.Membership
	if code := call(t, h, http.MethodPost, "/api/groups/join", guest.token, papi.JoinGroupRequest{
		Group: "  friday night ", PasswordProof: proof, Nickname: "João",
	}, &joined); code != http.StatusOK {
		t.Fatalf("join group: status %d", code)
	}
	if joined.GroupID != created.GroupID {
		t.Fatalf("joined %q, want %q", joined.GroupID, created.GroupID)
	}
	// Joining is what hands out an address, so both have one before either of
	// them has connected.
	if created.VirtualIP != "10.69.0.1" || joined.VirtualIP != "10.69.0.2" {
		t.Fatalf("addresses = %q and %q", created.VirtualIP, joined.VirtualIP)
	}

	var groups []papi.Membership
	if code := call(t, h, http.MethodGet, "/api/groups", guest.token, nil, &groups); code != http.StatusOK {
		t.Fatalf("list groups: status %d", code)
	}
	if len(groups) != 1 || groups[0].Nickname != "João" {
		t.Fatalf("groups = %+v", groups)
	}

	sessionPath := "/api/groups/" + created.GroupID + "/sessions"

	var hostSession papi.CreateSessionResponse
	if code := call(t, h, http.MethodPost, sessionPath, host.token, nil, &hostSession); code != http.StatusCreated {
		t.Fatalf("host connect: status %d", code)
	}
	if hostSession.VirtualIP != "10.69.0.1" || len(hostSession.Peers) != 0 {
		t.Fatalf("host session = %+v", hostSession)
	}

	// The host publishes where it can be reached, so the guest learns it.
	if code := call(t, h, http.MethodPut, "/api/sessions/"+hostSession.SessionID+"/endpoint", host.token,
		papi.Endpoint{Protocol: "udp", Address: "203.0.113.50", Port: 51821}, nil); code != http.StatusNoContent {
		t.Fatalf("publish endpoint: status %d", code)
	}

	var guestSession papi.CreateSessionResponse
	if code := call(t, h, http.MethodPost, sessionPath, guest.token, nil, &guestSession); code != http.StatusCreated {
		t.Fatalf("guest connect: status %d", code)
	}
	if guestSession.VirtualIP != "10.69.0.2" {
		t.Errorf("guest ip = %q, want 10.69.0.2", guestSession.VirtualIP)
	}
	if len(guestSession.Peers) != 1 {
		t.Fatalf("guest peers = %+v", guestSession.Peers)
	}

	peer := guestSession.Peers[0]
	if peer.DeviceID != host.id || peer.Nickname != "Tiago" || peer.VirtualIP != "10.69.0.1" {
		t.Errorf("peer = %+v", peer)
	}
	if peer.TransportKey == "" {
		t.Error("peer has no transport key, so the guest cannot authenticate it")
	}
	if peer.Endpoint == nil || peer.Endpoint.Port != 51821 {
		t.Errorf("peer endpoint = %+v", peer.Endpoint)
	}

	if code := call(t, h, http.MethodPost, "/api/sessions/"+guestSession.SessionID+"/heartbeat", guest.token, nil, nil); code != http.StatusNoContent {
		t.Errorf("heartbeat: status %d", code)
	}

	// Disconnecting frees the address for whoever connects next.
	if code := call(t, h, http.MethodDelete, "/api/sessions/"+guestSession.SessionID, guest.token, nil, nil); code != http.StatusNoContent {
		t.Fatalf("disconnect: status %d", code)
	}
	var afterHost papi.CreateSessionResponse
	if code := call(t, h, http.MethodPost, sessionPath, host.token, nil, &afterHost); code != http.StatusCreated {
		t.Fatalf("host reconnect: status %d", code)
	}
	if len(afterHost.Peers) != 0 {
		t.Errorf("a disconnected peer is still listed: %+v", afterHost.Peers)
	}
}

func TestJoiningWithTheWrongPasswordLooksLikeAMissingGroup(t *testing.T) {
	h := newTestServer(t)
	host := register(t, h, "dev_host")
	guest := register(t, h, "dev_guest")

	if code := call(t, h, http.MethodPost, "/api/groups", host.token, papi.CreateGroupRequest{
		Name: "Friday Night", PasswordProof: auth.DeriveGroupProof("hunter2", "Friday Night"), Nickname: "Tiago",
	}, nil); code != http.StatusCreated {
		t.Fatalf("create group: status %d", code)
	}

	// A wrong password and a group that does not exist have to be told apart by
	// nobody, or this endpoint becomes a way to enumerate groups.
	var wrongPassword, missingGroup papi.Error
	wrongCode := call(t, h, http.MethodPost, "/api/groups/join", guest.token, papi.JoinGroupRequest{
		Group: "Friday Night", PasswordProof: auth.DeriveGroupProof("wrong", "Friday Night"), Nickname: "João",
	}, &wrongPassword)
	missingCode := call(t, h, http.MethodPost, "/api/groups/join", guest.token, papi.JoinGroupRequest{
		Group: "No Such Group", PasswordProof: auth.DeriveGroupProof("hunter2", "No Such Group"), Nickname: "João",
	}, &missingGroup)

	if wrongCode != missingCode {
		t.Errorf("status %d for a wrong password and %d for a missing group", wrongCode, missingCode)
	}
	if wrongPassword != missingGroup {
		t.Errorf("body %+v for a wrong password and %+v for a missing group", wrongPassword, missingGroup)
	}
}

func TestDuplicateGroupNamesAreRejected(t *testing.T) {
	h := newTestServer(t)
	host := register(t, h, "dev_host")

	req := papi.CreateGroupRequest{
		Name: "Friday Night", PasswordProof: auth.DeriveGroupProof("hunter2", "Friday Night"), Nickname: "Tiago",
	}
	if code := call(t, h, http.MethodPost, "/api/groups", host.token, req, nil); code != http.StatusCreated {
		t.Fatalf("create group: status %d", code)
	}

	var body papi.Error
	if code := call(t, h, http.MethodPost, "/api/groups", host.token, req, &body); code != http.StatusConflict {
		t.Errorf("duplicate name: status %d, want 409", code)
	}
	if body.Code != papi.ErrGroupNameTaken {
		t.Errorf("code = %q, want %q", body.Code, papi.ErrGroupNameTaken)
	}
}

func TestSessionsBelongToOneDevice(t *testing.T) {
	h := newTestServer(t)
	host := register(t, h, "dev_host")
	stranger := register(t, h, "dev_stranger")

	var group papi.Membership
	if code := call(t, h, http.MethodPost, "/api/groups", host.token, papi.CreateGroupRequest{
		Name: "Friday Night", PasswordProof: auth.DeriveGroupProof("hunter2", "Friday Night"), Nickname: "Tiago",
	}, &group); code != http.StatusCreated {
		t.Fatalf("create group: status %d", code)
	}

	var sess papi.CreateSessionResponse
	if code := call(t, h, http.MethodPost, "/api/groups/"+group.GroupID+"/sessions", host.token, nil, &sess); code != http.StatusCreated {
		t.Fatalf("connect: status %d", code)
	}

	// Holding somebody else's session ID gets you nothing.
	if code := call(t, h, http.MethodDelete, "/api/sessions/"+sess.SessionID, stranger.token, nil, nil); code != http.StatusNotFound {
		t.Errorf("deleting another device's session: status %d, want 404", code)
	}
	if code := call(t, h, http.MethodPut, "/api/sessions/"+sess.SessionID+"/endpoint", stranger.token,
		papi.Endpoint{Address: "203.0.113.9", Port: 1234}, nil); code != http.StatusNotFound {
		t.Errorf("publishing to another device's session: status %d, want 404", code)
	}
}

func TestConnectingWithoutMembershipIsRefused(t *testing.T) {
	h := newTestServer(t)
	host := register(t, h, "dev_host")
	stranger := register(t, h, "dev_stranger")

	var group papi.Membership
	if code := call(t, h, http.MethodPost, "/api/groups", host.token, papi.CreateGroupRequest{
		Name: "Friday Night", PasswordProof: auth.DeriveGroupProof("hunter2", "Friday Night"), Nickname: "Tiago",
	}, &group); code != http.StatusCreated {
		t.Fatalf("create group: status %d", code)
	}

	path := "/api/groups/" + group.GroupID + "/sessions"
	if code := call(t, h, http.MethodPost, path, stranger.token, nil, nil); code != http.StatusNotFound {
		t.Errorf("connecting to a group you are not in: status %d, want 404", code)
	}

	// And a member who leaves cannot connect any more.
	if code := call(t, h, http.MethodDelete, "/api/groups/"+group.GroupID+"/membership", host.token, nil, nil); code != http.StatusNoContent {
		t.Fatalf("leave group: status %d", code)
	}
	if code := call(t, h, http.MethodPost, path, host.token, nil, nil); code != http.StatusNotFound {
		t.Errorf("connecting after leaving: status %d, want 404", code)
	}
}

func TestEndpointValidation(t *testing.T) {
	h := newTestServer(t)
	host := register(t, h, "dev_host")

	var group papi.Membership
	if code := call(t, h, http.MethodPost, "/api/groups", host.token, papi.CreateGroupRequest{
		Name: "Friday Night", PasswordProof: auth.DeriveGroupProof("hunter2", "Friday Night"), Nickname: "Tiago",
	}, &group); code != http.StatusCreated {
		t.Fatalf("create group: status %d", code)
	}
	var sess papi.CreateSessionResponse
	if code := call(t, h, http.MethodPost, "/api/groups/"+group.GroupID+"/sessions", host.token, nil, &sess); code != http.StatusCreated {
		t.Fatalf("connect: status %d", code)
	}

	path := "/api/sessions/" + sess.SessionID + "/endpoint"
	for _, endpoint := range []papi.Endpoint{
		{Protocol: "udp", Address: "not-an-address", Port: 51821},
		{Protocol: "udp", Address: "203.0.113.50", Port: 0},
		{Protocol: "udp", Address: "203.0.113.50", Port: 70000},
		{Protocol: "tcp", Address: "203.0.113.50", Port: 51821},
	} {
		if code := call(t, h, http.MethodPut, path, host.token, endpoint, nil); code != http.StatusBadRequest {
			t.Errorf("endpoint %+v: status %d, want 400", endpoint, code)
		}
	}
}

func TestJoinAttemptsAreRateLimited(t *testing.T) {
	h := newTestServer(t)
	guest := register(t, h, "dev_guest")

	req := papi.JoinGroupRequest{
		Group: "No Such Group", PasswordProof: "proof", Nickname: "João",
	}

	var limited bool
	for range 20 {
		if call(t, h, http.MethodPost, "/api/groups/join", guest.token, req, nil) == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("20 wrong password attempts in a row were all allowed")
	}
}

func TestDiscoveryDocument(t *testing.T) {
	h := newTestServer(t)

	var doc papi.Discovery
	if code := call(t, h, http.MethodGet, protocol.WellKnownPath, "", nil, &doc); code != http.StatusOK {
		t.Fatalf("discovery: status %d", code)
	}

	if doc.Version != protocol.DiscoveryVersion {
		t.Errorf("version = %d, want %d", doc.Version, protocol.DiscoveryVersion)
	}
	if doc.API != "https://api.192168.lol/api" {
		t.Errorf("api = %q", doc.API)
	}
	if doc.Realtime != "wss://api.192168.lol/realtime" {
		t.Errorf("realtime = %q", doc.Realtime)
	}
	if len(doc.STUN) != 1 || doc.STUN[0] != "stun:stun.example.com:3478" {
		t.Errorf("stun = %v", doc.STUN)
	}
	// Nothing may advertise a feature this deployment cannot do.
	if doc.Features.Relay || doc.Features.PeerRouting || doc.Relay != nil {
		t.Errorf("features = %+v, relay = %v", doc.Features, doc.Relay)
	}
}

// Members lists everyone in the group, not only the people connected to it, so
// the app can show a group of six as six rather than as whoever is awake.
func TestMembersListsEveryoneAndSaysWhoIsOnline(t *testing.T) {
	h := newTestServer(t)
	host := register(t, h, "dev_host")
	guest := register(t, h, "dev_guest")
	absent := register(t, h, "dev_absent")

	proof := auth.DeriveGroupProof("hunter2", "Friday Night")

	var created papi.Membership
	if code := call(t, h, http.MethodPost, "/api/groups", host.token, papi.CreateGroupRequest{
		Name: "Friday Night", PasswordProof: proof, Nickname: "Tiago",
	}, &created); code != http.StatusCreated {
		t.Fatalf("create group: status %d", code)
	}

	for _, joiner := range []struct {
		device   device
		nickname string
	}{{guest, "Joao"}, {absent, "Pedro"}} {
		if code := call(t, h, http.MethodPost, "/api/groups/join", joiner.device.token, papi.JoinGroupRequest{
			Group: "Friday Night", PasswordProof: proof, Nickname: joiner.nickname,
		}, nil); code != http.StatusOK {
			t.Fatalf("join as %s: status %d", joiner.nickname, code)
		}
	}

	// Two of the three connect. The third belongs and is not here.
	sessions := "/api/groups/" + created.GroupID + "/sessions"
	for _, connecting := range []device{host, guest} {
		if code := call(t, h, http.MethodPost, sessions, connecting.token, nil, nil); code != http.StatusCreated {
			t.Fatalf("connect: status %d", code)
		}
	}

	var members papi.MembersResponse
	if code := call(t, h, http.MethodGet, "/api/groups/"+created.GroupID+"/members", guest.token, nil, &members); code != http.StatusOK {
		t.Fatalf("list members: status %d", code)
	}
	if len(members.Members) != 3 {
		t.Fatalf("members = %+v, want 3", members.Members)
	}

	online := map[string]bool{}
	addresses := map[string]string{}
	for _, member := range members.Members {
		online[member.Nickname] = member.Online
		addresses[member.Nickname] = member.VirtualIP
	}
	if !online["Tiago"] || !online["Joao"] || online["Pedro"] {
		t.Fatalf("online = %+v, want Tiago and Joao here and Pedro away", online)
	}
	// Pedro is away and still has an address. It is his whether he is here or
	// not, and the list says so.
	if addresses["Tiago"] != "10.69.0.1" || addresses["Joao"] != "10.69.0.2" || addresses["Pedro"] != "10.69.0.3" {
		t.Errorf("addresses = %+v", addresses)
	}
}

// Who is in a group is the business of the people in it.
func TestMembersNeedsMembership(t *testing.T) {
	h := newTestServer(t)
	host := register(t, h, "dev_host")
	stranger := register(t, h, "dev_stranger")

	var created papi.Membership
	if code := call(t, h, http.MethodPost, "/api/groups", host.token, papi.CreateGroupRequest{
		Name: "Friday Night", PasswordProof: auth.DeriveGroupProof("hunter2", "Friday Night"), Nickname: "Tiago",
	}, &created); code != http.StatusCreated {
		t.Fatalf("create group: status %d", code)
	}

	if code := call(t, h, http.MethodGet, "/api/groups/"+created.GroupID+"/members", stranger.token, nil, nil); code != http.StatusNotFound {
		t.Fatalf("stranger listing members: status %d, want 404", code)
	}
}

// setUpGroup makes a group with an owner and one other member.
func setUpGroup(t *testing.T, h *Server) (owner, member device, groupID, proof string) {
	t.Helper()

	owner = register(t, h, "dev_owner")
	member = register(t, h, "dev_member")
	proof = auth.DeriveGroupProof("hunter2", "Friday Night")

	var created papi.Membership
	if code := call(t, h, http.MethodPost, "/api/groups", owner.token, papi.CreateGroupRequest{
		Name: "Friday Night", PasswordProof: proof, Nickname: "Tiago",
	}, &created); code != http.StatusCreated {
		t.Fatalf("create group: status %d", code)
	}
	if created.Role != "owner" {
		t.Fatalf("creator role = %q, want owner", created.Role)
	}

	if code := call(t, h, http.MethodPost, "/api/groups/join", member.token, papi.JoinGroupRequest{
		Group: "Friday Night", PasswordProof: proof, Nickname: "Joao",
	}, nil); code != http.StatusOK {
		t.Fatalf("join: status %d", code)
	}
	return owner, member, created.GroupID, proof
}

// Removing somebody has to keep them out. They still know the name and the
// password, so if joining again undid it then removing anybody would mean
// nothing at all.
func TestARemovedMemberCannotJoinAgain(t *testing.T) {
	h := newTestServer(t)
	owner, member, groupID, proof := setUpGroup(t, h)

	if code := call(t, h, http.MethodDelete, "/api/groups/"+groupID+"/members/dev_member", owner.token, nil, nil); code != http.StatusNoContent {
		t.Fatalf("remove member: status %d", code)
	}
	_ = member

	var members papi.MembersResponse
	if code := call(t, h, http.MethodGet, "/api/groups/"+groupID+"/members", owner.token, nil, &members); code != http.StatusOK {
		t.Fatalf("list members: status %d", code)
	}
	if len(members.Members) != 1 {
		t.Fatalf("members = %+v, want only the owner", members.Members)
	}

	// And the answer is the one a wrong password gets, so being removed cannot
	// be told apart from never having been let in.
	var removed papi.Error
	code := call(t, h, http.MethodPost, "/api/groups/join", member.token, papi.JoinGroupRequest{
		Group: "Friday Night", PasswordProof: proof, Nickname: "Joao",
	}, &removed)
	if code != http.StatusForbidden || removed.Code != papi.ErrInvalidPassword {
		t.Fatalf("rejoin after removal: status %d code %q, want 403 %s", code, removed.Code, papi.ErrInvalidPassword)
	}

	// Leaving is a different thing and does come back.
	other := register(t, h, "dev_other")
	if code := call(t, h, http.MethodPost, "/api/groups/join", other.token, papi.JoinGroupRequest{
		Group: "Friday Night", PasswordProof: proof, Nickname: "Pedro",
	}, nil); code != http.StatusOK {
		t.Fatalf("join: status %d", code)
	}
	if code := call(t, h, http.MethodDelete, "/api/groups/"+groupID+"/membership", other.token, nil, nil); code != http.StatusNoContent {
		t.Fatalf("leave: status %d", code)
	}
	if code := call(t, h, http.MethodPost, "/api/groups/join", other.token, papi.JoinGroupRequest{
		Group: "Friday Night", PasswordProof: proof, Nickname: "Pedro",
	}, nil); code != http.StatusOK {
		t.Fatalf("rejoin after leaving: status %d, want 200", code)
	}
}

// Everything that changes a group is the owner's alone.
func TestOnlyTheOwnerCanManageAGroup(t *testing.T) {
	h := newTestServer(t)
	owner, member, groupID, _ := setUpGroup(t, h)

	for _, attempt := range []struct {
		what   string
		method string
		path   string
		body   any
	}{
		{"remove a member", http.MethodDelete, "/api/groups/" + groupID + "/members/dev_owner", nil},
		{"rename", http.MethodPut, "/api/groups/" + groupID + "/name", map[string]string{"name": "Theirs Now"}},
		{"change the look", http.MethodPut, "/api/groups/" + groupID + "/appearance", papi.SetGroupAppearanceRequest{Icon: "star", Color: "pink"}},
		{"change the password", http.MethodPut, "/api/groups/" + groupID + "/password", map[string]string{"passwordProof": auth.DeriveGroupProof("x", "y")}},
		{"take ownership", http.MethodPut, "/api/groups/" + groupID + "/owner/dev_member", nil},
	} {
		if code := call(t, h, attempt.method, attempt.path, member.token, attempt.body, nil); code != http.StatusForbidden {
			t.Errorf("member tried to %s: status %d, want 403", attempt.what, code)
		}
	}

	// The owner can, and the group is called something else afterwards.
	if code := call(t, h, http.MethodPut, "/api/groups/"+groupID+"/name", owner.token,
		map[string]string{"name": "Saturday Night"}, nil); code != http.StatusNoContent {
		t.Fatalf("owner rename: status %d", code)
	}
	var groups []papi.Membership
	if code := call(t, h, http.MethodGet, "/api/groups", owner.token, nil, &groups); code != http.StatusOK {
		t.Fatalf("list groups: status %d", code)
	}
	if len(groups) != 1 || groups[0].GroupName != "Saturday Night" {
		t.Fatalf("groups = %+v", groups)
	}
}

// The look belongs to the group rather than to whoever picked it, so everybody
// in it sees the same one.
func TestTheGroupsLookReachesEveryMember(t *testing.T) {
	h := newTestServer(t)
	owner, member, groupID, _ := setUpGroup(t, h)

	if code := call(t, h, http.MethodPut, "/api/groups/"+groupID+"/appearance", owner.token,
		papi.SetGroupAppearanceRequest{Icon: "game", Color: "green"}, nil); code != http.StatusNoContent {
		t.Fatalf("set appearance: status %d", code)
	}

	var groups []papi.Membership
	if code := call(t, h, http.MethodGet, "/api/groups", member.token, nil, &groups); code != http.StatusOK {
		t.Fatalf("list groups: status %d", code)
	}
	if len(groups) != 1 || groups[0].GroupIcon != "game" || groups[0].GroupColor != "green" {
		t.Fatalf("groups = %+v", groups)
	}

	// The server checks the shape of a key, not its meaning.
	if code := call(t, h, http.MethodPut, "/api/groups/"+groupID+"/appearance", owner.token,
		papi.SetGroupAppearanceRequest{Icon: "<script>", Color: "green"}, nil); code != http.StatusBadRequest {
		t.Fatalf("set a nonsense icon: status %d, want 400", code)
	}
}

// A group is made with its look rather than given one straight afterwards, so
// it is never briefly something other than what its maker picked.
func TestAGroupKeepsTheLookItWasMadeWith(t *testing.T) {
	h := newTestServer(t)
	owner := register(t, h, "dev_owner")

	if code := call(t, h, http.MethodPost, "/api/groups", owner.token, papi.CreateGroupRequest{
		Name:          "Sunday",
		PasswordProof: auth.DeriveGroupProof("hunter2", "Sunday"),
		Nickname:      "Tiago",
		Icon:          "flag",
		Color:         "orange",
	}, nil); code != http.StatusCreated {
		t.Fatalf("create: status %d", code)
	}

	var groups []papi.Membership
	if code := call(t, h, http.MethodGet, "/api/groups", owner.token, nil, &groups); code != http.StatusOK {
		t.Fatalf("list groups: status %d", code)
	}
	if len(groups) != 1 || groups[0].GroupIcon != "flag" || groups[0].GroupColor != "orange" {
		t.Fatalf("groups = %+v", groups)
	}
}

// A group whose owner loses their machine would otherwise be unmanageable for
// good, because a device identity does not survive a reinstall.
func TestOwnershipCanBeHandedOver(t *testing.T) {
	h := newTestServer(t)
	owner, member, groupID, _ := setUpGroup(t, h)

	if code := call(t, h, http.MethodPut, "/api/groups/"+groupID+"/owner/dev_member", owner.token, nil, nil); code != http.StatusNoContent {
		t.Fatalf("transfer: status %d", code)
	}

	// The new owner can manage it, and the old one cannot.
	if code := call(t, h, http.MethodPut, "/api/groups/"+groupID+"/name", member.token,
		map[string]string{"name": "Mine Now"}, nil); code != http.StatusNoContent {
		t.Fatalf("new owner rename: status %d", code)
	}
	if code := call(t, h, http.MethodPut, "/api/groups/"+groupID+"/name", owner.token,
		map[string]string{"name": "No"}, nil); code != http.StatusForbidden {
		t.Fatalf("old owner rename: status %d, want 403", code)
	}
}

// The password is checked at the door and nowhere else, so changing it stops
// the next person joining and removes nobody already inside.
func TestChangingThePasswordKeepsEveryoneAndStopsTheOldOne(t *testing.T) {
	h := newTestServer(t)
	owner, member, groupID, oldProof := setUpGroup(t, h)

	newProof := auth.DeriveGroupProof("correcthorse", "Friday Night")
	if code := call(t, h, http.MethodPut, "/api/groups/"+groupID+"/password", owner.token,
		map[string]string{"passwordProof": newProof}, nil); code != http.StatusNoContent {
		t.Fatalf("change password: status %d", code)
	}

	// Still a member, and can still connect.
	if code := call(t, h, http.MethodPost, "/api/groups/"+groupID+"/sessions", member.token, nil, nil); code != http.StatusCreated {
		t.Fatalf("member connect after password change: status %d", code)
	}

	stranger := register(t, h, "dev_stranger")
	if code := call(t, h, http.MethodPost, "/api/groups/join", stranger.token, papi.JoinGroupRequest{
		Group: "Friday Night", PasswordProof: oldProof, Nickname: "Nope",
	}, nil); code != http.StatusForbidden {
		t.Fatalf("join with the old password: status %d, want 403", code)
	}
	if code := call(t, h, http.MethodPost, "/api/groups/join", stranger.token, papi.JoinGroupRequest{
		Group: "Friday Night", PasswordProof: newProof, Nickname: "Yes",
	}, nil); code != http.StatusOK {
		t.Fatalf("join with the new password: status %d", code)
	}
}

// Deleting a group takes it away from everyone in it, so it is the owner's
// alone and it takes the memberships and sessions with it.
func TestDeletingAGroupTakesEverythingWithIt(t *testing.T) {
	h := newTestServer(t)
	owner, member, groupID, proof := setUpGroup(t, h)

	if code := call(t, h, http.MethodPost, "/api/groups/"+groupID+"/sessions", member.token, nil, nil); code != http.StatusCreated {
		t.Fatalf("member connect: status %d", code)
	}

	if code := call(t, h, http.MethodDelete, "/api/groups/"+groupID, member.token, nil, nil); code != http.StatusForbidden {
		t.Fatalf("member deleting: status %d, want 403", code)
	}
	if code := call(t, h, http.MethodDelete, "/api/groups/"+groupID, owner.token, nil, nil); code != http.StatusNoContent {
		t.Fatalf("owner deleting: status %d", code)
	}

	// Gone from everybody's list, not only the owner's.
	for _, who := range []struct {
		name   string
		device device
	}{{"owner", owner}, {"member", member}} {
		var groups []papi.Membership
		if code := call(t, h, http.MethodGet, "/api/groups", who.device.token, nil, &groups); code != http.StatusOK {
			t.Fatalf("%s listing groups: status %d", who.name, code)
		}
		if len(groups) != 0 {
			t.Errorf("%s still has %+v", who.name, groups)
		}
	}

	// And the name is free again, rather than held by a group nobody can reach.
	if code := call(t, h, http.MethodPost, "/api/groups", member.token, papi.CreateGroupRequest{
		Name: "Friday Night", PasswordProof: proof, Nickname: "Joao",
	}, nil); code != http.StatusCreated {
		t.Fatalf("reusing the name: status %d, want 201", code)
	}
}
