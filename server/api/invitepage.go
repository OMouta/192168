package api

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/OMouta/192168/protocol/api"
	"github.com/OMouta/192168/protocol/invite"
	"github.com/OMouta/192168/server/storage"
)

//go:embed templates
var templates embed.FS

// wordmark is the same image the site and the app use, served rather than
// inlined so the page stays small on a phone.
var wordmark, _ = templates.ReadFile("templates/wordmark.png")

// invitePage is parsed once, at startup, rather than on the first person to
// open a link.
var invitePage = template.Must(template.ParseFS(templates, "templates/invite.html"))

// downloadURL is where somebody without the app goes. A self-hosted server runs
// the same app, so every deployment points at the same releases.
const downloadURL = "https://github.com/OMouta/192168/releases/latest"

// handleWordmark serves the logo the invite page shows.
func (s *Server) handleWordmark(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	// It changes when the brand does, which is to say not on a timescale a
	// browser cares about.
	w.Header().Set("Cache-Control", "public, max-age=604800")
	http.ServeContent(w, r, "wordmark.png", time.Time{}, bytes.NewReader(wordmark))
}

// handleInvitePage is what a link opens in a browser. The server draws it
// rather than the website, so a self-hosted instance needs nothing else
// deployed for its links to work.
func (s *Server) handleInvitePage(w http.ResponseWriter, r *http.Request) {
	view := struct {
		Invite  *api.Invite
		Members string
		// html/template blanks a custom scheme in an href rather than saying so.
		// Built here from a code the server just looked up, so marking it is safe.
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
		// The status and part of the body are already sent.
		s.log.Error("could not render an invite", "error", err)
	}
}

// members phrases the count, so it does not read "1 people".
func members(n int) string {
	if n == 1 {
		return "1 person"
	}
	return fmt.Sprintf("%d people", n)
}
