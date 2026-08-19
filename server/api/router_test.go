package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OMouta/192168/protocol"
	papi "github.com/OMouta/192168/protocol/api"
	"github.com/OMouta/192168/server/config"
)

func TestDiscoveryDocument(t *testing.T) {
	cfg := config.Config{
		PublicURL: "https://lan.example.com",
		STUN:      []string{"stun:stun.example.com:3478"},
	}
	h := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, protocol.WellKnownPath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var doc papi.Discovery
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Version != protocol.DiscoveryVersion {
		t.Errorf("version = %d, want %d", doc.Version, protocol.DiscoveryVersion)
	}
	if doc.API != "https://lan.example.com/api" {
		t.Errorf("api = %q", doc.API)
	}
	if doc.Realtime != "wss://lan.example.com/realtime" {
		t.Errorf("realtime = %q", doc.Realtime)
	}
	if len(doc.STUN) != 1 || doc.STUN[0] != "stun:stun.example.com:3478" {
		t.Errorf("stun = %v", doc.STUN)
	}
}
