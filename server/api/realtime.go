package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/OMouta/192168/protocol/api"
	"github.com/OMouta/192168/server/storage"
)

const (
	// pingInterval is how often the server checks a connection is still there.
	// A daemon behind a NAT that silently dropped the mapping looks fine until
	// something is written to it.
	pingInterval = 30 * time.Second

	// pingTimeout is how long a pong may take before the connection is treated
	// as dead.
	pingTimeout = 10 * time.Second
)

// handleRealtime upgrades to a WebSocket bound to one active session. The
// daemon holds it open for the life of a group connection and reads presence
// and endpoint changes from it.
//
// The socket carries events one way. A daemon that wants to change something
// calls the HTTP API, so there is one path for writes and one shape to secure.
func (s *Server) handleRealtime(w http.ResponseWriter, r *http.Request, device storage.Device) {
	session, err := s.store.SessionByID(r.Context(), r.URL.Query().Get("session"))
	if err != nil || session.DeviceID != device.ID {
		writeError(w, http.StatusNotFound, api.ErrSessionInvalid, "That session is no longer active.")
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// The daemon is not a browser and sends no Origin, so there is no
		// same-origin check to make here. Authentication is the bearer token.
		InsecureSkipVerify: true,
	})
	if err != nil {
		s.log.Warn("websocket upgrade failed", "deviceId", device.ID, "error", err)
		return
	}
	defer conn.CloseNow()

	sub := s.hub.Subscribe(session.GroupID, device.ID)
	defer s.hub.Unsubscribe(sub)

	// Anything that changed between the peer list this daemon was given at
	// connect and this subscription would otherwise be lost, and a peer whose
	// endpoint arrived in that gap is unreachable forever. Sending the current
	// peers now closes it, and does the same for a reconnect.
	s.sendCurrentPeers(r.Context(), session, device.ID)

	s.log.Info("realtime connected", "deviceId", device.ID, "groupId", session.GroupID)

	// The request context is done once the handler stops owning the connection,
	// but the connection outlives it. What ends this loop is the peer going
	// away, which the reader below notices.
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()
	go s.readUntilClosed(ctx, cancel, conn)

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case payload, ok := <-sub.Events():
			if !ok {
				conn.Close(websocket.StatusNormalClosure, "")
				return
			}
			writeCtx, writeCancel := context.WithTimeout(ctx, pingTimeout)
			err := conn.Write(writeCtx, websocket.MessageText, payload)
			writeCancel()
			if err != nil {
				return
			}

		case <-ticker.C:
			pingCtx, pingCancel := context.WithTimeout(ctx, pingTimeout)
			err := conn.Ping(pingCtx)
			pingCancel()
			if err != nil {
				s.log.Info("realtime ping failed", "deviceId", device.ID)
				return
			}
			// The daemon is still there, so its session is too.
			if err := s.store.TouchSession(ctx, session.ID); err != nil {
				if !errors.Is(err, storage.ErrNotFound) {
					s.log.Error("cannot touch session", "sessionId", session.ID, "error", err)
				}
				return
			}
		}
	}
}

// readUntilClosed drains incoming frames. Nothing is expected on this
// direction, but a reader has to run for the library to process control frames
// and to notice the peer going away.
func (s *Server) readUntilClosed(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn) {
	defer cancel()
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			return
		}
	}
}

// ExpireSessions removes sessions nobody has heard from and tells their groups.
// Without it a client that was unplugged mid-game stays in the peer list, and
// everyone else keeps trying to punch toward an address that is gone.
//
// It runs until ctx is cancelled.
func (s *Server) ExpireSessions(ctx context.Context, every, timeout time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stale, err := s.store.ExpireSessions(ctx, timeout)
			if err != nil {
				s.log.Error("cannot expire sessions", "error", err)
				continue
			}
			for _, session := range stale {
				s.log.Info("session expired", "sessionId", session.ID, "deviceId", session.DeviceID)
				s.hub.Broadcast(session.GroupID, session.DeviceID, api.EventPeerOffline, api.PeerOfflineData{
					DeviceID: session.DeviceID,
				})
			}
		}
	}
}

// sendCurrentPeers replays who is online to one subscriber.
//
// Peer online is idempotent on the daemon side, so a peer it already knows
// about costs nothing and a peer it missed is picked up.
func (s *Server) sendCurrentPeers(ctx context.Context, session storage.Session, deviceID string) {
	peers, err := s.store.PeersInGroup(ctx, session.GroupID, deviceID)
	if err != nil {
		s.log.Error("cannot read peers for a new subscriber", "sessionId", session.ID, "error", err)
		return
	}

	for _, peer := range toPeers(peers) {
		s.hub.SendTo(session.GroupID, deviceID, api.EventPeerOnline, api.PeerOnlineData{Peer: peer})
	}
}
