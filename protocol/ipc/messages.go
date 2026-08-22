package ipc

import (
	"encoding/json"

	"github.com/OMouta/192168/protocol/api"
)

// Params and results for every method in Method. A method with no entry here
// takes no parameters, returns nothing, or both.
//
// Invite codes cross this boundary, and a code is all it takes to get into a
// group, so the pipe stays restricted to the current user.

// CreateGroupParams creates a group and joins it in one step, with the look it
// is made with.
type CreateGroupParams struct {
	Name  string `json:"name"`
	Icon  string `json:"icon,omitempty"`
	Color string `json:"color,omitempty"`
}

// JoinGroupParams joins whichever group an invite opens. Code is whatever was
// pasted, bare or as a link.
type JoinGroupParams struct {
	Code string `json:"code"`
}

// InviteParams asks about a code without acting on it.
type InviteParams struct {
	Code string `json:"code"`
}

// InviteResult is what a code opens. Found is false for a code that opens
// nothing, which is an answer rather than an error. Half-typed codes are
// invalid and that is normal.
type InviteResult struct {
	Found      bool   `json:"found"`
	Code       string `json:"code,omitempty"`
	GroupName  string `json:"groupName,omitempty"`
	GroupIcon  string `json:"groupIcon,omitempty"`
	GroupColor string `json:"groupColor,omitempty"`
	Members    int    `json:"members,omitempty"`
}

// InviteCodeResult is a group's new code and the link to it.
type InviteCodeResult struct {
	Code string `json:"code"`
	Link string `json:"link,omitempty"`
}

// GroupResult is what CreateGroup and JoinGroup return.
type GroupResult struct {
	Group Group `json:"group"`
}

// GroupParams identifies a saved group, for ConnectGroup and LeaveGroup.
type GroupParams struct {
	GroupID string `json:"groupId"`
}

// SetNicknameParams changes what this device is called. One name covers every
// group, so there is no group to name.
type SetNicknameParams struct {
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

// PacketLogResult reports whether the packet log is on, for the same reason
// LanDiscoveryResult does: opening the file can fail, and the client should
// show what happened rather than what it asked for.
type PacketLogResult struct {
	Enabled bool `json:"enabled"`
}

// PacketLogParams turns the packet log on or off.
type PacketLogParams struct {
	Enabled bool `json:"enabled"`
}

// ClearLogsResult names the logs that were emptied, so the app can say what it
// did rather than claiming more than happened. A log that was never written
// is not listed and is not an error.
type ClearLogsResult struct {
	Cleared []string `json:"cleared"`
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
