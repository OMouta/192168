package api

import (
	"net/http"
	"strings"

	"github.com/OMouta/192168/protocol/api"
	"github.com/OMouta/192168/server/storage"
)

// handleMe reports what this device is called. A client that has lost its local
// copy reads the name back from here instead of falling back to the hostname.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, device storage.Device) {
	writeJSON(w, http.StatusOK, api.Me{DeviceID: device.ID, Nickname: device.Nickname})
}

// handleSetNickname changes what this device is called, in every group at once.
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

// announceNickname pushes a rename to everyone who can see this person now.
// Without it the new name only arrived with the next peer list. A name belongs
// to the device, so this goes to every group it is connected in.
func (s *Server) announceNickname(r *http.Request, deviceID, nickname string) {
	groupIDs, err := s.store.ConnectedGroupIDs(r.Context(), deviceID)
	if err != nil {
		// The rename worked. Failing the request now would be a lie.
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
