// Package config resolves the daemon's local settings and storage paths.
package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/OMouta/192168/protocol"
)

// DefaultServerURL is the hosted deployment the app uses until the user points
// it somewhere else. Self-hosted servers are reached by typing their URL in
// directly. Either way this is the only address a user ever sees: the API,
// realtime, and STUN endpoints all come from the server's discovery document.
const DefaultServerURL = "https://api.192168.lol"

// Config is the daemon's local configuration.
type Config struct {
	// ServerURL is the base URL of the coordination server.
	ServerURL string
	// DataDir holds the device identity, membership credentials, and logs.
	DataDir string
}

// Load resolves configuration from the environment and OS conventions.
func Load() (Config, error) {
	dataDir := os.Getenv("NET192168_DATA_DIR")
	if dataDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return Config{}, fmt.Errorf("resolve data directory: %w", err)
		}
		dataDir = filepath.Join(base, protocol.Name)
	}

	cfg := Config{
		ServerURL: strings.TrimRight(envOr("NET192168_SERVER_URL", DefaultServerURL), "/"),
		DataDir:   dataDir,
	}
	if err := ValidateServerURL(cfg.ServerURL); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// ValidateServerURL enforces the transport rule for control-plane traffic:
// HTTPS everywhere, with a localhost exception so the server can be developed
// against without certificates.
func ValidateServerURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid server URL: %q has no host", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopback(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("server URL must use https: %q", raw)
	default:
		return fmt.Errorf("unsupported server URL scheme %q", u.Scheme)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// LogLevel reads NET192168_LOG_LEVEL. Debug is where the per-packet lines live,
// which is the difference between seeing that a packet reached the adapter and
// guessing.
func LogLevel() slog.Level {
	switch strings.ToLower(os.Getenv("NET192168_LOG_LEVEL")) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
