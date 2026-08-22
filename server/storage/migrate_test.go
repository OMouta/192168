package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// A database that predates invite codes and device nicknames has to come
// through the migrations with its groups, its members, and their addresses
// intact.
//
// Every other test starts from an empty file, which exercises the schema but
// never the data. This is the case that only happens once, on somebody's real
// server, where getting it wrong loses the thing the server is for.
func TestAnOlderDatabaseKeepsItsGroupsAndMembers(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "old.db")

	// Version 16 is the last schema before a nickname belonged to the device.
	// Built by running the migrations of the day rather than by writing the
	// schema out again, so this cannot drift from what was actually shipped.
	seedOldDatabase(t, ctx, path, 16)

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open an older database: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	// The group survived, with its look, and it has a code it never had before.
	group, err := s.GroupByID(ctx, "grp_1")
	if err != nil {
		t.Fatalf("GroupByID: %v", err)
	}
	if group.Name != "Friday Night" || group.Icon != "game" {
		t.Errorf("group = %+v", group)
	}
	if len(group.InviteCode) == 0 {
		t.Error("the group came through without an invite code")
	}
	if _, err := s.GroupByInviteCode(ctx, group.InviteCode); err != nil {
		t.Errorf("the minted code does not open the group: %v", err)
	}

	// Both members are still in it, at the addresses they had, and each answers
	// to the name they last used rather than to their machine name.
	members, err := s.Members(ctx, "grp_1")
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("members = %+v, want 2", members)
	}
	names := map[string]Member{}
	for _, m := range members {
		names[m.DeviceID] = m
	}
	if got := names["dev_1"]; got.Nickname != "Tiago" || got.VirtualIP != "10.69.0.1" || got.Role != RoleOwner {
		t.Errorf("dev_1 = %+v", got)
	}
	if got := names["dev_2"]; got.Nickname != "Joao" || got.VirtualIP != "10.69.0.2" {
		t.Errorf("dev_2 = %+v", got)
	}

	// A device that never joined anything falls back to its machine name, since
	// there was no other name to take.
	third, err := s.DeviceByToken(ctx, "token-3")
	if err != nil {
		t.Fatalf("DeviceByToken: %v", err)
	}
	if third.Nickname != "PEDRO-PC" {
		t.Errorf("nickname = %q, want the machine name", third.Nickname)
	}

	// Two groups can share a name now, which the rebuilt table has to allow.
	if _, err := s.CreateGroup(ctx, Group{ID: "grp_2", Name: "Friday Night", Subnet: "10.69.0.0/24"}, "dev_1"); err != nil {
		t.Errorf("a second Friday Night: %v", err)
	}
}

// seedOldDatabase builds a database at an earlier schema version, with the kind
// of rows a real one would have by then.
func seedOldDatabase(t *testing.T, ctx context.Context, path string, version int) {
	t.Helper()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	files, err := migrationFiles()
	if err != nil {
		t.Fatalf("migrationFiles: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_version (version INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create schema_version: %v", err)
	}
	for i := 0; i < version; i++ {
		statement, err := migrations.ReadFile("migrations/" + files[i].Name())
		if err != nil {
			t.Fatalf("read %s: %v", files[i].Name(), err)
		}
		if _, err := db.ExecContext(ctx, string(statement)); err != nil {
			t.Fatalf("apply %s: %v", files[i].Name(), err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (?)`, version); err != nil {
		t.Fatalf("record version: %v", err)
	}

	for _, statement := range []string{
		`INSERT INTO devices (id, public_key, transport_key, name, created_at, last_seen_at)
		 VALUES ('dev_1', 'pub-1', 't-1', 'TIAGO-PC', 1, 1),
		        ('dev_2', 'pub-2', 't-2', 'JOAO-PC', 1, 1),
		        ('dev_3', 'pub-3', 't-3', 'PEDRO-PC', 1, 1)`,
		`INSERT INTO device_tokens (token_hash, device_id, created_at)
		 VALUES ('` + hashToken("token-3") + `', 'dev_3', 1)`,
		`INSERT INTO groups (id, name, name_normalized, icon, color, password_verifier, subnet, created_by_device_id, created_at)
		 VALUES ('grp_1', 'Friday Night', 'friday night', 'game', 'green', 'a-verifier', '10.69.0.0/24', 'dev_1', 1)`,
		`INSERT INTO memberships (id, group_id, device_id, nickname, virtual_ip, role, created_at)
		 VALUES ('mem_1', 'grp_1', 'dev_1', 'Tiago', '10.69.0.1', 'owner', 1),
		        ('mem_2', 'grp_1', 'dev_2', 'Joao', '10.69.0.2', 'member', 2)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}
