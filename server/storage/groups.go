package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Group is a persistent private LAN.
type Group struct {
	ID               string
	Name             string
	PasswordVerifier string
	Subnet           string
	CreatedAt        time.Time
}

// Membership is one device's place in one group.
type Membership struct {
	ID        string
	GroupID   string
	GroupName string
	DeviceID  string
	Nickname  string
	Subnet    string
	CreatedAt time.Time
}

// CreateGroup creates a group and makes its creator the first member, in one
// transaction. A group with no members would be unreachable.
func (s *Store) CreateGroup(ctx context.Context, g Group, normalizedName, deviceID, nickname string) (Membership, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Membership{}, fmt.Errorf("storage: begin: %w", err)
	}
	defer tx.Rollback()

	now := time.Now()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO groups (id, name, name_normalized, password_verifier, subnet, created_by_device_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		g.ID, g.Name, normalizedName, g.PasswordVerifier, g.Subnet, deviceID, now.Unix())
	if isUniqueViolation(err) {
		return Membership{}, ErrConflict
	}
	if err != nil {
		return Membership{}, fmt.Errorf("storage: create group: %w", err)
	}

	m, err := insertMembership(ctx, tx, g, deviceID, nickname, now)
	if err != nil {
		return Membership{}, err
	}
	if err := tx.Commit(); err != nil {
		return Membership{}, fmt.Errorf("storage: commit: %w", err)
	}
	return m, nil
}

// GroupByName looks a group up by its normalized name, which is how a user
// finds one to join.
func (s *Store) GroupByName(ctx context.Context, normalizedName string) (Group, error) {
	return s.scanGroup(s.db.QueryRowContext(ctx, `
		SELECT id, name, password_verifier, subnet, created_at
		FROM groups WHERE name_normalized = ?`, normalizedName))
}

// GroupByID looks a group up by ID.
func (s *Store) GroupByID(ctx context.Context, id string) (Group, error) {
	return s.scanGroup(s.db.QueryRowContext(ctx, `
		SELECT id, name, password_verifier, subnet, created_at
		FROM groups WHERE id = ?`, id))
}

func (s *Store) scanGroup(row *sql.Row) (Group, error) {
	var (
		g       Group
		created int64
	)
	err := row.Scan(&g.ID, &g.Name, &g.PasswordVerifier, &g.Subnet, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Group{}, ErrNotFound
	}
	if err != nil {
		return Group{}, fmt.Errorf("storage: read group: %w", err)
	}
	g.CreatedAt = time.Unix(created, 0)
	return g, nil
}

// AddMembership joins a device to a group. Joining a group the device is
// already in updates the nickname and clears any revocation, so rejoining after
// being removed works without a stale row in the way.
func (s *Store) AddMembership(ctx context.Context, g Group, deviceID, nickname string) (Membership, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Membership{}, fmt.Errorf("storage: begin: %w", err)
	}
	defer tx.Rollback()

	m, err := insertMembership(ctx, tx, g, deviceID, nickname, time.Now())
	if err != nil {
		return Membership{}, err
	}
	if err := tx.Commit(); err != nil {
		return Membership{}, fmt.Errorf("storage: commit: %w", err)
	}
	return m, nil
}

func insertMembership(ctx context.Context, tx *sql.Tx, g Group, deviceID, nickname string, now time.Time) (Membership, error) {
	id, err := NewID("mem")
	if err != nil {
		return Membership{}, err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO memberships (id, group_id, device_id, nickname, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (group_id, device_id) DO UPDATE SET
			nickname   = excluded.nickname,
			revoked_at = NULL`,
		id, g.ID, deviceID, nickname, now.Unix())
	if err != nil {
		return Membership{}, fmt.Errorf("storage: add membership: %w", err)
	}

	// The insert may have updated an existing row, so the authoritative ID
	// comes back from a read rather than from the value just generated.
	var created int64
	err = tx.QueryRowContext(ctx, `
		SELECT id, nickname, created_at FROM memberships WHERE group_id = ? AND device_id = ?`,
		g.ID, deviceID,
	).Scan(&id, &nickname, &created)
	if err != nil {
		return Membership{}, fmt.Errorf("storage: read membership: %w", err)
	}

	return Membership{
		ID:        id,
		GroupID:   g.ID,
		GroupName: g.Name,
		DeviceID:  deviceID,
		Nickname:  nickname,
		Subnet:    g.Subnet,
		CreatedAt: time.Unix(created, 0),
	}, nil
}

// MembershipsByDevice lists every group a device belongs to, which is what lets
// it reconnect without the group password.
func (s *Store) MembershipsByDevice(ctx context.Context, deviceID string) ([]Membership, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.group_id, g.name, m.nickname, g.subnet, m.created_at
		FROM memberships m
		JOIN groups g ON g.id = m.group_id
		WHERE m.device_id = ? AND m.revoked_at IS NULL
		ORDER BY m.created_at`, deviceID)
	if err != nil {
		return nil, fmt.Errorf("storage: list memberships: %w", err)
	}
	defer rows.Close()

	var out []Membership
	for rows.Next() {
		var (
			m       Membership
			created int64
		)
		if err := rows.Scan(&m.ID, &m.GroupID, &m.GroupName, &m.Nickname, &m.Subnet, &created); err != nil {
			return nil, fmt.Errorf("storage: scan membership: %w", err)
		}
		m.DeviceID = deviceID
		m.CreatedAt = time.Unix(created, 0)
		out = append(out, m)
	}
	return out, rows.Err()
}

// Membership returns one device's membership in one group.
func (s *Store) Membership(ctx context.Context, groupID, deviceID string) (Membership, error) {
	var (
		m       Membership
		created int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT m.id, g.name, m.nickname, g.subnet, m.created_at
		FROM memberships m
		JOIN groups g ON g.id = m.group_id
		WHERE m.group_id = ? AND m.device_id = ? AND m.revoked_at IS NULL`,
		groupID, deviceID,
	).Scan(&m.ID, &m.GroupName, &m.Nickname, &m.Subnet, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Membership{}, ErrNotFound
	}
	if err != nil {
		return Membership{}, fmt.Errorf("storage: read membership: %w", err)
	}
	m.GroupID = groupID
	m.DeviceID = deviceID
	m.CreatedAt = time.Unix(created, 0)
	return m, nil
}

// RevokeMembership removes a device from a group and ends any session it has
// there. Revocation applies whether or not the device is online.
func (s *Store) RevokeMembership(ctx context.Context, groupID, deviceID string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE memberships SET revoked_at = ? WHERE group_id = ? AND device_id = ? AND revoked_at IS NULL`,
		time.Now().Unix(), groupID, deviceID)
	if err != nil {
		return fmt.Errorf("storage: revoke membership: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: revoke membership: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}

	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM sessions
		WHERE membership_id IN (SELECT id FROM memberships WHERE group_id = ? AND device_id = ?)`,
		groupID, deviceID); err != nil {
		return fmt.Errorf("storage: end sessions: %w", err)
	}
	return nil
}

// SetNickname changes a device's nickname in one group.
func (s *Store) SetNickname(ctx context.Context, groupID, deviceID, nickname string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE memberships SET nickname = ? WHERE group_id = ? AND device_id = ? AND revoked_at IS NULL`,
		nickname, groupID, deviceID)
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
