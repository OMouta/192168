package storage

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func newDevice(t *testing.T, s *Store, id string) Device {
	t.Helper()
	d := Device{
		ID:           id,
		PublicKey:    "pub-" + id,
		TransportKey: "transport-" + id,
		Name:         id + "-PC",
		Nickname:     id + "-PC",
	}
	if _, err := s.RegisterDevice(t.Context(), d); err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}
	return d
}

func newGroup(t *testing.T, s *Store, creator, name string) (Group, Membership) {
	t.Helper()
	id, err := NewID("grp")
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	g := Group{ID: id, Name: name, PasswordVerifier: "verifier", Subnet: "10.69.0.0/24"}
	m, err := s.CreateGroup(t.Context(), g, name, creator)
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	return g, m
}

func TestRegisterDeviceIssuesAUsableToken(t *testing.T) {
	s := newStore(t)

	token, err := s.RegisterDevice(t.Context(), Device{
		ID: "dev_1", PublicKey: "pub", TransportKey: "transport", Name: "TIAGO-PC",
	})
	if err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}

	got, err := s.DeviceByToken(t.Context(), token)
	if err != nil {
		t.Fatalf("DeviceByToken: %v", err)
	}
	if got.ID != "dev_1" || got.TransportKey != "transport" {
		t.Errorf("device = %+v", got)
	}

	if _, err := s.DeviceByToken(t.Context(), "not-a-token"); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeviceByToken with junk err = %v, want ErrNotFound", err)
	}
}

func TestRegisteringTwiceKeepsTheDeviceAndAddsAToken(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	first, err := s.RegisterDevice(ctx, Device{ID: "dev_1", PublicKey: "pub", TransportKey: "t1", Name: "PC"})
	if err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}
	// A reinstall keeps the same device ID and rotates its keys.
	second, err := s.RegisterDevice(ctx, Device{ID: "dev_1", PublicKey: "pub", TransportKey: "t2", Name: "PC"})
	if err != nil {
		t.Fatalf("RegisterDevice again: %v", err)
	}

	for _, token := range []string{first, second} {
		d, err := s.DeviceByToken(ctx, token)
		if err != nil {
			t.Fatalf("DeviceByToken: %v", err)
		}
		if d.TransportKey != "t2" {
			t.Errorf("transport key = %q, want the newer one", d.TransportKey)
		}
	}
}

func TestClaimRegisterNonce(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	ok, err := s.ClaimRegisterNonce(ctx, "nonce-1", time.Minute)
	if err != nil {
		t.Fatalf("ClaimRegisterNonce: %v", err)
	}
	if !ok {
		t.Fatal("a fresh nonce was refused")
	}

	ok, err = s.ClaimRegisterNonce(ctx, "nonce-1", time.Minute)
	if err != nil {
		t.Fatalf("ClaimRegisterNonce: %v", err)
	}
	if ok {
		t.Error("a replayed nonce was accepted")
	}

	// Once a nonce is older than the window it cannot be replayed anyway, so
	// dropping it is safe and keeps the table small.
	ok, err = s.ClaimRegisterNonce(ctx, "nonce-1", 0)
	if err != nil {
		t.Fatalf("ClaimRegisterNonce: %v", err)
	}
	if !ok {
		t.Error("a pruned nonce was still blocking")
	}
}

func TestCreateGroupMakesTheCreatorAMember(t *testing.T) {
	s := newStore(t)
	newDevice(t, s, "dev_1")

	_, m := newGroup(t, s, "dev_1", "friday night")
	if m.Nickname != "dev_1-PC" || m.Subnet != "10.69.0.0/24" {
		t.Errorf("membership = %+v", m)
	}

	memberships, err := s.MembershipsByDevice(t.Context(), "dev_1")
	if err != nil {
		t.Fatalf("MembershipsByDevice: %v", err)
	}
	if len(memberships) != 1 || memberships[0].GroupName != "friday night" {
		t.Errorf("memberships = %+v", memberships)
	}
}

