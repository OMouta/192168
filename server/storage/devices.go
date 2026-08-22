package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

// Device is one installation.
//
// Name is the machine's own name. Nickname is what the person picked, and it is
// what the rest of a group sees. One per device, not one per membership.
type Device struct {
	ID           string
	PublicKey    string
	TransportKey string
	Name         string
	Nickname     string
	CreatedAt    time.Time
	LastSeenAt   time.Time
}

// RegisterDevice stores a device and issues its first token. Registering a
// device ID that already exists updates its keys and name, so reinstalling over
// an existing identity works instead of colliding.
//
// The nickname is a seed, not an assignment: it only lands on a device with
// none, so registering again does not undo a chosen name.
//
// The returned token is the only time the plaintext exists on this side.
func (s *Store) RegisterDevice(ctx context.Context, d Device) (token string, err error) {
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("storage: begin: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO devices (id, public_key, transport_key, name, nickname, created_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			public_key    = excluded.public_key,
			transport_key = excluded.transport_key,
			name          = excluded.name,
			nickname      = CASE WHEN devices.nickname = '' THEN excluded.nickname ELSE devices.nickname END,
			last_seen_at  = excluded.last_seen_at`,
		d.ID, d.PublicKey, d.TransportKey, d.Name, d.Nickname, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			// The public key belongs to a different device ID.
			return "", ErrConflict
		}
		return "", fmt.Errorf("storage: register device: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO device_tokens (token_hash, device_id, created_at) VALUES (?, ?, ?)`,
		hash, d.ID, now); err != nil {
		return "", fmt.Errorf("storage: issue token: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("storage: commit: %w", err)
	}
	return token, nil
}

// DeviceByToken resolves a bearer token to the device that holds it, and marks
// the device as seen. A revoked token returns ErrNotFound, since a caller has
// no business learning the difference.
func (s *Store) DeviceByToken(ctx context.Context, token string) (Device, error) {
	var (
		d        Device
		created  int64
		lastSeen int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT d.id, d.public_key, d.transport_key, d.name, d.nickname, d.created_at, d.last_seen_at
		FROM device_tokens t
		JOIN devices d ON d.id = t.device_id
		WHERE t.token_hash = ? AND t.revoked_at IS NULL`,
		hashToken(token),
	).Scan(&d.ID, &d.PublicKey, &d.TransportKey, &d.Name, &d.Nickname, &created, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	if err != nil {
		return Device{}, fmt.Errorf("storage: device by token: %w", err)
	}
	d.CreatedAt = time.Unix(created, 0)
	d.LastSeenAt = time.Unix(lastSeen, 0)

	if _, err := s.db.ExecContext(ctx, `UPDATE devices SET last_seen_at = ? WHERE id = ?`, time.Now().Unix(), d.ID); err != nil {
		return Device{}, fmt.Errorf("storage: touch device: %w", err)
	}
	return d, nil
}

// SetDeviceNickname changes what this device is called, everywhere at once.
func (s *Store) SetDeviceNickname(ctx context.Context, deviceID, nickname string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE devices SET nickname = ? WHERE id = ?`, nickname, deviceID)
	if err != nil {
		return fmt.Errorf("storage: set nickname: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: set nickname: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// ConnectedGroupIDs is every group this device has a live session in. A rename
// is broadcast to each of them. In practice there is at most one.
func (s *Store) ConnectedGroupIDs(ctx context.Context, deviceID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT s.group_id
		FROM sessions s
		JOIN memberships m ON m.id = s.membership_id
		WHERE m.device_id = ?`, deviceID)
	if err != nil {
		return nil, fmt.Errorf("storage: list connected groups: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("storage: scan connected group: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ClaimRegisterNonce records a registration nonce and reports whether it was
// unused. A repeat means someone is replaying a captured registration.
func (s *Store) ClaimRegisterNonce(ctx context.Context, nonce string, keepFor time.Duration) (bool, error) {
	now := time.Now()

	// Old nonces cannot be replayed any more, because the timestamp they are
	// bound to is already out of range.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM register_nonces WHERE seen_at <= ?`, now.Add(-keepFor).Unix()); err != nil {
		return false, fmt.Errorf("storage: prune nonces: %w", err)
	}

	_, err := s.db.ExecContext(ctx, `INSERT INTO register_nonces (nonce, seen_at) VALUES (?, ?)`, nonce, now.Unix())
	if isUniqueViolation(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("storage: claim nonce: %w", err)
	}
	return true, nil
}

func newToken() (token, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("storage: new token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawStdEncoding.EncodeToString(sum[:])
}
