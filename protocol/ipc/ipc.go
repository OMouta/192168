// Package ipc describes the local control channel between the Windows client
// and the daemon, carried over a named pipe.
//
// The daemon is the source of truth for connection state. The client sends
// high-level intents ("connect to this group") and renders the state it is
// told about; it never drives sockets, routes, or NAT traversal itself.
package ipc

import "encoding/json"

// PipeName is the named pipe the daemon listens on.
const PipeName = `\\.\pipe\192168`

// Messages are newline-delimited JSON, and responses and events share the one
// stream. They do not take turns: an event can arrive between a request and its
// response, so a client that reads the next line and calls it the answer will
// get an event instead.
//
// Tell them apart by field. A response has "id", an event has "event". Match
// responses to requests by that id rather than by arrival order.

// Method is a client -> daemon request.
type Method string

const (
	MethodGetState    Method = "GetState"
	MethodGetGroups   Method = "GetGroups"
	MethodCreateGroup Method = "CreateGroup"
	MethodJoinGroup   Method = "JoinGroup"
	MethodLeaveGroup  Method = "LeaveGroup"
	MethodConnect     Method = "ConnectGroup"
	MethodDisconnect  Method = "Disconnect"
	MethodSetNickname Method = "SetNickname"

	// Managing a group. All of these are the owner's alone, and the server is
	// what says so: the app hides them from everyone else, and hiding is not
	// enforcing.
	MethodRemoveMember       Method = "RemoveMember"
	MethodRenameGroup        Method = "RenameGroup"
	MethodSetGroupAppearance Method = "SetGroupAppearance"
	MethodSetGroupPassword   Method = "SetGroupPassword"
	MethodTransferOwnership  Method = "TransferOwnership"
	MethodDeleteGroup        Method = "DeleteGroup"

	MethodGetServer  Method = "GetServer"
	MethodSetServer  Method = "SetServer"
	MethodTestServer Method = "TestServer"
	// LAN discovery: replicating broadcast and multicast across the group, and
	// pointing this machine's multicast at the tunnel while connected. It has
	// a cost outside the app, so the user decides.
	MethodGetLanDiscovery Method = "GetLanDiscovery"
	MethodSetLanDiscovery Method = "SetLanDiscovery"

	// MethodResetSettings puts local settings back to their defaults. The
	// daemon owns those defaults, so the client asks rather than assuming.
	MethodResetSettings Method = "ResetSettings"
)

// Request is one client call. ID correlates the response; it is opaque to the
// daemon.
type Request struct {
	ID     string          `json:"id"`
	Method Method          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response answers exactly one Request. Err is set when OK is false.
type Response struct {
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Err    *Error          `json:"error,omitempty"`
}

// Error carries a user-presentable failure. Socket codes and other diagnostics
// go to the daemon log, not here.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// EventName is a daemon -> client push.
type EventName string

const (
	EventStateChanged            EventName = "StateChanged"
	EventGroupConnected          EventName = "GroupConnected"
	EventGroupDisconnected       EventName = "GroupDisconnected"
	EventPeerAdded               EventName = "PeerAdded"
	EventPeerRemoved             EventName = "PeerRemoved"
	EventPeerStateChanged        EventName = "PeerStateChanged"
	EventPeerLatencyChanged      EventName = "PeerLatencyChanged"
	EventServerConnectionChanged EventName = "ServerConnectionChanged"
	EventFatalError              EventName = "FatalError"
)

// Event is an unsolicited daemon message. Clients ignore names they do not
// know so the daemon can add events without breaking an older client.
type Event struct {
	Event EventName       `json:"event"`
	Data  json.RawMessage `json:"data,omitempty"`
}
