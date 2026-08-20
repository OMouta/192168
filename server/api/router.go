// Package api is the coordination server's HTTP handlers, covering device
// registration, groups, sessions, and the realtime channel.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/OMouta/192168/protocol"
	"github.com/OMouta/192168/protocol/api"
	"github.com/OMouta/192168/server/config"
	"github.com/OMouta/192168/server/realtime"
	"github.com/OMouta/192168/server/storage"
)

// DefaultSubnet is the address range every group gets. A group is under ten
// people, so a /24 is more room than anyone needs, and keeping it the same
// everywhere means an address someone reads out loud is unambiguous.
const DefaultSubnet = "10.69.0.0/24"

// Default session timing. A daemon heartbeats well inside the timeout, so a
// session only expires when the machine behind it has actually gone.
const (
	SessionTimeout  = 90 * time.Second
	ExpiryFrequency = 30 * time.Second
)

// Server holds the dependencies shared by every handler.
type Server struct {
	cfg     config.Config
	store   *storage.Store
	log     *slog.Logger
	hub     *realtime.Hub
	mux     *http.ServeMux
	joins   *rateLimiter
	devices *rateLimiter
}

// ServeHTTP routes a request.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// New builds the coordination server.
func New(cfg config.Config, store *storage.Store, log *slog.Logger) *Server {
	s := &Server{
		cfg:   cfg,
		store: store,
		log:   log,
		hub:   realtime.NewHub(log),
		// Group passwords are the weakest secret in the system, so guessing at
		// them has to be slow. Registration is limited too, since it is the one
		// unauthenticated write.
		joins:   newRateLimiter(10, time.Minute),
		devices: newRateLimiter(20, time.Minute),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+protocol.WellKnownPath, s.handleDiscovery)
	mux.HandleFunc("GET /api/health", s.handleHealth)

	mux.HandleFunc("POST /api/devices/register", s.handleRegisterDevice)

	mux.Handle("GET /api/groups", s.authenticated(s.handleListGroups))
	mux.Handle("POST /api/groups", s.authenticated(s.handleCreateGroup))
	mux.Handle("POST /api/groups/join", s.authenticated(s.handleJoinGroup))
	mux.Handle("DELETE /api/groups/{groupId}/membership", s.authenticated(s.handleLeaveGroup))
	mux.Handle("PUT /api/groups/{groupId}/nickname", s.authenticated(s.handleSetNickname))
	mux.Handle("POST /api/groups/{groupId}/sessions", s.authenticated(s.handleCreateSession))

	mux.Handle("PUT /api/sessions/{sessionId}/endpoint", s.authenticated(s.handleSetEndpoint))
	mux.Handle("POST /api/sessions/{sessionId}/heartbeat", s.authenticated(s.handleHeartbeat))
	mux.Handle("DELETE /api/sessions/{sessionId}", s.authenticated(s.handleDeleteSession))

	mux.Handle("GET /realtime", s.authenticated(s.handleRealtime))

	s.mux = mux
	return s
}

// handleDiscovery tells a client where the API, realtime channel, and STUN
// servers live, and which optional features this deployment supports. It is
// the only endpoint a client needs to know by path.
func (s *Server) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, api.Discovery{
		Version:  protocol.DiscoveryVersion,
		API:      s.cfg.APIURL(),
		Realtime: s.cfg.RealtimeURL(),
		STUN:     s.cfg.STUN,
		Relay:    nil,
		Features: api.Features{Relay: false, PeerRouting: false},
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError sends a stable code the client maps to its own copy, with a
// message safe to show as it is.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, api.Error{Code: code, Message: message})
}

// decode reads a JSON body, rejecting anything oversized or unparseable before
// a handler has to think about it.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	const maxBody = 64 << 10
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, api.ErrBadRequest, "The request could not be read.")
		return false
	}
	return true
}

// fail turns a storage error into a response, logging the ones that mean the
// server is broken rather than the request being wrong.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error, notFoundCode, notFoundMessage string) {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeError(w, http.StatusNotFound, notFoundCode, notFoundMessage)
	case errors.Is(err, storage.ErrGroupFull):
		writeError(w, http.StatusConflict, api.ErrGroupFull, "That group has no free addresses left.")
	default:
		s.log.Error("request failed", "path", r.URL.Path, "error", err)
		writeError(w, http.StatusInternalServerError, api.ErrInternal, "Something went wrong on the server.")
	}
}
