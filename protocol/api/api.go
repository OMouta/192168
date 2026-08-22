// Package api describes the control-plane HTTP and WebSocket payloads.
//
// The control server introduces peers to each other: it owns groups,
// memberships, sessions, virtual IP assignment, and endpoint exchange. It
// never sees game traffic.
package api

// Discovery is the response of GET /.well-known/192168. It lets a single
// shipped client work against the default deployment or any self-hosted one
// from a base URL alone.
type Discovery struct {
	Version  int      `json:"version"`
	API      string   `json:"api"`
	Realtime string   `json:"realtime"`
	STUN     []string `json:"stun"`
	// Invite is where this server serves invite links, ending in a slash. A
	// code goes on the end. Empty from a server that predates them.
	Invite   string   `json:"invite,omitempty"`
	Relay    *string  `json:"relay"`
	Features Features `json:"features"`
}

// Features advertises optional server capabilities. Absent or false means the
// client must not depend on them.
type Features struct {
	Relay       bool `json:"relay"`
	PeerRouting bool `json:"peerRouting"`
}

// RegisterDeviceRequest registers this installation's long-lived identity. The
// private half of the key pair never leaves the machine.
//
// A device carries two keys. PublicKey is Ed25519 and signs this request, which
// proves the caller holds it. TransportKey is the Curve25519 static key peers
// run the Noise handshake against, and the server passes it to the rest of the
// group. One key signs, the other does key agreement.
//
// This is the one unauthenticated write in the API. The signature covers every
// other field, and IssuedAt and Nonce are what stop a captured registration
// from being replayed later.
type RegisterDeviceRequest struct {
	DeviceID     string `json:"deviceId"`
	PublicKey    string `json:"publicKey"`
	TransportKey string `json:"transportKey"`
	DeviceName   string `json:"deviceName"`
	IssuedAt     int64  `json:"issuedAt"`
	Nonce        string `json:"nonce"`
	Signature    string `json:"signature"`
}

// RegisterDeviceResponse hands back the bearer token every later request
// carries. It is shown once and stored under OS-backed protection.
type RegisterDeviceResponse struct {
	DeviceToken string `json:"deviceToken"`
}

// Me is what the calling device is: for now, the name it goes by. A client
// that has lost its local copy reads it back from here.
type Me struct {
	DeviceID string `json:"deviceId"`
	Nickname string `json:"nickname"`
}

// SetNicknameRequest changes what this device is called, in every group.
type SetNicknameRequest struct {
	Nickname string `json:"nickname"`
}

// CreateGroupRequest creates a new virtual LAN.
//
// Icon and Color are the look it is made with, in the keys
// SetGroupAppearanceRequest carries. Set here rather than straight after, so a
// group is never briefly something other than what its maker picked.
type CreateGroupRequest struct {
	Name  string `json:"name"`
	Icon  string `json:"icon,omitempty"`
	Color string `json:"color,omitempty"`
}

// JoinByCodeRequest joins whichever group a code opens. The device token says
// who is asking, so the code is the whole body.
type JoinByCodeRequest struct {
	Code string `json:"code"`
}

// Invite is what a code opens, shown before anybody joins. It is served
// without a token, because holding the code is the authorization.
type Invite struct {
	Code       string `json:"code"`
	GroupName  string `json:"groupName"`
	GroupIcon  string `json:"groupIcon,omitempty"`
	GroupColor string `json:"groupColor,omitempty"`
	Members    int    `json:"members"`
}

// InviteCodeResponse carries a group's code after it has been replaced.
type InviteCodeResponse struct {
	Code string `json:"code"`
}