func TestGroupNamesAreUnique(t *testing.T) {
	s := newStore(t)
	newDevice(t, s, "dev_1")
	newGroup(t, s, "dev_1", "friday night")

	id, err := NewID("grp")
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	_, err = s.CreateGroup(t.Context(), Group{
		ID: id, Name: "Friday Night", PasswordVerifier: "v", Subnet: "10.69.0.0/24",
	}, "friday night", "dev_1")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("CreateGroup err = %v, want ErrConflict", err)
	}
}

func TestRejoiningClearsRevocation(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	newDevice(t, s, "dev_1")
	newDevice(t, s, "dev_2")
	g, _ := newGroup(t, s, "dev_1", "friday night")

	if _, err := s.AddMembership(ctx, g, "dev_2"); err != nil {
		t.Fatalf("AddMembership: %v", err)
	}
	if err := s.RevokeMembership(ctx, g.ID, "dev_2"); err != nil {
		t.Fatalf("RevokeMembership: %v", err)
	}
	if _, err := s.Membership(ctx, g.ID, "dev_2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Membership after revoke err = %v, want ErrNotFound", err)
	}

	if _, err := s.AddMembership(ctx, g, "dev_2"); err != nil {
		t.Fatalf("AddMembership after revoke: %v", err)
	}
	if _, err := s.Membership(ctx, g.ID, "dev_2"); err != nil {
		t.Errorf("Membership after rejoin: %v", err)
	}
}

func TestMembersGetTheLowestFreeAddress(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	newDevice(t, s, "dev_1")
	newDevice(t, s, "dev_2")
	newDevice(t, s, "dev_3")
	g, first := newGroup(t, s, "dev_1", "friday night")

	second, err := s.AddMembership(ctx, g, "dev_2")
	if err != nil {
		t.Fatalf("AddMembership: %v", err)
	}
	third, err := s.AddMembership(ctx, g, "dev_3")
	if err != nil {
		t.Fatalf("AddMembership: %v", err)
	}

	want := []string{"10.69.0.1", "10.69.0.2", "10.69.0.3"}
	for i, m := range []Membership{first, second, third} {
		if m.VirtualIP != want[i] {
			t.Errorf("member %d got %q, want %q", i, m.VirtualIP, want[i])
		}
	}
}

