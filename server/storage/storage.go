// Package storage is the coordination server's database.
//
// SQLite is the default because a self-hosted instance for a handful of friends
// does not need a database server next to it. The schema is plain SQL and the
// queries are plain SQL, so moving to Postgres later is a driver and a dialect
// problem rather than a rewrite.
package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a lookup finds nothing. Callers turn it into the
// right API error, since a missing group and a missing session are not the same
// thing to a user.
var ErrNotFound = errors.New("storage: not found")

// ErrConflict is returned when a write loses a uniqueness race, such as two
// devices creating the same group name at once.
var ErrConflict = errors.New("storage: conflict")

// ErrBanned is returned when a device that was removed from a group tries to
// join it again. It knows the name and the password; that is the point.
var ErrBanned = errors.New("storage: removed from this group")

// ErrForbidden is returned when a caller is a member but not allowed to do this
// to the group.
var ErrForbidden = errors.New("storage: not allowed")

// Store owns the database handle.
type Store struct {
	db *sql.DB
}

// Open connects to the database and brings the schema up to date. An empty dsn
// means a file called 192168.db in the working directory, which is fine for a
// local run. A container deployment has to point this at a mounted volume, or
// the database goes away with the container.
func Open(ctx context.Context, dsn string) (*Store, error) {
	if dsn == "" {
		dsn = "192168.db"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: open: %w", err)
	}
	// SQLite takes one writer at a time. Leaving the pool unbounded turns that
	// into "database is locked" errors under concurrent writes rather than
	// queueing, which is the behaviour we want.
	db.SetMaxOpenConns(1)

	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("storage: %s: %w", pragma, err)
		}
	}

	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// migrations run in order and never change once released. Adding a column means
// adding an entry, not editing one.
var migrations = []string{
	`CREATE TABLE devices (
		id            TEXT PRIMARY KEY,
		public_key    TEXT NOT NULL UNIQUE,
		transport_key TEXT NOT NULL,
		name          TEXT NOT NULL,
		created_at    INTEGER NOT NULL,
		last_seen_at  INTEGER NOT NULL
	)`,

	// Tokens are stored hashed. A leaked database should not hand out live
	// credentials, and the server never needs the original back.
	`CREATE TABLE device_tokens (
		token_hash TEXT PRIMARY KEY,
		device_id  TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
		created_at INTEGER NOT NULL,
		revoked_at INTEGER
	)`,
	`CREATE INDEX idx_device_tokens_device ON device_tokens(device_id)`,

	// Registration nonces, kept only long enough to outlive the timestamp skew
	// a registration is allowed, then pruned.
	`CREATE TABLE register_nonces (
		nonce   TEXT PRIMARY KEY,
		seen_at INTEGER NOT NULL
	)`,

	`CREATE TABLE groups (
		id                   TEXT PRIMARY KEY,
		name                 TEXT NOT NULL,
		name_normalized      TEXT NOT NULL UNIQUE,
		password_verifier    TEXT NOT NULL,
		subnet               TEXT NOT NULL,
		created_by_device_id TEXT NOT NULL REFERENCES devices(id),
		created_at           INTEGER NOT NULL
	)`,

	`CREATE TABLE memberships (
		id         TEXT PRIMARY KEY,
		group_id   TEXT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
		device_id  TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
		nickname   TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		revoked_at INTEGER,
		UNIQUE (group_id, device_id)
	)`,

	// One session per membership, so connecting twice replaces the old one
	// rather than leaving a ghost in the peer list. Virtual IPs are unique
	// inside a group and only for as long as the session lives.
	`CREATE TABLE sessions (
		id               TEXT PRIMARY KEY,
		group_id         TEXT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
		membership_id    TEXT NOT NULL UNIQUE REFERENCES memberships(id) ON DELETE CASCADE,
		virtual_ip       TEXT NOT NULL,
		endpoint_address TEXT,
		endpoint_port    INTEGER,
		connected_at     INTEGER NOT NULL,
		last_seen_at     INTEGER NOT NULL,
		UNIQUE (group_id, virtual_ip)
	)`,

	// Who may manage a group. On the membership rather than the group, so it
	// generalises to more than one person and survives being handed over
	// without touching the group itself.
	`ALTER TABLE memberships ADD COLUMN role TEXT NOT NULL DEFAULT 'member'`,

	// Set apart from revoked_at, which only means not currently a member.
	// Leaving is revoked and can be undone by joining again; being removed is
	// both, and joining again does not undo it. Without the difference,
	// removing somebody who knows the password does nothing at all.
	`ALTER TABLE memberships ADD COLUMN banned_at INTEGER`,

	// Groups that existed before roles did. Whoever made one owns it.
	`UPDATE memberships SET role = 'owner'
	 WHERE EXISTS (
		SELECT 1 FROM groups g
		WHERE g.id = memberships.group_id AND g.created_by_device_id = memberships.device_id
	 )`,

	// An address belongs to a membership rather than to a session. It is handed
	// out at the door and stays the same, so somebody who hosted last night is
	// at the address their friends already wrote into a game.
	`ALTER TABLE memberships ADD COLUMN virtual_ip TEXT`,

	// Partial, so a revoked membership frees its address without losing it.
	// Somebody who leaves and comes back takes the same one again unless
	// another member claimed it while they were gone.
	`CREATE UNIQUE INDEX idx_memberships_address
	 ON memberships(group_id, virtual_ip) WHERE revoked_at IS NULL`,

	// Sessions no longer carry an address. Rebuilt rather than altered, since
	// the address was half of a table constraint; a session lasts as long as
	// somebody is connected, so there is nothing here worth carrying across.
	`DROP TABLE sessions`,
	`CREATE TABLE sessions (
		id               TEXT PRIMARY KEY,
		group_id         TEXT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
		membership_id    TEXT NOT NULL UNIQUE REFERENCES memberships(id) ON DELETE CASCADE,
		endpoint_address TEXT,
		endpoint_port    INTEGER,
		connected_at     INTEGER NOT NULL,
		last_seen_at     INTEGER NOT NULL
	)`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("storage: create schema_version: %w", err)
	}

	var applied int
	err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&applied)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := s.db.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (0)`); err != nil {
			return fmt.Errorf("storage: init schema_version: %w", err)
		}
	case err != nil:
		return fmt.Errorf("storage: read schema_version: %w", err)
	}

	for i := applied; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("storage: begin migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("storage: migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE schema_version SET version = ?`, i+1); err != nil {
			tx.Rollback()
			return fmt.Errorf("storage: record migration %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("storage: commit migration %d: %w", i+1, err)
		}
	}
	return nil
}

// idAlphabet leaves out the letters and digits that get misread when someone
// copies an ID out of a log or a support message.
var idAlphabet = base32.NewEncoding("abcdefghijkmnpqrstuvwxyz23456789").WithPadding(base32.NoPadding)

// NewID returns a prefixed random identifier, such as grp_4f8c2a1b9e7d3f6a.
func NewID(prefix string) (string, error) {
	raw := make([]byte, 10)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("storage: new id: %w", err)
	}
	return prefix + "_" + idAlphabet.EncodeToString(raw), nil
}

// isUniqueViolation reports whether an error came from a UNIQUE constraint. The
// driver does not expose a typed error for it, so this reads the message.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