// Membership is the relationship between a device and a group. It is what lets
// the daemon reconnect without the invite code: the device token authenticates
// the caller, and the server looks up what it is a member of.
// Revoking a membership is a server-side change, so it takes effect whether or
// not that device is online.
// VirtualIP is the address this device holds in the group. It is given when
// the device joins and does not change, so it can be written into a game and
// left there.
//
// GroupIcon and GroupColor are the look the owner picked, as short keys the
// client maps to a glyph and a colour. Empty means it was never chosen.
type Membership struct {
	MembershipID string `json:"membershipId"`
	GroupID      string `json:"groupId"`
	GroupName    string `json:"groupName"`
	GroupIcon    string `json:"groupIcon,omitempty"`
	GroupColor   string `json:"groupColor,omitempty"`
	Subnet       string `json:"subnet"`
	VirtualIP    string `json:"virtualIp"`
	Role         string `json:"role"`
	// InviteCode is sent only to the group's owner. Empty for everybody else.
	InviteCode string `json:"inviteCode,omitempty"`
	// OnlineMembers is how many of the group are connected, counting this
	// device. Absent where the server was not asked to count, which is not the
	// same as nobody being there.
	OnlineMembers *int `json:"onlineMembers,omitempty"`
	// Members is how many belong to the group at all, connected or not. A
	// pointer because a server older than this field sends nothing, and "not
	// told" has to be told apart from a group with nobody in it, which is not
	// a thing a membership can point at.
	Members *int `json:"members,omitempty"`
}

// SetGroupAppearanceRequest changes the icon and colour a group is shown with.
// The owner's alone, and both travel together because they are picked together.
type SetGroupAppearanceRequest struct {
	Icon  string `json:"icon"`
	Color string `json:"color"`
}

// CreateSessionResponse is returned when a device connects to a group. The
// virtual IP is the one the membership already holds; connecting neither picks
// it nor gives it up.
type CreateSessionResponse struct {
	SessionID string `json:"sessionId"`
	VirtualIP string `json:"virtualIp"`
	Peers     []Peer `json:"peers"`
}

// Peer is another device with an active session in the same group.
//
// TransportKey is the Curve25519 static key this peer's handshake runs against.
// Checking it is what makes the endpoint below safe to dial: the address is
// only a hint, and anything answering there that cannot prove it holds this key
// gets nowhere.
type Peer struct {
	DeviceID     string    `json:"deviceId"`
	Nickname     string    `json:"nickname"`
	VirtualIP    string    `json:"virtualIp"`
	TransportKey string    `json:"transportKey"`
	Endpoint     *Endpoint `json:"endpoint,omitempty"`
}

// Roles a member can have in a group. Only the owner may change the group
// itself, and the server is what enforces it.
const (
	RoleMember = "member"
	RoleOwner  = "owner"
)

// Member is one person in a group, whether or not they are connected. A peer is
// someone there is a link to; a member is someone who belongs.
//
// The address is here rather than only on Peer because it belongs to the
// membership. Somebody who is away still has one, and the app shows it.
type Member struct {
	DeviceID  string `json:"deviceId"`
	Nickname  string `json:"nickname"`
	VirtualIP string `json:"virtualIp"`
	Role      string `json:"role"`
	Online    bool   `json:"online"`
}

// MembersResponse is what listing a group's members returns.
type MembersResponse struct {
	Members []Member `json:"members"`
}

// Endpoint is a public UDP address candidate discovered via STUN.
type Endpoint struct {
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
}

// Error is the body returned for any non-2xx control-plane response. Code is
// stable and machine-readable; Message is safe to show the user.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Stable error codes. The client maps these to user-facing copy rather than
// showing transport or socket details.
const (
	ErrBadRequest         = "bad_request"
	ErrUnauthorized       = "unauthorized"
	ErrGroupNotFound      = "group_not_found"
	ErrInviteInvalid      = "invite_invalid"
	ErrMembershipRevoked  = "membership_revoked"
	ErrSessionInvalid     = "session_invalid"
	ErrGroupFull          = "group_full"
	ErrRateLimited        = "rate_limited"
	ErrVersionUnsupported = "version_unsupported"
	ErrForbidden          = "forbidden"
	ErrBanned             = "banned"
	ErrInternal           = "internal"
)