// The whole point of the address living on the membership. Somebody who hosts
// tonight is at the same address tomorrow, whoever else connected first.
func TestAnAddressSurvivesDisconnecting(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	newDevice(t, s, "dev_1")
	newDevice(t, s, "dev_2")
	g, host := newGroup(t, s, "dev_1", "friday night")

	guest, err := s.AddMembership(ctx, g, "dev_2")
	if err != nil {
		t.Fatalf("AddMembership: %v", err)
	}

	hosting, err := s.CreateSession(ctx, host)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if hosting.VirtualIP != "10.69.0.1" {
		t.Fatalf("host got %q, want 10.69.0.1", hosting.VirtualIP)
	}
	if err := s.DeleteSession(ctx, hosting.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	// The next day, and the guest is in first. That used to take .1.
	if _, err := s.CreateSession(ctx, guest); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	again, err := s.CreateSession(ctx, host)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if again.VirtualIP != "10.69.0.1" {
		t.Errorf("host came back at %q, want 10.69.0.1", again.VirtualIP)
	}
}

func TestLeavingFreesAnAddressAndRejoiningTakesItBack(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	newDevice(t, s, "dev_1")
	newDevice(t, s, "dev_2")
	newDevice(t, s, "dev_3")
	g, _ := newGroup(t, s, "dev_1", "friday night")

	left, err := s.AddMembership(ctx, g, "dev_2")
	if err != nil {
		t.Fatalf("AddMembership: %v", err)
	}
	if err := s.RevokeMembership(ctx, g.ID, "dev_2"); err != nil {
		t.Fatalf("RevokeMembership: %v", err)
	}

	// Nobody took it while they were away, so it is theirs again.
	back, err := s.AddMembership(ctx, g, "dev_2")
	if err != nil {
		t.Fatalf("AddMembership after leaving: %v", err)
	}
	if back.VirtualIP != left.VirtualIP {
		t.Errorf("rejoined at %q, want the old %q", back.VirtualIP, left.VirtualIP)
	}

	// Somebody who leaves while a newcomer takes their address gets another
	// one rather than a collision.
	if err := s.RevokeMembership(ctx, g.ID, "dev_2"); err != nil {
		t.Fatalf("RevokeMembership: %v", err)
	}
	newcomer, err := s.AddMembership(ctx, g, "dev_3")
	if err != nil {
		t.Fatalf("AddMembership: %v", err)
	}
	if newcomer.VirtualIP != left.VirtualIP {
		t.Fatalf("newcomer got %q, want the freed %q", newcomer.VirtualIP, left.VirtualIP)
	}
	moved, err := s.AddMembership(ctx, g, "dev_2")
	if err != nil {
		t.Fatalf("AddMembership after losing an address: %v", err)
	}
	if moved.VirtualIP == left.VirtualIP || moved.VirtualIP == "" {
		t.Errorf("rejoined at %q, want an address of their own", moved.VirtualIP)
	}
}

func TestJoiningAFullGroupFails(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	newDevice(t, s, "dev_1")
	newDevice(t, s, "dev_2")

	id, err := NewID("grp")
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	// A /30 holds two hosts, and the creator takes one of them.
	g := Group{ID: id, Name: "tiny", PasswordVerifier: "verifier", Subnet: "10.69.0.0/30"}
	if _, err := s.CreateGroup(ctx, g, "tiny", "dev_1"); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if _, err := s.AddMembership(ctx, g, "dev_2"); err != nil {
		t.Fatalf("AddMembership: %v", err)
	}

	newDevice(t, s, "dev_3")
	if _, err := s.AddMembership(ctx, g, "dev_3"); !errors.Is(err, ErrGroupFull) {
		t.Errorf("AddMembership to a full group err = %v, want ErrGroupFull", err)
	}
}

func TestReconnectingReplacesTheOldSession(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	newDevice(t, s, "dev_1")
	_, m := newGroup(t, s, "dev_1", "friday night")

	first, err := s.CreateSession(ctx, m)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	second, err := s.CreateSession(ctx, m)
	if err != nil {
		t.Fatalf("CreateSession again: %v", err)
	}

	if _, err := s.SessionByID(ctx, first.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("the first session survived a reconnect, err = %v", err)
	}
	if _, err := s.SessionByID(ctx, second.ID); err != nil {
		t.Errorf("SessionByID: %v", err)
	}
}

func TestPeersInGroupLeavesOutTheCaller(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	newDevice(t, s, "dev_1")
	newDevice(t, s, "dev_2")
	g, mine := newGroup(t, s, "dev_1", "friday night")

	theirs, err := s.AddMembership(ctx, g, "dev_2")
	if err != nil {
		t.Fatalf("AddMembership: %v", err)
	}
	if _, err := s.CreateSession(ctx, mine); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	theirSession, err := s.CreateSession(ctx, theirs)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	peers, err := s.PeersInGroup(ctx, g.ID, "dev_1")
	if err != nil {
		t.Fatalf("PeersInGroup: %v", err)
	}
	if len(peers) != 1 || peers[0].DeviceID != "dev_2" {
		t.Fatalf("peers = %+v", peers)
	}
	// Nobody has published an endpoint yet, which is normal right after
	// connecting and has to read as online but unreachable.
	if peers[0].Endpoint != nil {
		t.Errorf("endpoint = %+v, want none", peers[0].Endpoint)
	}
	if peers[0].TransportKey != "transport-dev_2" {
		t.Errorf("transport key = %q", peers[0].TransportKey)
	}

	if err := s.SetSessionEndpoint(ctx, theirSession.ID, "198.51.100.20", 44120); err != nil {
		t.Fatalf("SetSessionEndpoint: %v", err)
	}
	peers, err = s.PeersInGroup(ctx, g.ID, "dev_1")
	if err != nil {
		t.Fatalf("PeersInGroup: %v", err)
	}
	if peers[0].Endpoint == nil || peers[0].Endpoint.Port != 44120 {
		t.Errorf("endpoint = %+v", peers[0].Endpoint)
	}
}

// The count is what the group list shows to say which groups are worth joining
// right now, so it has to follow sessions rather than membership.
func TestMembershipsCountWhoIsConnected(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	newDevice(t, s, "dev_1")
	newDevice(t, s, "dev_2")
	g, mine := newGroup(t, s, "dev_1", "friday night")

	theirs, err := s.AddMembership(ctx, g, "dev_2")
	if err != nil {
		t.Fatalf("AddMembership: %v", err)
	}

	if got := onlineIn(t, s, "dev_1", g.ID); got != 0 {
		t.Errorf("a group nobody has connected to counted %d online", got)
	}

	if _, err := s.CreateSession(ctx, mine); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	theirSession, err := s.CreateSession(ctx, theirs)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Counting yourself is deliberate. The list says how many are on the
	// network, and you would be one of them.
	if got := onlineIn(t, s, "dev_1", g.ID); got != 2 {
		t.Errorf("both connected counted %d online, want 2", got)
	}

	if err := s.DeleteSession(ctx, theirSession.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if got := onlineIn(t, s, "dev_1", g.ID); got != 1 {
		t.Errorf("after one left, counted %d online, want 1", got)
	}
}

// onlineIn reads one group's count out of a device's membership list.
func onlineIn(t *testing.T, s *Store, deviceID, groupID string) int {
	t.Helper()

	memberships, err := s.MembershipsByDevice(t.Context(), deviceID)
	if err != nil {
		t.Fatalf("MembershipsByDevice: %v", err)
	}
	for _, m := range memberships {
		if m.GroupID != groupID {
			continue
		}
		if m.OnlineMembers == nil {
			t.Fatalf("%s came back without a count", groupID)
		}
		return *m.OnlineMembers
	}
	t.Fatalf("%s is not in %s", deviceID, groupID)
	return 0
}

func TestRevokingAMembershipEndsItsSession(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	newDevice(t, s, "dev_1")
	newDevice(t, s, "dev_2")
	g, _ := newGroup(t, s, "dev_1", "friday night")

	m, err := s.AddMembership(ctx, g, "dev_2")
	if err != nil {
		t.Fatalf("AddMembership: %v", err)
	}
	sess, err := s.CreateSession(ctx, m)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := s.RevokeMembership(ctx, g.ID, "dev_2"); err != nil {
		t.Fatalf("RevokeMembership: %v", err)
	}
	if _, err := s.SessionByID(ctx, sess.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a revoked member kept its session, err = %v", err)
	}
}

func TestExpireSessions(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	newDevice(t, s, "dev_1")
	_, m := newGroup(t, s, "dev_1", "friday night")

	sess, err := s.CreateSession(ctx, m)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	stale, err := s.ExpireSessions(ctx, time.Hour)
	if err != nil {
		t.Fatalf("ExpireSessions: %v", err)
	}
	if len(stale) != 0 {
		t.Fatalf("a fresh session was expired: %+v", stale)
	}

	stale, err = s.ExpireSessions(ctx, 0)
	if err != nil {
		t.Fatalf("ExpireSessions: %v", err)
	}
	if len(stale) != 1 || stale[0].ID != sess.ID || stale[0].DeviceID != "dev_1" {
		t.Fatalf("stale = %+v", stale)
	}
	if _, err := s.SessionByID(ctx, sess.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("an expired session survived, err = %v", err)
	}
}

func TestFreeAddress(t *testing.T) {
	// The network and broadcast addresses are not usable hosts.
	ip, err := freeAddress("10.69.0.0/30", map[string]bool{"10.69.0.1": true})
	if err != nil {
		t.Fatalf("freeAddress: %v", err)
	}
	if ip != "10.69.0.2" {
		t.Errorf("ip = %q, want 10.69.0.2", ip)
	}

	_, err = freeAddress("10.69.0.0/30", map[string]bool{"10.69.0.1": true, "10.69.0.2": true})
	if !errors.Is(err, ErrGroupFull) {
		t.Errorf("freeAddress on a full subnet err = %v, want ErrGroupFull", err)
	}

	if _, err := freeAddress("not-a-subnet", nil); err == nil {
		t.Error("freeAddress accepted a malformed subnet")
	}
}

func TestNicknameFollowsTheDeviceIntoEveryGroup(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	newDevice(t, s, "dev_1")
	newDevice(t, s, "dev_2")

	friday, _ := newGroup(t, s, "dev_1", "friday night")
	beamng, _ := newGroup(t, s, "dev_1", "beamng")
	if _, err := s.AddMembership(ctx, friday, "dev_2"); err != nil {
		t.Fatalf("AddMembership: %v", err)
	}
	if _, err := s.AddMembership(ctx, beamng, "dev_2"); err != nil {
		t.Fatalf("AddMembership: %v", err)
	}

	if err := s.SetDeviceNickname(ctx, "dev_2", "joao"); err != nil {
		t.Fatalf("SetDeviceNickname: %v", err)
	}

	// One rename, and it is true in both groups rather than the one it was
	// asked for.
	for _, g := range []Group{friday, beamng} {
		members, err := s.Members(ctx, g.ID)
		if err != nil {
			t.Fatalf("Members: %v", err)
		}
		for _, member := range members {
			if member.DeviceID == "dev_2" && member.Nickname != "joao" {
				t.Errorf("in %s, dev_2 is %q, want joao", g.Name, member.Nickname)
			}
		}
	}

	memberships, err := s.MembershipsByDevice(ctx, "dev_2")
	if err != nil {
		t.Fatalf("MembershipsByDevice: %v", err)
	}
	if len(memberships) != 2 {
		t.Fatalf("memberships = %+v", memberships)
	}
	for _, m := range memberships {
		if m.Nickname != "joao" {
			t.Errorf("membership of %s carries %q, want joao", m.GroupName, m.Nickname)
		}
	}
}

func TestRegisteringAgainKeepsAChosenNickname(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	d := Device{ID: "dev_1", PublicKey: "pub", TransportKey: "t", Name: "TIAGO-PC", Nickname: "TIAGO-PC"}
	if _, err := s.RegisterDevice(ctx, d); err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}
	if err := s.SetDeviceNickname(ctx, "dev_1", "tiago"); err != nil {
		t.Fatalf("SetDeviceNickname: %v", err)
	}

	// A reinstall registers again and offers the machine name once more. It is
	// a fallback for a device with no name, not an instruction.
	token, err := s.RegisterDevice(ctx, d)
	if err != nil {
		t.Fatalf("RegisterDevice again: %v", err)
	}
	got, err := s.DeviceByToken(ctx, token)
	if err != nil {
		t.Fatalf("DeviceByToken: %v", err)
	}
	if got.Nickname != "tiago" {
		t.Errorf("nickname = %q, want tiago", got.Nickname)
	}
}

func TestConnectedGroupIDsIsWhereARenameHasToReach(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	newDevice(t, s, "dev_1")

	g, m := newGroup(t, s, "dev_1", "friday night")
	if _, err := s.CreateSession(ctx, m); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// A group this device belongs to but is not connected in has nobody to
	// tell.
	newGroup(t, s, "dev_1", "beamng")

	ids, err := s.ConnectedGroupIDs(ctx, "dev_1")
	if err != nil {
		t.Fatalf("ConnectedGroupIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != g.ID {
		t.Errorf("ids = %v, want just %s", ids, g.ID)
	}
}
