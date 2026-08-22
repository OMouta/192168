package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"

	"github.com/OMouta/192168/protocol/invite"
)

// inviteCodeBytes is how much randomness a code carries. Five bytes is exactly
// invite.Length characters once encoded, so a code has no padding and no
// leftovers.
const inviteCodeBytes = 5

// NewInviteCode returns a fresh code, in the same unambiguous alphabet IDs use.
func NewInviteCode() (string, error) {
	raw := make([]byte, inviteCodeBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("storage: new invite code: %w", err)
	}
	return idAlphabet.EncodeToString(raw), nil
}

// GroupByInviteCode finds the group a code opens.
//
// The code is normalized first, so somebody who pasted a link, or typed it with
// a space in the middle, still gets in.
func (s *Store) GroupByInviteCode(ctx context.Context, code string) (Group, error) {
	normalized := invite.Normalize(code)
	if normalized == "" {
		return Group{}, ErrNotFound
	}
	return s.scanGroup(s.db.QueryRowContext(ctx, `
		SELECT id, name, icon, color, subnet, invite_code, created_at
		FROM groups WHERE invite_code = ?`, normalized))
}

// ResetInviteCode throws away a group's code and returns its replacement. It is
// the only way back from a code that reached somebody it should not have, and
// it is the owner's alone.
func (s *Store) ResetInviteCode(ctx context.Context, groupID, ownerDeviceID string) (string, error) {
	var code string
	err := s.write(ctx, func(tx *sql.Tx) error {
		if err := ownerOnly(ctx, tx, groupID, ownerDeviceID); err != nil {
			return err
		}
		fresh, err := setInviteCode(ctx, tx, groupID)
		if err != nil {
			return err
		}
		code = fresh
		return nil
	})
	return code, err
}

// setInviteCode gives a group a new code.
func setInviteCode(ctx context.Context, tx *sql.Tx, groupID string) (string, error) {
	code, err := freeInviteCode(ctx, tx)
	if err != nil {
		return "", err
	}
	res, err := tx.ExecContext(ctx, `UPDATE groups SET invite_code = ? WHERE id = ?`, code, groupID)
	if err != nil {
		return "", fmt.Errorf("storage: set invite code: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("storage: set invite code: %w", err)
	}
	if affected == 0 {
		return "", ErrNotFound
	}
	return code, nil
}

// freeInviteCode draws a code no group holds. A collision at forty bits is not
// something to plan around, but it costs one query to be certain rather than
// confident.
func freeInviteCode(ctx context.Context, tx *sql.Tx) (string, error) {
	for attempt := 0; attempt < 5; attempt++ {
		code, err := NewInviteCode()
		if err != nil {
			return "", err
		}
		var taken bool
		if err := tx.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM groups WHERE invite_code = ?)`, code).Scan(&taken); err != nil {
			return "", fmt.Errorf("storage: check invite code: %w", err)
		}
		if !taken {
			return code, nil
		}
	}
	return "", fmt.Errorf("storage: could not find a free invite code")
}

// MemberCount is how many people belong to a group. It is what an invite shows
// somebody deciding whether to accept it.
func (s *Store) MemberCount(ctx context.Context, groupID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memberships WHERE group_id = ? AND revoked_at IS NULL`,
		groupID).Scan(&count)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("storage: count members: %w", err)
	}
	return count, nil
}
