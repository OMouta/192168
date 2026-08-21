package api

import "encoding/json"

// EventType identifies a coordination WebSocket message. The channel carries
// presence and endpoint changes so the daemon never has to poll.
type EventType string

const (
	EventPeerOnline          EventType = "peer_online"
	EventPeerOffline         EventType = "peer_offline"
	EventPeerEndpointUpdated EventType = "peer_endpoint_updated"
	EventPeerRenamed         EventType = "peer_renamed"
	EventMembershipRevoked   EventType = "membership_revoked"
	EventGroupUpdated        EventType = "group_updated"
	EventGroupDeleted        EventType = "group_deleted"
	EventSessionInvalidated  EventType = "session_invalidated"
)

// Event is the envelope for every realtime message. Data is decoded according
// to Type; unknown types are ignored so that a newer server can add events
// without breaking older daemons.
type Event struct {
	Type EventType       `json:"type"`
	Data json.RawMessage `json:"data"`
}

// PeerOnlineData accompanies EventPeerOnline.
type PeerOnlineData struct {
	Peer Peer `json:"peer"`
}

// PeerOfflineData accompanies EventPeerOffline.
type PeerOfflineData struct {
	DeviceID string `json:"deviceId"`
}

// GroupUpdatedData accompanies EventGroupUpdated. Only the owner can cause one,
// and everybody in the group needs to hear it.
//
// It describes the group as it now stands rather than only what changed.
type GroupUpdatedData struct {
	GroupID string `json:"groupId"`
	Name    string `json:"name"`
	Icon    string `json:"icon,omitempty"`
	Color   string `json:"color,omitempty"`
}

// GroupDeletedData accompanies EventGroupDeleted. The group is gone, so anyone
// connected to it is disconnected and it leaves their list.
type GroupDeletedData struct {
	GroupID string `json:"groupId"`
}

// PeerRenamedData accompanies EventPeerRenamed. A nickname is what everyone
// else sees, so a change to it has to reach them without a reconnect.
type PeerRenamedData struct {
	DeviceID string `json:"deviceId"`
	Nickname string `json:"nickname"`
}

// PeerEndpointUpdatedData accompanies EventPeerEndpointUpdated. A peer's NAT
// mapping can change at any time; the daemon re-punches when it does.
type PeerEndpointUpdatedData struct {
	DeviceID string   `json:"deviceId"`
	Endpoint Endpoint `json:"endpoint"`
}
