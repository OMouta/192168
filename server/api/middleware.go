package api

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/OMouta/192168/protocol/api"
	"github.com/OMouta/192168/protocol/auth"
	"github.com/OMouta/192168/server/storage"
)

type contextKey int

const deviceKey contextKey = iota

// authenticated resolves the bearer token to a device before running h. Every
// endpoint except discovery, health, and registration goes through here.
func (s *Server) authenticated(h func(http.ResponseWriter, *http.Request, storage.Device)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, api.ErrUnauthorized, "This device is not signed in.")
			return
		}

		device, err := s.store.DeviceByToken(r.Context(), token)
		if err != nil {
			// A revoked token and an invented one get the same answer, since
			// the caller has no business learning which it holds.
			writeError(w, http.StatusUnauthorized, api.ErrUnauthorized, "This device is not signed in.")
			return
		}

		h(w, r.WithContext(context.WithValue(r.Context(), deviceKey, device)), device)
	})
}

func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, auth.TokenScheme) || token == "" {
		return "", false
	}
	return token, true
}

// rateLimiter counts recent attempts per key and refuses once a caller is over
// the limit. It is in memory, which is enough for a single instance and is
// where this server is expected to run.
type rateLimiter struct {
	limit  int
	window time.Duration

	mu       sync.Mutex
	attempts map[string][]time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, window: window, attempts: map[string][]time.Time{}}
}

// allow records an attempt and reports whether it is within the limit.
func (l *rateLimiter) allow(key string) bool {
	now := time.Now()
	cutoff := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	kept := l.attempts[key][:0]
	for _, at := range l.attempts[key] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	if len(kept) >= l.limit {
		l.attempts[key] = kept
		return false
	}
	l.attempts[key] = append(kept, now)
	return true
}

// clientIP is the rate limit key. Behind a reverse proxy this is the proxy's
// address unless it is stripping and forwarding, which is a deployment concern
// rather than something to guess at from a header an attacker controls.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
