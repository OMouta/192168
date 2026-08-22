package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/OMouta/192168/protocol/api"
	"github.com/OMouta/192168/protocol/invite"
	"github.com/OMouta/192168/server/storage"
)

// handleJoinByCode puts the caller in whichever group a code opens.
//
// Every failure gets the same answer. A code that never existed, one that has
// been replaced, and one for a group this device was removed from all look
// alike from outside.
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
		writeError(w, http.StatusForbidden, api.ErrInviteInvalid, "That invite no longer works.")
		return
	}
	if err != nil {
		s.fail(w, r, err, api.ErrInternal, "")
		return
	}

	membership, err := s.store.AddMembership(r.Context(), group, device.ID)
	if errors.Is(err, storage.ErrBanned) {
		writeError(w, http.StatusForbidden, api.ErrInviteInvalid, "That invite no longer works.")
		return
	}
	if err != nil {
		s.fail(w, r, err, api.ErrInternal, "")
		return
	}

	s.log.Info("group joined by invite", "groupId", group.ID, "deviceId", device.ID)
	writeJSON(w, http.StatusOK, toMembership(membership))
}

// handleInvite says what a code opens, without joining anything. No token: the
// landing page a link opens has none, and holding the code is the
// authorization.
func (s *Server) handleInvite(w http.ResponseWriter, r *http.Request) {
	// Unauthenticated, so it shares the join limiter.
	if !s.joins.allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, api.ErrRateLimited, "Too many attempts. Wait a moment and try again.")
		return
	}

	found, err := s.findInvite(r.Context(), r.PathValue("code"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, api.ErrInviteInvalid, "That invite no longer works.")
		return
	}
	if err != nil {
		s.fail(w, r, err, api.ErrInternal, "")
		return
	}
	writeJSON(w, http.StatusOK, found)
}

// findInvite resolves a code to what it opens. The API and the landing page
// share it, so a bad code means the same thing to both.
func (s *Server) findInvite(ctx context.Context, raw string) (api.Invite, error) {
	code := invite.Parse(raw)
	if code == "" {
		return api.Invite{}, storage.ErrNotFound
	}

	group, err := s.store.GroupByInviteCode(ctx, code)
	if err != nil {
		return api.Invite{}, err
	}
	members, err := s.store.MemberCount(ctx, group.ID)
	if err != nil {
		return api.Invite{}, err
	}

	return api.Invite{
		Code:       group.InviteCode,
		GroupName:  group.Name,
		GroupIcon:  group.Icon,
		GroupColor: group.Color,
		Members:    members,
	}, nil
}

// handleResetInvite replaces a group's code. The old one stops working.
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
