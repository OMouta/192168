// Package config resolves the daemon's local settings and storage paths.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/OMouta/192168/protocol"
)

// Config is the daemon's local configuration.
type Config struct {
	// ServerURL is the base URL of the coordination server, empty until the
	// user picks one. There is no default deployment baked in: a server is
	// something you are given or run, and it is the only address the user ever
	// types. Everything else (API, realtime, STUN) comes from its discovery
	// document.
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
		ServerURL: strings.TrimRight(os.Getenv("NET192168_SERVER_URL"), "/"),
		DataDir:   dataDir,
	}
	// An unconfigured server is the normal first-run state, not an error: the
	// daemon still has to start so the client can connect and set one.
	if cfg.ServerURL != "" {
		if err := ValidateServerURL(cfg.ServerURL); err != nil {
			return Config{}, err
		}
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
