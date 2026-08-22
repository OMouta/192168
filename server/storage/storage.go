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
	"embed"
	"encoding/base32"
	"errors"
	"fmt"
	"io/fs"
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
	// A group made before invite codes existed needs one, and a random value
	// per row is not something SQL should be asked to produce. It runs once and
	// then finds nothing.
	if err := s.mintMissingInviteCodes(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// migrations run in filename order, one statement per file. The number in the
// name is the schema version, so a released file is never edited, renamed, or
// renumbered: it has already run on databases still in use.
//
//go:embed migrations/*.sql
var migrations embed.FS

// migrationFiles lists the migrations in the order they apply. A name out of
// sequence fails startup rather than skipping a version or repeating one.
func migrationFiles() ([]fs.DirEntry, error) {
	files, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("storage: read migrations: %w", err)
	}

	for i, file := range files {
		if prefix := fmt.Sprintf("%04d_", i+1); !strings.HasPrefix(file.Name(), prefix) {
			return nil, fmt.Errorf("storage: migration %d is %q, which does not start with %q", i+1, file.Name(), prefix)
		}
	}
	return files, nil
}

func (s *Store) migrate(ctx context.Context) error {
	files, err := migrationFiles()
	if err != nil {
		return err
	}

	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("storage: create schema_version: %w", err)
	}

	var applied int
	err = s.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&applied)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := s.db.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (0)`); err != nil {
			return fmt.Errorf("storage: init schema_version: %w", err)
		}
	case err != nil:
		return fmt.Errorf("storage: read schema_version: %w", err)
	}

	for i := applied; i < len(files); i++ {
		name := files[i].Name()

		statement, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("storage: read migration %s: %w", name, err)
		}

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("storage: begin migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, string(statement)); err != nil {
			tx.Rollback()
			return fmt.Errorf("storage: migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE schema_version SET version = ?`, i+1); err != nil {
			tx.Rollback()
			return fmt.Errorf("storage: record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("storage: commit migration %s: %w", name, err)
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
