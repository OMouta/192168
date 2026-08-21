package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"time"
)

// ErrGroupFull means every address in the group's subnet is taken.
var ErrGroupFull = errors.New("storage: group is full")

// Group is a persistent private LAN.
//
// Icon and Colour are how the group is told apart from the others in a list.
// Both are short keys the app maps to a glyph and a colour, and both are empty
// until somebody picks.
type Group struct {
	ID               string
	Name             string
	Icon             string
	Color            string
	PasswordVerifier string
	Subnet           string
	CreatedAt        time.Time
}

// Role is what a member may do to the group itself.
type Role string

const (
	// RoleMember can use the group and change nothing about it.
	RoleMember Role = "member"
	// RoleOwner can rename it, change its password, and remove people.
	RoleOwner Role = "owner"
)

// Membership is one device's place in one group.
type Membership struct {
	ID         string
	GroupID    string
	GroupName  string
	GroupIcon  string
	GroupColor string

	DeviceID  string
	Nickname  string
	Subnet    string
	VirtualIP string
	Role      Role
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
		INSERT INTO groups (id, name, name_normalized, icon, color, password_verifier, subnet, created_by_device_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		g.ID, g.Name, normalizedName, g.Icon, g.Color, g.PasswordVerifier, g.Subnet, deviceID, now.Unix())
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
	// Whoever makes a group runs it.
	if _, err := tx.ExecContext(ctx,
		`UPDATE memberships SET role = 'owner' WHERE id = ?`, m.ID); err != nil {
		return Membership{}, fmt.Errorf("storage: set owner: %w", err)
	}
	m.Role = RoleOwner

	if err := tx.Commit(); err != nil {
		return Membership{}, fmt.Errorf("storage: commit: %w", err)
	}
	return m, nil
}

// GroupByName looks a group up by its normalized name, which is how a user
// finds one to join.
func (s *Store) GroupByName(ctx context.Context, normalizedName string) (Group, error) {
	return s.scanGroup(s.db.QueryRowContext(ctx, `
		SELECT id, name, icon, color, password_verifier, subnet, created_at
		FROM groups WHERE name_normalized = ?`, normalizedName))
}

// GroupByID looks a group up by ID.
func (s *Store) GroupByID(ctx context.Context, id string) (Group, error) {
	return s.scanGroup(s.db.QueryRowContext(ctx, `
		SELECT id, name, icon, color, password_verifier, subnet, created_at
		FROM groups WHERE id = ?`, id))
}

func (s *Store) scanGroup(row *sql.Row) (Group, error) {
	var (
		g       Group
		created int64
	)
	err := row.Scan(&g.ID, &g.Name, &g.Icon, &g.Color, &g.PasswordVerifier, &g.Subnet, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Group{}, ErrNotFound
	}
	if err != nil {
		return Group{}, fmt.Errorf("storage: read group: %w", err)
	}
	g.CreatedAt = time.Unix(created, 0)
	return g, nil
}

