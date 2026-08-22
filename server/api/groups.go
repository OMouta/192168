package api

import (
	"net/http"
	"strings"

	"github.com/OMouta/192168/protocol/api"
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
	if name == "" || len(name) > maxNameLength {
		writeError(w, http.StatusBadRequest, api.ErrBadRequest, "A group needs a name.")
		return
	}
	if !isAppearanceKey(req.Icon) || !isAppearanceKey(req.Color) {
		writeError(w, http.StatusBadRequest, api.ErrBadRequest, "That icon or colour will not work.")
		return
	}

	id, err := storage.NewID("grp")
	if err != nil {
		s.fail(w, r, err, api.ErrInternal, "")
		return
	}

	membership, err := s.store.CreateGroup(r.Context(), storage.Group{
		ID:     id,
		Name:   name,
		Icon:   req.Icon,
		Color:  req.Color,
		Subnet: DefaultSubnet,
	}, device.ID)
	if err != nil {
		s.fail(w, r, err, api.ErrInternal, "")
		return
	}

	s.log.Info("group created", "groupId", membership.GroupID, "deviceId", device.ID)
	writeJSON(w, http.StatusCreated, toMembership(membership))
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

// handleListMembers lists everyone in a group, connected or not.
//
// Membership is checked first: who belongs to a group is only the business of
// the people in it.
func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request, device storage.Device) {
	groupID := r.PathValue("groupId")
	if _, err := s.store.Membership(r.Context(), groupID, device.ID); err != nil {
		s.fail(w, r, err, api.ErrGroupNotFound, "You are not a member of that group.")
		return
	}

	members, err := s.store.Members(r.Context(), groupID)
	if err != nil {
		s.fail(w, r, err, api.ErrGroupNotFound, "You are not a member of that group.")
		return
	}

	out := make([]api.Member, 0, len(members))
	for _, m := range members {
		out = append(out, api.Member{
			DeviceID:  m.DeviceID,
			Nickname:  m.Nickname,
			VirtualIP: m.VirtualIP,
			Role:      string(m.Role),
			Online:    m.Online,
		})
	}
	writeJSON(w, http.StatusOK, api.MembersResponse{Members: out})
}

// handleLeaveGroup removes the caller from a group and ends its session there.
func (s *Server) handleLeaveGroup(w http.ResponseWriter, r *http.Request, device storage.Device) {
	groupID := r.PathValue("groupId")
	if err := s.store.RevokeMembership(r.Context(), groupID, device.ID); err != nil {
		s.fail(w, r, err, api.ErrGroupNotFound, "You are not a member of that group.")
		return
	}
	s.log.Info("group left", "groupId", groupID, "deviceId", device.ID)

	// Leaving ends the session too, so the group hears the same thing it would
	// hear from a disconnect.
	s.hub.Broadcast(groupID, device.ID, api.EventPeerOffline, api.PeerOfflineData{DeviceID: device.ID})
	s.hub.SendTo(groupID, device.ID, api.EventMembershipRevoked, api.PeerOfflineData{DeviceID: device.ID})

	w.WriteHeader(http.StatusNoContent)
}

// toMembership is what the client is handed for one group. The invite code goes
// only to the owner, and the server is what decides that rather than the app
// choosing not to draw a button.
func toMembership(m storage.Membership) api.Membership {
	code := ""
	if m.Role == storage.RoleOwner {
		code = m.InviteCode
	}
	return api.Membership{
		MembershipID:  m.ID,
		GroupID:       m.GroupID,
		GroupName:     m.GroupName,
		GroupIcon:     m.GroupIcon,
		GroupColor:    m.GroupColor,
		Subnet:        m.Subnet,
		VirtualIP:     m.VirtualIP,
		Role:          string(m.Role),
		InviteCode:    code,
		OnlineMembers: m.OnlineMembers,
		Members:       m.Members,
	}
}

// handleRemoveMember takes someone out of a group and keeps them out.
func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request, device storage.Device) {
	groupID := r.PathValue("groupId")
	target := r.PathValue("deviceId")

	if err := s.store.RemoveMember(r.Context(), groupID, device.ID, target); err != nil {
		s.fail(w, r, err, api.ErrGroupNotFound, "That person is not in this group.")
		return
	}
	s.log.Info("member removed", "groupId", groupID, "deviceId", target, "by", device.ID)

	// Two audiences. The group sees somebody leave; the person removed is told
	// so, and their daemon disconnects rather than sitting on a dead session.
	s.hub.Broadcast(groupID, target, api.EventPeerOffline, api.PeerOfflineData{DeviceID: target})
	s.hub.SendTo(groupID, target, api.EventMembershipRevoked, api.PeerOfflineData{DeviceID: target})

	w.WriteHeader(http.StatusNoContent)
}

