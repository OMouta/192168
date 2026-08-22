package api

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"net/http"

	"github.com/OMouta/192168/protocol/api"
	"github.com/OMouta/192168/protocol/invite"
	"github.com/OMouta/192168/server/storage"
)

//go:embed templates/invite.html
var templates embed.FS

// invitePage is parsed once. A template that will not parse is a build mistake,
// so it fails at startup rather than on the first person to open a link.
var invitePage = template.Must(template.ParseFS(templates, "templates/invite.html"))

// downloadURL is where somebody without the app goes. Every deployment points
// at the same releases, because a self-hosted server runs the same app.
const downloadURL = "https://github.com/OMouta/192168/releases/latest"

// handleInvitePage is what a link opens in a browser.
//
// The server draws it rather than the website, so a self-hosted instance hands
// out links that work with nothing else deployed. It is the same lookup the API
// does, rendered instead of encoded.
func (s *Server) handleInvitePage(w http.ResponseWriter, r *http.Request) {
	view := struct {
		Invite  *api.Invite
		Members string
		// A custom scheme is not one html/template will let through on its own,
		// and it blanks the attribute rather than saying so. This is built here
		// out of a code the server just looked up, so it is safe to mark.
		DeepLink template.URL
		Download string
	}{Download: downloadURL}

	found, err := s.findInvite(r.Context(), r.PathValue("code"))
	if err == nil {
		view.Invite = &found
		view.Members = members(found.Members)
		view.DeepLink = template.URL(invite.DeepLink(found.Code))
	} else if !errors.Is(err, storage.ErrNotFound) {
		s.log.Error("could not read an invite", "error", err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if view.Invite == nil {
		w.WriteHeader(http.StatusNotFound)
	}
	if err := invitePage.Execute(w, view); err != nil {
		// Nothing useful can be sent now: the status and part of the body are
		// already on their way.
		s.log.Error("could not render an invite", "error", err)
	}
}

// members phrases the count, because "1 people" is the kind of thing that makes
// a product look unfinished.
func members(n int) string {
	if n == 1 {
		return "1 person"
	}
	return fmt.Sprintf("%d people", n)
}
