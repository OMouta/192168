package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Session is a device's live connection to a group. It exists only while
// connected. The address it reports is the membership's, which outlives it.
type Session struct {
	ID           string
	GroupID      string
	MembershipID string
	DeviceID     string
	VirtualIP    string
	ConnectedAt  time.Time
}

// SessionPeer is one row of a group's live peer list.
type SessionPeer struct {
	DeviceID     string
	Nickname     string
	VirtualIP    string
	TransportKey string
	Endpoint     *Endpoint
}

// Endpoint is a peer's public UDP address, as it reported it after STUN.
type Endpoint struct {
	Address string
	Port    int
}

// CreateSession connects a membership to its group.
//
// It hands out no address. The membership already has one, given when the
// device joined, so connecting first says nothing about which address you get.
//
// Connecting again replaces the old session rather than adding a second one. A
// client that crashed and came back would otherwise sit in the peer list twice,
// once as a ghost nobody can reach.
func (s *Store) CreateSession(ctx context.Context, m Membership) (Session, error) {
	id, err := NewID("ses")
	if err != nil {
		return Session{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("storage: begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE membership_id = ?`, m.ID); err != nil {
		return Session{}, fmt.Errorf("storage: replace session: %w", err)
	}

	now := time.Now()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sessions (id, group_id, membership_id, connected_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?)`,
		id, m.GroupID, m.ID, now.Unix(), now.Unix()); err != nil {
		return Session{}, fmt.Errorf("storage: create session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("storage: commit: %w", err)
	}

	return Session{
		ID:           id,
		GroupID:      m.GroupID,
		MembershipID: m.ID,
		DeviceID:     m.DeviceID,
		VirtualIP:    m.VirtualIP,
		ConnectedAt:  now,
	}, nil
}

// SessionByID reads a session and the device that owns it, so a caller can
// check the session belongs to whoever is asking.
func (s *Store) SessionByID(ctx context.Context, id string) (Session, error) {
	var (
		sess      Session
		connected int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.group_id, s.membership_id, m.device_id, m.virtual_ip, s.connected_at
		FROM sessions s
		JOIN memberships m ON m.id = s.membership_id
		WHERE s.id = ?`, id,
	).Scan(&sess.ID, &sess.GroupID, &sess.MembershipID, &sess.DeviceID, &sess.VirtualIP, &connected)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("storage: read session: %w", err)
	}
	sess.ConnectedAt = time.Unix(connected, 0)
	return sess, nil
}

// PeersInGroup lists the live sessions in a group, leaving out the device
// asking, since nobody needs to be told about themselves.
func (s *Store) PeersInGroup(ctx context.Context, groupID, exceptDeviceID string) ([]SessionPeer, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.device_id, d.nickname, m.virtual_ip, d.transport_key, s.endpoint_address, s.endpoint_port
		FROM sessions s
		JOIN memberships m ON m.id = s.membership_id
		JOIN devices d     ON d.id = m.device_id
		WHERE s.group_id = ? AND m.device_id != ? AND m.revoked_at IS NULL
		ORDER BY s.connected_at`, groupID, exceptDeviceID)
	if err != nil {
		return nil, fmt.Errorf("storage: list peers: %w", err)
	}
	defer rows.Close()

	var out []SessionPeer
	for rows.Next() {
		var (
			p    SessionPeer
			addr sql.NullString
			port sql.NullInt64
		)
		if err := rows.Scan(&p.DeviceID, &p.Nickname, &p.VirtualIP, &p.TransportKey, &addr, &port); err != nil {
			return nil, fmt.Errorf("storage: scan peer: %w", err)
		}
		// A peer that has not published an endpoint yet is online but not
		// reachable, which is a normal state right after connecting.
		if addr.Valid && port.Valid {
			p.Endpoint = &Endpoint{Address: addr.String, Port: int(port.Int64)}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetSessionEndpoint records where a peer can be reached, after it has asked
// STUN. Endpoints change whenever a NAT mapping does, so this is called again
// through the life of a session.
func (s *Store) SetSessionEndpoint(ctx context.Context, sessionID, address string, port int) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET endpoint_address = ?, endpoint_port = ?, last_seen_at = ? WHERE id = ?`,
		address, port, time.Now().Unix(), sessionID)
	if err != nil {
		return fmt.Errorf("storage: set endpoint: %w", err)
	}
	return checkAffected(res)
}

// TouchSession marks a session as still alive.
func (s *Store) TouchSession(ctx context.Context, sessionID string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE id = ?`, time.Now().Unix(), sessionID)
	if err != nil {
		return fmt.Errorf("storage: touch session: %w", err)
	}
	return checkAffected(res)
}

// DeleteSession ends a session. The address stays with the membership, so
// disconnecting takes somebody off the peer list and nothing more.
func (s *Store) DeleteSession(ctx context.Context, sessionID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID)
	if err != nil {
		return fmt.Errorf("storage: delete session: %w", err)
	}
	return checkAffected(res)
}

// ExpireSessions removes sessions nobody has heard from, so a client that was
// unplugged stops showing as online. It returns the group and device of each
// one, which is what the realtime channel announces.
func (s *Store) ExpireSessions(ctx context.Context, olderThan time.Duration) ([]Session, error) {
	cutoff := time.Now().Add(-olderThan).Unix()

	// Reading the doomed rows and deleting them has to be one transaction. A
	// session that gets touched in between would otherwise be reported as gone
	// and then deleted anyway, disconnecting somebody who is right there.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("storage: begin: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT s.id, s.group_id, m.device_id
		FROM sessions s
		JOIN memberships m ON m.id = s.membership_id
		WHERE s.last_seen_at <= ?`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("storage: find stale sessions: %w", err)
	}

	var stale []Session
	for rows.Next() {
		var sess Session
		if err := rows.Scan(&sess.ID, &sess.GroupID, &sess.DeviceID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("storage: scan stale session: %w", err)
		}
		stale = append(stale, sess)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(stale) == 0 {
		return nil, nil
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE last_seen_at <= ?`, cutoff); err != nil {
		return nil, fmt.Errorf("storage: expire sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("storage: commit: %w", err)
	}
	return stale, nil
}

func checkAffected(res sql.Result) error {
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}
