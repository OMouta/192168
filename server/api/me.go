package api

import (
	"net/http"
	"strings"

	"github.com/OMouta/192168/protocol/api"
	"github.com/OMouta/192168/server/storage"
)

// handleMe reports what this device is called. A client that has lost its local
// copy, which is what upgrading from per-group nicknames looks like, gets the
// name back from here rather than reverting the person to their machine name.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, device storage.Device) {
	writeJSON(w, http.StatusOK, api.Me{DeviceID: device.ID, Nickname: device.Nickname})
}

// handleSetNickname changes what this device is called, in every group at once.
//
// It also serves the old per-group route, which ignores the group in the path.
// An older client renaming itself somewhere means the same thing it does here,
// and refusing would break a working app for the sake of a URL.
func (s *Server) handleSetNickname(w http.ResponseWriter, r *http.Request, device storage.Device) {
	var req api.SetNicknameRequest
	if !decode(w, r, &req) {
		return
	}

	nickname := strings.TrimSpace(req.Nickname)
	if nickname == "" || len(nickname) > maxNameLength {
		writeError(w, http.StatusBadRequest, api.ErrBadRequest, "That nickname will not work.")
		return
	}

	if err := s.store.SetDeviceNickname(r.Context(), device.ID, nickname); err != nil {
		s.fail(w, r, err, api.ErrUnauthorized, "This device is not signed in.")
		return
	}
	s.announceNickname(r, device.ID, nickname)

	w.WriteHeader(http.StatusNoContent)
}

// adoptNickname takes the name an older client still sends when it creates or
// joins a group and makes it this device's name. Those clients believe a
// nickname is picked per group, and this is what keeps them working: the name
// they typed is the one they see back.
//
// A new client sends nothing here, and the name the device already has stands.
// Reports whether the caller should carry on.
func (s *Server) adoptNickname(w http.ResponseWriter, r *http.Request, device storage.Device, requested string) bool {
	nickname := strings.TrimSpace(requested)
	if nickname == "" || nickname == device.Nickname {
		return true
	}
	if len(nickname) > maxNameLength {
		writeError(w, http.StatusBadRequest, api.ErrBadRequest, "That nickname will not work.")
		return false
	}
	if err := s.store.SetDeviceNickname(r.Context(), device.ID, nickname); err != nil {
		s.fail(w, r, err, api.ErrUnauthorized, "This device is not signed in.")
		return false
	}
	s.announceNickname(r, device.ID, nickname)
	return true
}

// announceNickname tells everyone who can currently see this person that they
// are called something else now. Without it the new name only arrived with the
// next peer list, so everyone else kept the old one until they reconnected.
//
// A name is the device's, so this goes to every group the device is connected
// in rather than to one of them.
func (s *Server) announceNickname(r *http.Request, deviceID, nickname string) {
	groupIDs, err := s.store.ConnectedGroupIDs(r.Context(), deviceID)
	if err != nil {
		// The rename itself worked. Telling the group is worth a log line and
		// not worth failing a request that already took effect.
		s.log.Error("could not announce a rename", "deviceId", deviceID, "error", err)
		return
	}
	for _, groupID := range groupIDs {
		s.hub.Broadcast(groupID, deviceID, api.EventPeerRenamed, api.PeerRenamedData{
			DeviceID: deviceID,
			Nickname: nickname,
		})
	}
}
