package ipc

import (
	"encoding/json"

	"github.com/OMouta/192168/protocol/api"
)

// Params and results for every method in Method. A method with no entry here
// takes no parameters, returns nothing, or both.
//
// Group passwords cross this boundary in the clear. The daemon owns all
// cryptography, so it is the side that derives the proof, and the client never
// implements a KDF. That makes the pipe itself a secret-bearing channel, and it
// has to be restricted to the current user.

// CreateGroupParams creates a group and joins it in one step, with the look it
// is made with.
type CreateGroupParams struct {
	Name     string `json:"name"`
	Password string `json:"password"`
	Nickname string `json:"nickname"`
	Icon     string `json:"icon,omitempty"`
	Color    string `json:"color,omitempty"`
}

// JoinGroupParams joins an existing group by name or ID.
type JoinGroupParams struct {
	Group    string `json:"group"`
	Password string `json:"password"`
	Nickname string `json:"nickname"`
}

// GroupResult is what CreateGroup and JoinGroup return.
type GroupResult struct {
	Group Group `json:"group"`
}

// GroupParams identifies a saved group, for ConnectGroup and LeaveGroup.
type GroupParams struct {
	GroupID string `json:"groupId"`
}

// SetNicknameParams changes the nickname for one group. Nicknames are per
// group, so this does not touch the others.
type SetNicknameParams struct {
	GroupID  string `json:"groupId"`
	Nickname string `json:"nickname"`
}

// GetGroupsResult lists every saved membership.
type GetGroupsResult struct {
	Groups []Group `json:"groups"`
}

// ServerParams carries a server base URL, for SetServer and TestServer.
type ServerParams struct {
	URL string `json:"url"`
}

// GetServerResult reports which server the daemon is configured to use.
type GetServerResult struct {
	URL string `json:"url"`
}

// LanDiscoveryResult reports whether LAN discovery is on. Both GetLanDiscovery
// and SetLanDiscovery return it, so a client always renders what the daemon
// actually settled on rather than what it asked for.
type LanDiscoveryResult struct {
	Enabled bool `json:"enabled"`
}

// LanDiscoveryParams turns LAN discovery on or off.
type LanDiscoveryParams struct {
	Enabled bool `json:"enabled"`
}

// TestServerResult is what the Settings screen shows after a connection test.
// Reachable false with a Message is the normal way a test fails, rather than an
// error response, because the user asked a question and got an answer.
type TestServerResult struct {
	Reachable bool         `json:"reachable"`
	Version   int          `json:"version,omitempty"`
	Features  api.Features `json:"features,omitzero"`
	Message   string       `json:"message,omitempty"`
}

// Data payloads for every event in EventName. StateChanged carries a whole
// State, so a client that misses events can still be brought back in line.

// GroupConnectedData accompanies EventGroupConnected.
type GroupConnectedData struct {
	GroupID   string `json:"groupId"`
	GroupName string `json:"groupName"`
	VirtualIP string `json:"virtualIp"`
}

// GroupDisconnectedData accompanies EventGroupDisconnected. Reason is empty
// when the user asked for it and set when something else caused it.
type GroupDisconnectedData struct {
	GroupID string `json:"groupId"`
	Reason  string `json:"reason,omitempty"`
}

// PeerAddedData accompanies EventPeerAdded.
type PeerAddedData struct {
	Peer PeerView `json:"peer"`
}

// PeerRemovedData accompanies EventPeerRemoved.
type PeerRemovedData struct {
	DeviceID string `json:"deviceId"`
}

// PeerStateChangedData accompanies EventPeerStateChanged.
type PeerStateChangedData struct {
	DeviceID string     `json:"deviceId"`
	State    PeerState  `json:"state"`
	Reason   PeerReason `json:"reason,omitempty"`
}

// PeerLatencyChangedData accompanies EventPeerLatencyChanged.
type PeerLatencyChangedData struct {
	DeviceID  string `json:"deviceId"`
	LatencyMS int    `json:"latencyMs"`
}

// ServerConnectionChangedData accompanies EventServerConnectionChanged. Losing
// the server does not drop established peer sessions, so this is a status line
// rather than a failure.
type ServerConnectionChangedData struct {
	Online  bool   `json:"online"`
	Message string `json:"message,omitempty"`
}

// FatalErrorData accompanies EventFatalError, for the failures that end the
// session rather than one peer's link.
type FatalErrorData struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// UnmarshalParams decodes a request's parameters into v. Absent parameters
// leave v untouched, so a method that takes none needs no special case.
func (r Request) UnmarshalParams(v any) error {
	if len(r.Params) == 0 {
		return nil
	}
	return json.Unmarshal(r.Params, v)
}

// UnmarshalData decodes an event's payload into v.
func (e Event) UnmarshalData(v any) error {
	if len(e.Data) == 0 {
		return nil
	}
	return json.Unmarshal(e.Data, v)
}

// MemberParams names one person in one group, for removing them or handing the
// group over to them.
type MemberParams struct {
	GroupID  string `json:"groupId"`
	DeviceID string `json:"deviceId"`
}

// PeerParams names one person on the connected network. No group, because only
// one is connected and a link belongs to that one.
type PeerParams struct {
	DeviceID string `json:"deviceId"`
}

// RenameGroupParams changes what a group is called, for everyone in it.
type RenameGroupParams struct {
	GroupID string `json:"groupId"`
	Name    string `json:"name"`
}

// SetGroupAppearanceParams changes the icon and colour a group is shown with,
// for everyone in it.
type SetGroupAppearanceParams struct {
	GroupID string `json:"groupId"`
	Icon    string `json:"icon"`
	Color   string `json:"color"`
}

// SetGroupPasswordParams changes the password a new member joins with. It
// removes nobody: the password is only ever checked at the door.
type SetGroupPasswordParams struct {
	GroupID  string `json:"groupId"`
	Password string `json:"password"`
}
