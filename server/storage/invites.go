package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"

	"github.com/OMouta/192168/protocol/invite"
)

// inviteCodeBytes encodes to exactly invite.Length characters, so a code has no
// padding and nothing left over.
const inviteCodeBytes = 5

// NewInviteCode returns a fresh code, in the same alphabet IDs use.
func NewInviteCode() (string, error) {
	raw := make([]byte, inviteCodeBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("storage: new invite code: %w", err)
	}
	return idAlphabet.EncodeToString(raw), nil
}

// GroupByInviteCode finds the group a code opens. The code is normalized first,
// so a pasted link or a stray space still works.
func (s *Store) GroupByInviteCode(ctx context.Context, code string) (Group, error) {
	normalized := invite.Normalize(code)
	if normalized == "" {
		return Group{}, ErrNotFound
	}
	return s.scanGroup(s.db.QueryRowContext(ctx, `
		SELECT id, name, icon, color, subnet, invite_code, created_at
		FROM groups WHERE invite_code = ?`, normalized))
}

// ResetInviteCode replaces a group's code and returns the new one. Owner only.
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

// freeInviteCode draws a code no group holds. A collision at 40 bits is not
// worth planning around, but checking costs one query.
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

// MemberCount is how many people belong to a group, shown on an invite.
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
