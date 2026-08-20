package api

import (
	"net/http"
	"net/netip"

	"github.com/OMouta/192168/protocol/api"
	"github.com/OMouta/192168/server/storage"
)

// handleCreateSession connects a device to a group and assigns it a virtual IP.
// The response carries the peers already online, which is the list the daemon
// starts punching toward.
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request, device storage.Device) {
	groupID := r.PathValue("groupId")

	membership, err := s.store.Membership(r.Context(), groupID, device.ID)
	if err != nil {
		s.fail(w, r, err, api.ErrMembershipRevoked, "You are no longer a member of that group.")
		return
	}

	session, err := s.store.CreateSession(r.Context(), membership)
	if err != nil {
		s.fail(w, r, err, api.ErrGroupNotFound, "That group no longer exists.")
		return
	}

	peers, err := s.store.PeersInGroup(r.Context(), groupID, device.ID)
	if err != nil {
		s.fail(w, r, err, api.ErrInternal, "")
		return
	}

	s.log.Info("session opened",
		"sessionId", session.ID, "groupId", groupID, "deviceId", device.ID, "virtualIp", session.VirtualIP)

	// The peers already online learn about this one. Their own list came from
	// their connect call, which happened before this device existed to them.
	s.hub.Broadcast(groupID, device.ID, api.EventPeerOnline, api.PeerOnlineData{
		Peer: api.Peer{
			DeviceID:     device.ID,
			Nickname:     membership.Nickname,
			VirtualIP:    session.VirtualIP,
			TransportKey: device.TransportKey,
		},
	})

	writeJSON(w, http.StatusCreated, api.CreateSessionResponse{
		SessionID: session.ID,
		VirtualIP: session.VirtualIP,
		Peers:     toPeers(peers),
	})
}

// handleSetEndpoint records where this session can be reached, after the daemon
// has asked STUN. It is called again whenever a NAT mapping changes.
func (s *Server) handleSetEndpoint(w http.ResponseWriter, r *http.Request, device storage.Device) {
	session, ok := s.ownedSession(w, r, device)
	if !ok {
		return
	}

	var req api.Endpoint
	if !decode(w, r, &req) {
		return
	}
	if req.Protocol != "" && req.Protocol != "udp" {
		writeError(w, http.StatusBadRequest, api.ErrBadRequest, "Only UDP endpoints are supported.")
		return
	}
	if _, err := netip.ParseAddr(req.Address); err != nil || req.Port < 1 || req.Port > 65535 {
		writeError(w, http.StatusBadRequest, api.ErrBadRequest, "That endpoint is not a valid address and port.")
		return
	}

	if err := s.store.SetSessionEndpoint(r.Context(), session.ID, req.Address, req.Port); err != nil {
		s.fail(w, r, err, api.ErrSessionInvalid, "That session is no longer active.")
		return
	}

	// A changed mapping is why a peer stops being reachable, so this has to get
	// to the group promptly or everyone keeps punching at a dead address.
	s.hub.Broadcast(session.GroupID, device.ID, api.EventPeerEndpointUpdated, api.PeerEndpointUpdatedData{
		DeviceID: device.ID,
		Endpoint: api.Endpoint{Protocol: "udp", Address: req.Address, Port: req.Port},
	})

	w.WriteHeader(http.StatusNoContent)
}

// handleHeartbeat keeps a session alive. Without it a client that dropped off
// would stay in the peer list until someone noticed.
func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request, device storage.Device) {
	session, ok := s.ownedSession(w, r, device)
	if !ok {
		return
	}
	if err := s.store.TouchSession(r.Context(), session.ID); err != nil {
		s.fail(w, r, err, api.ErrSessionInvalid, "That session is no longer active.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteSession disconnects, freeing the virtual IP for the next person.
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request, device storage.Device) {
	session, ok := s.ownedSession(w, r, device)
	if !ok {
		return
	}
	if err := s.store.DeleteSession(r.Context(), session.ID); err != nil {
		s.fail(w, r, err, api.ErrSessionInvalid, "That session is no longer active.")
		return
	}
	s.log.Info("session closed", "sessionId", session.ID, "deviceId", device.ID)
	s.hub.Broadcast(session.GroupID, device.ID, api.EventPeerOffline, api.PeerOfflineData{DeviceID: device.ID})
	w.WriteHeader(http.StatusNoContent)
}

// ownedSession loads the session named in the path and checks it belongs to the
// caller. Someone else's session ID gets the same answer as one that never
// existed.
func (s *Server) ownedSession(w http.ResponseWriter, r *http.Request, device storage.Device) (storage.Session, bool) {
	session, err := s.store.SessionByID(r.Context(), r.PathValue("sessionId"))
	if err != nil {
		s.fail(w, r, err, api.ErrSessionInvalid, "That session is no longer active.")
		return storage.Session{}, false
	}
	if session.DeviceID != device.ID {
		writeError(w, http.StatusNotFound, api.ErrSessionInvalid, "That session is no longer active.")
		return storage.Session{}, false
	}
	return session, true
}

func toPeers(peers []storage.SessionPeer) []api.Peer {
	out := make([]api.Peer, 0, len(peers))
	for _, p := range peers {
		peer := api.Peer{
			DeviceID:     p.DeviceID,
			Nickname:     p.Nickname,
			VirtualIP:    p.VirtualIP,
			TransportKey: p.TransportKey,
		}
		if p.Endpoint != nil {
			peer.Endpoint = &api.Endpoint{Protocol: "udp", Address: p.Endpoint.Address, Port: p.Endpoint.Port}
		}
		out = append(out, peer)
	}
	return out
}