// AddMembership joins a device to a group. Joining one the device already left
// picks the old membership back up, so leaving and coming back does not leave a
// stale row in the way.
//
// Being removed is different from leaving, and does not come back: a removed
// device still knows the name and the password, so if joining undid it then
// removing anybody would mean nothing.
func (s *Store) AddMembership(ctx context.Context, g Group, deviceID, nickname string) (Membership, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Membership{}, fmt.Errorf("storage: begin: %w", err)
	}
	defer tx.Rollback()

	var banned sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT banned_at FROM memberships WHERE group_id = ? AND device_id = ?`,
		g.ID, deviceID).Scan(&banned)
	switch {
	case err == nil && banned.Valid:
		return Membership{}, ErrBanned
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return Membership{}, fmt.Errorf("storage: check removal: %w", err)
	}

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

	// Whatever address this device had here before, if it was ever a member.
	// A revoked row keeps its address for exactly this reason.
	var previous sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT virtual_ip FROM memberships WHERE group_id = ? AND device_id = ?`,
		g.ID, deviceID).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Membership{}, fmt.Errorf("storage: read previous address: %w", err)
	}

	// The address is cleared on the way in and chosen below. Un-revoking a row
	// that still names an address somebody else took meanwhile would collide
	// with the uniqueness index.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO memberships (id, group_id, device_id, nickname, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (group_id, device_id) DO UPDATE SET
			nickname   = excluded.nickname,
			revoked_at = NULL,
			virtual_ip = NULL`,
		id, g.ID, deviceID, nickname, now.Unix())
	if err != nil {
		return Membership{}, fmt.Errorf("storage: add membership: %w", err)
	}

	address, err := assignAddress(ctx, tx, g, deviceID, previous.String)
	if err != nil {
		return Membership{}, err
	}

	// The insert may have updated an existing row, so the authoritative ID
	// comes back from a read rather than from the value just generated.
	var (
		created int64
		role    Role
	)
	err = tx.QueryRowContext(ctx, `
		SELECT id, nickname, role, created_at FROM memberships WHERE group_id = ? AND device_id = ?`,
		g.ID, deviceID,
	).Scan(&id, &nickname, &role, &created)
	if err != nil {
		return Membership{}, fmt.Errorf("storage: read membership: %w", err)
	}

	return Membership{
		ID:         id,
		GroupID:    g.ID,
		GroupName:  g.Name,
		GroupIcon:  g.Icon,
		GroupColor: g.Color,

		DeviceID:  deviceID,
		Nickname:  nickname,
		Subnet:    g.Subnet,
		VirtualIP: address,
		Role:      role,
		CreatedAt: time.Unix(created, 0),
	}, nil
}

// assignAddress gives a device its address in a group and returns it. The one
// it had before comes back unless somebody claimed it while it was away.
func assignAddress(ctx context.Context, tx *sql.Tx, g Group, deviceID, previous string) (string, error) {
	taken, err := takenAddresses(ctx, tx, g.ID)
	if err != nil {
		return "", err
	}

	address := previous
	if address == "" || taken[address] {
		address, err = freeAddress(g.Subnet, taken)
		if err != nil {
			return "", err
		}
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE memberships SET virtual_ip = ? WHERE group_id = ? AND device_id = ?`,
		address, g.ID, deviceID); err != nil {
		return "", fmt.Errorf("storage: assign address: %w", err)
	}
	return address, nil
}

