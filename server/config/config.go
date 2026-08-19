// Package config loads the coordination server's runtime configuration.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// DefaultSTUN is used when no STUN servers are configured. Deployments that
// run their own STUN infrastructure override it without clients needing a
// rebuild: the list is published in the discovery document.
var DefaultSTUN = []string{"stun:stun.l.google.com:19302"}

// Config is the full server configuration, read from the environment so the
// Docker deployment needs nothing but variables.
type Config struct {
	// Addr is the listen address, e.g. ":8080".
	Addr string
	// PublicURL is the externally reachable base URL. It is the one address
	// users paste into the app, and where the discovery document is served.
	PublicURL string
	// APIOverride, when set, is advertised as the API base instead of the
	// value derived from PublicURL. It exists for deployments that answer
	// discovery on one host and serve the API on another, such as
	// https://192168.lol advertising https://api.192168.lol.
	APIOverride string
	// RealtimeOverride does the same for the WebSocket URL.
	RealtimeOverride string
	// STUN servers advertised to clients.
	STUN []string
	// DatabaseURL is the storage DSN. An empty value means the local SQLite
	// default, which is enough for a small self-hosted instance.
	DatabaseURL string
}

// Load reads configuration from the environment and validates it.
func Load() (Config, error) {
	c := Config{
		Addr:             envOr("NET192168_ADDR", ":8080"),
		PublicURL:        trimURL(os.Getenv("NET192168_PUBLIC_URL")),
		APIOverride:      trimURL(os.Getenv("NET192168_API_URL")),
		RealtimeOverride: trimURL(os.Getenv("NET192168_REALTIME_URL")),
		DatabaseURL:      os.Getenv("NET192168_DATABASE_URL"),
		STUN:             DefaultSTUN,
	}

	if raw := os.Getenv("NET192168_STUN"); raw != "" {
		c.STUN = splitAndTrim(raw)
	}

	if c.PublicURL == "" {
		return Config{}, fmt.Errorf("NET192168_PUBLIC_URL is required")
	}
	if err := checkPublic("NET192168_PUBLIC_URL", c.PublicURL, "https"); err != nil {
		return Config{}, err
	}
	if c.APIOverride != "" {
		if err := checkPublic("NET192168_API_URL", c.APIOverride, "https"); err != nil {
			return Config{}, err
		}
	}
	if c.RealtimeOverride != "" {
		if err := checkPublic("NET192168_REALTIME_URL", c.RealtimeOverride, "wss"); err != nil {
			return Config{}, err
		}
	}

	return c, nil
}

// APIURL is the API base advertised to clients.
func (c Config) APIURL() string {
	if c.APIOverride != "" {
		return c.APIOverride
	}
	return c.PublicURL + "/api"
}

// RealtimeURL is the WebSocket URL advertised to clients. Deriving it from the
// public URL is what lets an operator configure one address rather than four.
func (c Config) RealtimeURL() string {
	if c.RealtimeOverride != "" {
		return c.RealtimeOverride
	}
	if rest, ok := strings.CutPrefix(c.PublicURL, "https://"); ok {
		return "wss://" + rest + "/realtime"
	}
	return "ws://" + strings.TrimPrefix(c.PublicURL, "http://") + "/realtime"
}

// checkPublic rejects addresses clients would refuse to use. Control-plane
// traffic is TLS-only, with a localhost exception so the server can be
// developed against without certificates.
func checkPublic(name, raw, secureScheme string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s is not a valid URL: %w", name, err)
	}
	if u.Host == "" {
		return fmt.Errorf("%s has no host: %q", name, raw)
	}
	if u.Scheme == secureScheme || isLoopback(u.Hostname()) {
		return nil
	}
	return fmt.Errorf("%s must use %s (got %q)", name, secureScheme, raw)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func trimURL(raw string) string { return strings.TrimRight(strings.TrimSpace(raw), "/") }

func splitAndTrim(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
