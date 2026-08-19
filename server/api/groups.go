package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/OMouta/192168/protocol/api"
	"github.com/OMouta/192168/protocol/auth"
	"github.com/OMouta/192168/server/storage"
)

const maxNameLength = 64

// handleCreateGroup creates a group and makes the caller its first member.
func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request, device storage.Device) {
	var req api.CreateGroupRequest
	if !decode(w, r, &req) {
		return
	}

	name := strings.TrimSpace(req.Name)
	nickname := strings.TrimSpace(req.Nickname)
	if name == "" || len(name) > maxNameLength || req.PasswordProof == "" || nickname == "" {
		writeError(w, http.StatusBadRequest, api.ErrBadRequest, "A group needs a name, a password, and a nickname.")
		return
	}

	verifier, err := auth.NewGroupVerifier(req.PasswordProof)
	if err != nil {
		s.fail(w, r, err, api.ErrInternal, "")
		return
	}
	id, err := storage.NewID("grp")
	if err != nil {
		s.fail(w, r, err, api.ErrInternal, "")
		return
	}

	membership, err := s.store.CreateGroup(r.Context(), storage.Group{
		ID:               id,
		Name:             name,
		PasswordVerifier: verifier,
		Subnet:           DefaultSubnet,
	}, auth.NormalizeGroupName(name), device.ID, nickname)
	if errors.Is(err, storage.ErrConflict) {
		writeError(w, http.StatusConflict, api.ErrGroupNameTaken, "A group with that name already exists.")
		return
	}
	if err != nil {
		s.fail(w, r, err, api.ErrInternal, "")
		return
	}

	s.log.Info("group created", "groupId", membership.GroupID, "deviceId", device.ID)
	writeJSON(w, http.StatusCreated, toMembership(membership))
}

// handleJoinGroup adds the caller to an existing group.
//
// A wrong name and a wrong password get the same answer. Telling them apart
// would turn this endpoint into a way to find out which groups exist.
func (s *Server) handleJoinGroup(w http.ResponseWriter, r *http.Request, device storage.Device) {
	var req api.JoinGroupRequest
	if !decode(w, r, &req) {
		return
	}

	name := strings.TrimSpace(req.Group)
	nickname := strings.TrimSpace(req.Nickname)
	if name == "" || req.PasswordProof == "" || nickname == "" {
		writeError(w, http.StatusBadRequest, api.ErrBadRequest, "Joining needs a group, a password, and a nickname.")
		return
	}

	// Guessing has to be slow, and the limit is per caller rather than per
	// group so one attacker cannot lock everyone else out of a group.
	if !s.joins.allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, api.ErrRateLimited, "Too many attempts. Wait a moment and try again.")
		return
	}

	group, err := s.store.GroupByName(r.Context(), auth.NormalizeGroupName(name))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusForbidden, api.ErrInvalidPassword, "That group name or password is not right.")
		return
	}
	if err != nil {
		s.fail(w, r, err, api.ErrInternal, "")
		return
	}

	ok, err := auth.VerifyGroupProof(group.PasswordVerifier, req.PasswordProof)
	if err != nil {
		s.fail(w, r, err, api.ErrInternal, "")
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, api.ErrInvalidPassword, "That group name or password is not right.")
		return
	}

	membership, err := s.store.AddMembership(r.Context(), group, device.ID, nickname)
	if err != nil {
		s.fail(w, r, err, api.ErrInternal, "")
		return
	}

	s.log.Info("group joined", "groupId", group.ID, "deviceId", device.ID)
	writeJSON(w, http.StatusOK, toMembership(membership))
}

// handleListGroups returns the groups this device belongs to, which is how a
// reinstalled client gets its list back.
func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request, device storage.Device) {
	memberships, err := s.store.MembershipsByDevice(r.Context(), device.ID)
	if err != nil {
		s.fail(w, r, err, api.ErrInternal, "")
		return
	}

	out := make([]api.Membership, 0, len(memberships))
	for _, m := range memberships {
		out = append(out, toMembership(m))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleLeaveGroup removes the caller from a group and ends its session there.
func (s *Server) handleLeaveGroup(w http.ResponseWriter, r *http.Request, device storage.Device) {
	groupID := r.PathValue("groupId")
	if err := s.store.RevokeMembership(r.Context(), groupID, device.ID); err != nil {
		s.fail(w, r, err, api.ErrGroupNotFound, "You are not a member of that group.")
		return
	}
	s.log.Info("group left", "groupId", groupID, "deviceId", device.ID)
	w.WriteHeader(http.StatusNoContent)
}

// handleSetNickname changes the caller's nickname in one group. Nicknames are
// per group, so this leaves the others alone.
func (s *Server) handleSetNickname(w http.ResponseWriter, r *http.Request, device storage.Device) {
	var req struct {
		Nickname string `json:"nickname"`
	}
	if !decode(w, r, &req) {
		return
	}

	nickname := strings.TrimSpace(req.Nickname)
	if nickname == "" || len(nickname) > maxNameLength {
		writeError(w, http.StatusBadRequest, api.ErrBadRequest, "That nickname will not work.")
		return
	}

	if err := s.store.SetNickname(r.Context(), r.PathValue("groupId"), device.ID, nickname); err != nil {
		s.fail(w, r, err, api.ErrGroupNotFound, "You are not a member of that group.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toMembership(m storage.Membership) api.Membership {
	return api.Membership{
		MembershipID: m.ID,
		GroupID:      m.GroupID,
		GroupName:    m.GroupName,
		Nickname:     m.Nickname,
		Subnet:       m.Subnet,
	}
}
