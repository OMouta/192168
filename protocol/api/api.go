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
	Relay    *string  `json:"relay"`
	Features Features `json:"features"`
}

// Features advertises optional server capabilities. Absent or false means the
// client must not depend on them.
type Features struct {
	Relay       bool `json:"relay"`
	PeerRouting bool `json:"peerRouting"`
}

// RegisterDeviceRequest registers this installation's long-lived identity.
// The private half of the key pair never leaves the machine.
type RegisterDeviceRequest struct {
	DeviceID   string `json:"deviceId"`
	PublicKey  string `json:"publicKey"`
	DeviceName string `json:"deviceName"`
}

// CreateGroupRequest creates a new virtual LAN. PasswordProof is derived from
// the group password client-side; the plaintext password is never sent.
type CreateGroupRequest struct {
	Name          string `json:"name"`
	PasswordProof string `json:"passwordProof"`
	Nickname      string `json:"nickname"`
	DeviceID      string `json:"deviceId"`
}

// JoinGroupRequest joins an existing group by name or ID.
type JoinGroupRequest struct {
	Group         string `json:"group"`
	PasswordProof string `json:"passwordProof"`
	Nickname      string `json:"nickname"`
	DeviceID      string `json:"deviceId"`
}

// Membership is the persistent relationship between a device and a group. The
// credential is what lets the daemon reconnect without the group password, so
// it is returned once and stored under OS-backed protection.
type Membership struct {
	MembershipID string `json:"membershipId"`
	GroupID      string `json:"groupId"`
	GroupName    string `json:"groupName"`
	Nickname     string `json:"nickname"`
	Credential   string `json:"credential,omitempty"`
	Subnet       string `json:"subnet"`
}

// CreateSessionResponse is returned when a device connects to a group. The
// virtual IP is unique within the group for the lifetime of the session.
type CreateSessionResponse struct {
	SessionID string `json:"sessionId"`
	VirtualIP string `json:"virtualIp"`
	Peers     []Peer `json:"peers"`
}

// Peer is another device with an active session in the same group.
type Peer struct {
	DeviceID  string    `json:"deviceId"`
	Nickname  string    `json:"nickname"`
	VirtualIP string    `json:"virtualIp"`
	PublicKey string    `json:"publicKey"`
	Endpoint  *Endpoint `json:"endpoint,omitempty"`
}

// Endpoint is a public UDP address candidate discovered via STUN.
type Endpoint struct {
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
}

// Error is the body returned for any non-2xx control-plane response. Code is
// stable and machine-readable; Message is safe to surface to the user.
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
	ErrGroupNameTaken     = "group_name_taken"
	ErrInvalidPassword    = "invalid_password"
	ErrMembershipRevoked  = "membership_revoked"
	ErrSessionInvalid     = "session_invalid"
	ErrGroupFull          = "group_full"
	ErrRateLimited        = "rate_limited"
	ErrVersionUnsupported = "version_unsupported"
	ErrInternal           = "internal"
)
