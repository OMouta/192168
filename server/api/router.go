// Package api serves the coordination server's HTTP surface.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/OMouta/192168/protocol"
	"github.com/OMouta/192168/protocol/api"
	"github.com/OMouta/192168/server/config"
)

// Server holds the dependencies shared by every handler.
type Server struct {
	cfg config.Config
	log *slog.Logger
}

// New builds the HTTP handler for the coordination server.
func New(cfg config.Config, log *slog.Logger) http.Handler {
	s := &Server{cfg: cfg, log: log}

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+protocol.WellKnownPath, s.handleDiscovery)
	mux.HandleFunc("GET /api/health", s.handleHealth)
	return mux
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