// takenAddresses is every address spoken for in a group. A revoked membership
// keeps the value on its row but does not hold it, so leaving a group hands the
// address to whoever joins next.
func takenAddresses(ctx context.Context, tx *sql.Tx, groupID string) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT virtual_ip FROM memberships
		WHERE group_id = ? AND revoked_at IS NULL AND virtual_ip IS NOT NULL`, groupID)
	if err != nil {
		return nil, fmt.Errorf("storage: read assigned addresses: %w", err)
	}
	defer rows.Close()

	taken := map[string]bool{}
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			return nil, fmt.Errorf("storage: scan assigned address: %w", err)
		}
		taken[ip] = true
	}
	return taken, rows.Err()
}

// freeAddress picks the lowest unused host address in the subnet. Lowest rather
// than random keeps a small group's addresses readable, which matters when
// someone is typing one into a game.
func freeAddress(subnet string, taken map[string]bool) (string, error) {
	prefix, err := netip.ParsePrefix(subnet)
	if err != nil {
		return "", fmt.Errorf("storage: bad subnet %q: %w", subnet, err)
	}

	// Skip the network address, and stop before the broadcast address.
	candidate := prefix.Masked().Addr().Next()
	for prefix.Contains(candidate) {
		next := candidate.Next()
		if !prefix.Contains(next) {
			break // candidate is the broadcast address
		}
		if ip := candidate.String(); !taken[ip] {
			return ip, nil
		}
		candidate = next
	}
	return "", ErrGroupFull
}

// MembershipsByDevice lists every group a device belongs to, which is what lets
// it reconnect without the group password.
func (s *Store) MembershipsByDevice(ctx context.Context, deviceID string) ([]Membership, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.group_id, g.name, g.icon, g.color, m.nickname, g.subnet, m.virtual_ip, m.role, m.created_at
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
		if err := rows.Scan(&m.ID, &m.GroupID, &m.GroupName, &m.GroupIcon, &m.GroupColor, &m.Nickname, &m.Subnet, &m.VirtualIP, &m.Role, &created); err != nil {
			return nil, fmt.Errorf("storage: scan membership: %w", err)
		}
		m.DeviceID = deviceID
		m.CreatedAt = time.Unix(created, 0)
		out = append(out, m)
	}
	return out, rows.Err()
}

// Member is one person in a group, whether or not they are connected.
type Member struct {
	DeviceID  string
	Nickname  string
	VirtualIP string
	Role      Role
	Online    bool
}

// Members lists everyone in a group and says who is connected.
//
// The peer list only carries people with a live session, because that is who
// there is anything to reach. This is the rest of them, so the app can show who
// is in the group rather than only who is here right now.
func (s *Store) Members(ctx context.Context, groupID string) ([]Member, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.device_id, m.nickname, m.virtual_ip, m.role, s.id IS NOT NULL
		FROM memberships m
		LEFT JOIN sessions s ON s.membership_id = m.id
		WHERE m.group_id = ? AND m.revoked_at IS NULL
		ORDER BY m.created_at`, groupID)
	if err != nil {
		return nil, fmt.Errorf("storage: list members: %w", err)
	}
	defer rows.Close()

	var out []Member
	for rows.Next() {
		var member Member
		if err := rows.Scan(&member.DeviceID, &member.Nickname, &member.VirtualIP, &member.Role, &member.Online); err != nil {
			return nil, fmt.Errorf("storage: read member: %w", err)
		}
		out = append(out, member)
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
		SELECT m.id, g.name, g.icon, g.color, m.nickname, g.subnet, m.virtual_ip, m.role, m.created_at
		FROM memberships m
		JOIN groups g ON g.id = m.group_id
		WHERE m.group_id = ? AND m.device_id = ? AND m.revoked_at IS NULL`,
		groupID, deviceID,
	).Scan(&m.ID, &m.GroupName, &m.GroupIcon, &m.GroupColor, &m.Nickname, &m.Subnet, &m.VirtualIP, &m.Role, &created)
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

// ownerOnly fails unless this device runs the group. Every management call goes
// through it, so the rule lives in one place rather than in each of them.
func ownerOnly(ctx context.Context, tx *sql.Tx, groupID, deviceID string) error {
	var role Role
	err := tx.QueryRowContext(ctx,
		`SELECT role FROM memberships WHERE group_id = ? AND device_id = ? AND revoked_at IS NULL`,
		groupID, deviceID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("storage: read role: %w", err)
	}
	if role != RoleOwner {
		return ErrForbidden
	}
	return nil
}

// RemoveMember takes someone out of a group and keeps them out.
//
// Their session goes with the membership, so they are disconnected rather than
// left on a network they no longer belong to.
func (s *Store) RemoveMember(ctx context.Context, groupID, ownerDeviceID, targetDeviceID string) error {
	if ownerDeviceID == targetDeviceID {
		// A group with no owner cannot be managed again by anybody. Leaving is
		// the way out, and it is a different thing.
		return ErrForbidden
	}

	return s.write(ctx, func(tx *sql.Tx) error {
		if err := ownerOnly(ctx, tx, groupID, ownerDeviceID); err != nil {
			return err
		}

		now := time.Now().Unix()
		result, err := tx.ExecContext(ctx, `
			UPDATE memberships SET revoked_at = ?, banned_at = ?
			WHERE group_id = ? AND device_id = ? AND revoked_at IS NULL`,
			now, now, groupID, targetDeviceID)
		if err != nil {
			return fmt.Errorf("storage: remove member: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return ErrNotFound
		}

		if _, err := tx.ExecContext(ctx, `
			DELETE FROM sessions WHERE group_id = ? AND membership_id IN (
				SELECT id FROM memberships WHERE group_id = ? AND device_id = ?
			)`, groupID, groupID, targetDeviceID); err != nil {
			return fmt.Errorf("storage: end removed member's session: %w", err)
		}
		return nil
	})
}

// RenameGroup changes what the group is called.
func (s *Store) RenameGroup(ctx context.Context, groupID, ownerDeviceID, name, normalizedName string) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if err := ownerOnly(ctx, tx, groupID, ownerDeviceID); err != nil {
			return err
		}

		_, err := tx.ExecContext(ctx,
			`UPDATE groups SET name = ?, name_normalized = ? WHERE id = ?`,
			name, normalizedName, groupID)
		if isUniqueViolation(err) {
			return ErrConflict
		}
		if err != nil {
			return fmt.Errorf("storage: rename group: %w", err)
		}
		return nil
	})
}

// SetGroupAppearance changes the icon and colour the group is shown with.
//
// Both at once, because they are picked together and read together: a colour
// that arrives without the icon it was chosen against is a half-applied change
// somebody has to fix.
func (s *Store) SetGroupAppearance(ctx context.Context, groupID, ownerDeviceID, icon, color string) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if err := ownerOnly(ctx, tx, groupID, ownerDeviceID); err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE groups SET icon = ?, color = ? WHERE id = ?`, icon, color, groupID); err != nil {
			return fmt.Errorf("storage: set group appearance: %w", err)
		}
		return nil
	})
}

