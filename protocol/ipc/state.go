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

// PeerReason says what is standing in the way of a link. It is a key rather
// than a sentence, because the wording belongs to the app. Empty means nothing
// is in the way, which is why there is no constant for it.
//
// Only the cases a person can tell apart are here. The daemon knows more than
// this and it goes in the log, which is where a fault nobody can act on
// belongs.
type PeerReason string

const (
	// ReasonNoAddress means they are in the group and online, but nobody has
	// been told where to reach them yet. Ordinary for a second or two after
	// somebody connects, and a wait with no end after that.
	ReasonNoAddress PeerReason = "noAddress"
	// ReasonNoRoute means the attempt is over: neither network would let a
	// direct connection through, and nobody else in the group could carry it.
	ReasonNoRoute PeerReason = "noRoute"
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
	// Nickname is what this device is called, everywhere. It is here rather
	// than on each group, and it is there whether or not one is connected.
	Nickname  string `json:"nickname,omitempty"`
	VirtualIP string `json:"virtualIp,omitempty"`
	// IsOwner says whether this device runs the connected group, which is what
	// decides whether the managing parts of the screen are there at all.
	IsOwner bool       `json:"isOwner,omitempty"`
	Peers   []PeerView `json:"peers"`
	// PacketsSent and PacketsReceived are counted at the adapter rather than per
	// link, so they are not the sum of the rows. A packet addressed to the whole
	// LAN is one here and one on every link.
	PacketsSent     uint64 `json:"packetsSent,omitempty"`
	PacketsReceived uint64 `json:"packetsReceived,omitempty"`
	Message         string `json:"message,omitempty"`
}

// PeerView is one row of the active group screen.
type PeerView struct {
	DeviceID  string    `json:"deviceId"`
	Nickname  string    `json:"nickname"`
	VirtualIP string    `json:"virtualIp"`
	State     PeerState `json:"state"`
	LatencyMS *int      `json:"latencyMs,omitempty"`
	// Reason is what is in the way, empty for a link with nothing wrong with it.
	Reason PeerReason `json:"reason,omitempty"`
	// IsOwner marks whoever runs the group, so the list says who to ask.
	IsOwner bool `json:"isOwner,omitempty"`
	// PacketsSent and PacketsReceived count the game's packets and not the
	// handshakes or keepalives, so zero on an open link means nothing is using
	// it.
	PacketsSent     uint64 `json:"packetsSent,omitempty"`
	PacketsReceived uint64 `json:"packetsReceived,omitempty"`
}

// Group is one saved membership as shown on the groups screen.
type Group struct {
	GroupID string `json:"groupId"`
	Name    string `json:"name"`
	// Icon and Color are keys the client maps to a glyph and a colour. Empty
	// means the default look.
	Icon          string `json:"icon,omitempty"`
	Color         string `json:"color,omitempty"`
	Active        bool   `json:"active"`
	OnlineMembers *int   `json:"onlineMembers,omitempty"`
	// InviteCode is the group's way in, and it is only here for a group this
	// device owns. The server decides that, not the app.
	InviteCode string `json:"inviteCode,omitempty"`
	// InviteLink is that code as the address to send somebody, which is the
	// thing an owner actually shares. Empty where the server did not say where
	// its links live.
	InviteLink string `json:"inviteLink,omitempty"`
	// IsOwner marks the groups this device runs, so the list says which are
	// yours to change and which you are only in.
	IsOwner bool `json:"isOwner,omitempty"`
}
