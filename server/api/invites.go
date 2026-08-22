package api

import (
	"errors"
	"net/http"

	"github.com/OMouta/192168/protocol/api"
	"github.com/OMouta/192168/protocol/invite"
	"github.com/OMouta/192168/server/storage"
)

// handleJoinByCode puts the caller in whichever group a code opens.
//
// Every way this can fail gets the same answer. A code that never existed, one
// that has been replaced, and one belonging to a group this device was removed
// from are indistinguishable from outside, so none of them is a way to learn
// anything.
func (s *Server) handleJoinByCode(w http.ResponseWriter, r *http.Request, device storage.Device) {
	var req api.JoinByCodeRequest
	if !decode(w, r, &req) {
		return
	}

	code := invite.Parse(req.Code)
	if code == "" {
		writeError(w, http.StatusBadRequest, api.ErrBadRequest, "That does not look like an invite.")
		return
	}
	if !s.joins.allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, api.ErrRateLimited, "Too many attempts. Wait a moment and try again.")
		return
	}

	group, err := s.store.GroupByInviteCode(r.Context(), code)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusForbidden, api.ErrInviteInvalid, "That invite is not good any more.")
		return
	}
	if err != nil {
		s.fail(w, r, err, api.ErrInternal, "")
		return
	}

	membership, err := s.store.AddMembership(r.Context(), group, device.ID)
	if errors.Is(err, storage.ErrBanned) {
		writeError(w, http.StatusForbidden, api.ErrInviteInvalid, "That invite is not good any more.")
		return
	}
	if err != nil {
		s.fail(w, r, err, api.ErrInternal, "")
		return
	}

	s.log.Info("group joined by invite", "groupId", group.ID, "deviceId", device.ID)
	writeJSON(w, http.StatusOK, toMembership(membership))
}

// handleInvite says what a code opens, without joining anything.
//
// It takes no token: the landing page a link opens has none, and the app uses
// it to show what is about to be joined. Holding the code is the whole of the
// authorization, which is what a code is for.
func (s *Server) handleInvite(w http.ResponseWriter, r *http.Request) {
	preview, ok := s.lookUpInvite(w, r, r.PathValue("code"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

// lookUpInvite resolves a code to what it opens, answering the request itself
// when it cannot. The landing page and the API both go through here, so an
// invalid code says the same thing either way.
func (s *Server) lookUpInvite(w http.ResponseWriter, r *http.Request, raw string) (api.Invite, bool) {
	code := invite.Parse(raw)
	if code == "" {
		writeError(w, http.StatusNotFound, api.ErrInviteInvalid, "That invite is not good any more.")
		return api.Invite{}, false
	}
	// Unauthenticated and cheap to ask, so it is limited like joining is.
	if !s.joins.allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, api.ErrRateLimited, "Too many attempts. Wait a moment and try again.")
		return api.Invite{}, false
	}

	group, err := s.store.GroupByInviteCode(r.Context(), code)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, api.ErrInviteInvalid, "That invite is not good any more.")
		return api.Invite{}, false
	}
	if err != nil {
		s.fail(w, r, err, api.ErrInternal, "")
		return api.Invite{}, false
	}

	members, err := s.store.MemberCount(r.Context(), group.ID)
	if err != nil {
		s.fail(w, r, err, api.ErrInternal, "")
		return api.Invite{}, false
	}

	return api.Invite{
		Code:       group.InviteCode,
		GroupName:  group.Name,
		GroupIcon:  group.Icon,
		GroupColor: group.Color,
		Members:    members,
	}, true
}

// handleResetInvite replaces a group's code, which is what makes handing one
// out safe: a code that reached the wrong person stops working.
func (s *Server) handleResetInvite(w http.ResponseWriter, r *http.Request, device storage.Device) {
	groupID := r.PathValue("groupId")

	code, err := s.store.ResetInviteCode(r.Context(), groupID, device.ID)
	if err != nil {
		s.fail(w, r, err, api.ErrGroupNotFound, "You are not a member of that group.")
		return
	}

	s.log.Info("invite code reset", "groupId", groupID, "deviceId", device.ID)
	writeJSON(w, http.StatusOK, api.InviteCodeResponse{Code: code})
}
