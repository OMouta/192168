// Package protocol holds the definitions shared by the daemon, the control
// server, and (by mirroring) the Windows client: version numbers, wire
// formats, and the JSON shapes exchanged over HTTP, WebSocket, and local IPC.
//
// Anything that crosses a process or machine boundary belongs here, so that a
// change to a message shape is a change to one package rather than three.
package protocol

// Name is the project identifier used in paths, adapter names, and log scopes.
const Name = "192168"

// CodeName is the same name for the places that cannot begin with a digit:
// environment variables, C# namespaces, and Go identifiers.
const CodeName = "net192168"

// Protocol versions. Each channel is versioned independently because a
// self-hosted server, a shipped client, and a peer's daemon are all upgraded
// on their own schedule.
const (
	// DiscoveryVersion is the version of the server discovery document.
	DiscoveryVersion = 1

	// TransportVersion is the version of the peer-to-peer UDP wire format.
	TransportVersion = 1

	// IPCVersion is the version of the local client <-> daemon protocol.
	IPCVersion = 1

	// RealtimeVersion is the version of the coordination WebSocket protocol.
	RealtimeVersion = 1
)

// WellKnownPath is the unauthenticated endpoint every compatible server
// exposes so one shipped client can talk to any deployment.
const WellKnownPath = "/.well-known/" + Name

// EnvPrefix prefixes every environment variable read by the daemon and the
// server.
const EnvPrefix = "NET192168_"