// SetGroupPassword changes the password a new member joins with.
//
// It removes nobody. Membership is proved by the device token from then on, and
// the password is only ever checked at the door, so this closes the door rather
// than emptying the room.
func (s *Store) SetGroupPassword(ctx context.Context, groupID, ownerDeviceID, verifier string) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if err := ownerOnly(ctx, tx, groupID, ownerDeviceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE groups SET password_verifier = ? WHERE id = ?`, verifier, groupID); err != nil {
			return fmt.Errorf("storage: set group password: %w", err)
		}
		return nil
	})
}

// TransferOwnership hands the group to another member.
//
// A device identity lives on one machine and does not survive it being rebuilt,
// so without this a group whose owner reinstalls Windows can never be managed
// again.
func (s *Store) TransferOwnership(ctx context.Context, groupID, ownerDeviceID, targetDeviceID string) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if err := ownerOnly(ctx, tx, groupID, ownerDeviceID); err != nil {
			return err
		}

		result, err := tx.ExecContext(ctx, `
			UPDATE memberships SET role = 'owner'
			WHERE group_id = ? AND device_id = ? AND revoked_at IS NULL`,
			groupID, targetDeviceID)
		if err != nil {
			return fmt.Errorf("storage: promote member: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return ErrNotFound
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE memberships SET role = 'member' WHERE group_id = ? AND device_id = ?`,
			groupID, ownerDeviceID); err != nil {
			return fmt.Errorf("storage: step down: %w", err)
		}
		return nil
	})
}

// write runs one transaction and commits it if the work succeeds.
func (s *Store) write(ctx context.Context, work func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin: %w", err)
	}
	defer tx.Rollback()

	if err := work(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: commit: %w", err)
	}
	return nil
}

// DeleteGroup removes a group and everything that belongs to it.
//
// Memberships and sessions are declared to cascade from the group, so this ends
// them too: nobody is left holding a membership of something that no longer
// exists, and no session keeps a virtual IP in a group nobody can reach.
func (s *Store) DeleteGroup(ctx context.Context, groupID, ownerDeviceID string) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if err := ownerOnly(ctx, tx, groupID, ownerDeviceID); err != nil {
			return err
		}

		result, err := tx.ExecContext(ctx, `DELETE FROM groups WHERE id = ?`, groupID)
		if err != nil {
			return fmt.Errorf("storage: delete group: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return ErrNotFound
		}
		return nil
	})
}
