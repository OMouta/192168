package ipc

// ConnectionState is the daemon's overall status. Exactly one group session
// can be active at a time.
type ConnectionState string

const (
	StateDisconnected  ConnectionState = "disconnected"
	StateConnecting    ConnectionState = "connecting"
	StateConnected     ConnectionState = "connected"
	StateDisconnecting ConnectionState = "disconnecting"
	StateError         ConnectionState = "error"
)

// PeerState is the connectivity of one pairwise link. Links are independent:
// failing to reach one peer must not affect the others.
type PeerState string

const (
	// PeerConnecting means NAT traversal or the handshake is in progress.
	PeerConnecting PeerState = "connecting"
	// PeerDirect means an authenticated peer-to-peer UDP session is carrying traffic.
	PeerDirect PeerState = "direct"
	// PeerIndirect is reserved for future peer-assisted or relayed routes.
	PeerIndirect PeerState = "indirect"
	// PeerOffline means the peer has no active session in the group.
	PeerOffline PeerState = "offline"
	// PeerFailed means traversal was attempted and gave up.
	PeerFailed PeerState = "failed"
)

// State is the full snapshot returned by GetState and pushed on StateChanged.
// It is everything the UI needs to render, and nothing more.
type State struct {
	Connection   ConnectionState `json:"connection"`
	ServerURL    string          `json:"serverUrl"`
	ServerOnline bool            `json:"serverOnline"`
	GroupID      string          `json:"groupId,omitempty"`
	GroupName    string          `json:"groupName,omitempty"`
	// GroupIcon and GroupColor are the connected group's look, the same one its
	// row in the list wore.
	GroupIcon  string `json:"groupIcon,omitempty"`
	GroupColor string `json:"groupColor,omitempty"`
	Nickname   string `json:"nickname,omitempty"`
	VirtualIP  string `json:"virtualIp,omitempty"`
	// IsOwner says whether this device runs the connected group, which is what
	// decides whether the managing parts of the screen are there at all.
	IsOwner bool       `json:"isOwner,omitempty"`
	Peers   []PeerView `json:"peers"`
	Message string     `json:"message,omitempty"`
}

// PeerView is one row of the active group screen.
type PeerView struct {
	DeviceID  string    `json:"deviceId"`
	Nickname  string    `json:"nickname"`
	VirtualIP string    `json:"virtualIp"`
	State     PeerState `json:"state"`
	LatencyMS *int      `json:"latencyMs,omitempty"`
	// IsOwner marks whoever runs the group, so the list says who to ask.
	IsOwner bool `json:"isOwner,omitempty"`
}

// Group is one saved membership as shown on the groups screen.
type Group struct {
	GroupID string `json:"groupId"`
	Name    string `json:"name"`
	// Icon and Color are keys the client maps to a glyph and a colour. Empty
	// means the default look.
	Icon          string `json:"icon,omitempty"`
	Color         string `json:"color,omitempty"`
	Nickname      string `json:"nickname"`
	Active        bool   `json:"active"`
	OnlineMembers *int   `json:"onlineMembers,omitempty"`
	// IsOwner marks the groups this device runs, so the list says which are
	// yours to change and which you are only in.
	IsOwner bool `json:"isOwner,omitempty"`
}