// handleRenameGroup changes what a group is called, for everyone in it.
func (s *Server) handleRenameGroup(w http.ResponseWriter, r *http.Request, device storage.Device) {
	var req struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &req) {
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > maxNameLength {
		writeError(w, http.StatusBadRequest, api.ErrBadRequest, "That name will not work.")
		return
	}

	groupID := r.PathValue("groupId")
	if err := s.store.RenameGroup(r.Context(), groupID, device.ID, name); err != nil {
		s.fail(w, r, err, api.ErrGroupNotFound, "You are not a member of that group.")
		return
	}
	s.log.Info("group renamed", "groupId", groupID, "name", name, "by", device.ID)

	s.announceGroup(r, groupID)
	w.WriteHeader(http.StatusNoContent)
}

// handleSetGroupAppearance changes the icon and colour a group is shown with.
// Keys rather than a glyph and a hex colour, so a server that has never heard
// of an icon can still carry it.
func (s *Server) handleSetGroupAppearance(w http.ResponseWriter, r *http.Request, device storage.Device) {
	var req api.SetGroupAppearanceRequest
	if !decode(w, r, &req) {
		return
	}
	if !isAppearanceKey(req.Icon) || !isAppearanceKey(req.Color) {
		writeError(w, http.StatusBadRequest, api.ErrBadRequest, "That icon or colour will not work.")
		return
	}

	groupID := r.PathValue("groupId")
	if err := s.store.SetGroupAppearance(r.Context(), groupID, device.ID, req.Icon, req.Color); err != nil {
		s.fail(w, r, err, api.ErrGroupNotFound, "You are not a member of that group.")
		return
	}
	s.log.Info("group appearance changed", "groupId", groupID, "icon", req.Icon, "color", req.Color, "by", device.ID)

	s.announceGroup(r, groupID)
	w.WriteHeader(http.StatusNoContent)
}

// announceGroup tells the group what it now is. Read back rather than
// assembled from the request, so the event carries the whole group however
// little of it changed.
func (s *Server) announceGroup(r *http.Request, groupID string) {
	group, err := s.store.GroupByID(r.Context(), groupID)
	if err != nil {
		// The change went through; only the announcement did not. Everyone in
		// the group picks it up the next time they list their groups.
		s.log.Warn("cannot announce group change", "groupId", groupID, "error", err)
		return
	}

	s.hub.Broadcast(groupID, "", api.EventGroupUpdated, api.GroupUpdatedData{
		GroupID: group.ID,
		Name:    group.Name,
		Icon:    group.Icon,
		Color:   group.Color,
	})
}

// isAppearanceKey checks the shape of a key rather than its value, since the
// set of icons and colours lives in the app. Empty means the default look.
func isAppearanceKey(key string) bool {
	if len(key) > 32 {
		return false
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	return true
}

// handleTransferOwnership hands a group to another member.
func (s *Server) handleTransferOwnership(w http.ResponseWriter, r *http.Request, device storage.Device) {
	groupID := r.PathValue("groupId")
	target := r.PathValue("deviceId")

	if err := s.store.TransferOwnership(r.Context(), groupID, device.ID, target); err != nil {
		s.fail(w, r, err, api.ErrGroupNotFound, "That person is not in this group.")
		return
	}
	s.log.Info("group ownership transferred", "groupId", groupID, "to", target, "by", device.ID)

	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteGroup removes a group for everyone in it.
func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request, device storage.Device) {
	groupID := r.PathValue("groupId")

	if err := s.store.DeleteGroup(r.Context(), groupID, device.ID); err != nil {
		s.fail(w, r, err, api.ErrGroupNotFound, "You are not a member of that group.")
		return
	}
	s.log.Info("group deleted", "groupId", groupID, "by", device.ID)

	// After it is gone, not before: an attempt that gets turned down must not
	// tell the group it was deleted. The subscribers live in memory and outlive
	// the row, so there is still somebody to tell.
	s.hub.Broadcast(groupID, "", api.EventGroupDeleted, api.GroupDeletedData{GroupID: groupID})

	w.WriteHeader(http.StatusNoContent)
}
