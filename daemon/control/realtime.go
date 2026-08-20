package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/coder/websocket"

	"github.com/OMouta/192168/protocol/api"
	"github.com/OMouta/192168/protocol/auth"
)

// RealtimeHandler is what the daemon does with each kind of change. A nil
// field means that change is ignored.
type RealtimeHandler struct {
	PeerOnline          func(peer api.Peer)
	PeerOffline         func(deviceID string)
	PeerEndpointUpdated func(deviceID string, endpoint api.Endpoint)
	PeerRenamed         func(deviceID, nickname string)
	GroupDeleted        func(groupID string)
	MembershipRevoked   func()
	SessionInvalidated  func()

	// Connected reports whether the channel is up. Losing it is a status
	// change, not a failure: peer links keep working without it.
	Connected func(up bool)
}

// Realtime keeps the event channel open until ctx is cancelled.
//
// Without it a daemon learns the peer list once, when it connects, and never
// hears about anyone who arrives afterwards. Losing the channel leaves
// established links alone, so this reconnects quietly rather than tearing
// anything down.
func (c *Client) Realtime(ctx context.Context, sessionID string, handler RealtimeHandler) {
	backoff := time.Second

	for ctx.Err() == nil {
		err := c.listen(ctx, sessionID, handler)
		if ctx.Err() != nil {
			return
		}

		if handler.Connected != nil {
			handler.Connected(false)
		}

		// A session the server has forgotten will never accept this channel,
		// so retrying it forever would be noise.
		if IsUnauthorized(err) || isSessionGone(err) {
			if handler.SessionInvalidated != nil {
				handler.SessionInvalidated()
			}
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (c *Client) listen(ctx context.Context, sessionID string, handler RealtimeHandler) error {
	endpoint, err := url.Parse(c.discovery.Realtime)
	if err != nil {
		return err
	}
	query := endpoint.Query()
	query.Set("session", sessionID)
	endpoint.RawQuery = query.Encode()

	conn, res, err := websocket.Dial(ctx, endpoint.String(), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{auth.TokenScheme + " " + c.token}},
	})
	if err != nil {
		if res != nil && res.StatusCode == http.StatusUnauthorized {
			return &Error{Status: res.StatusCode, Code: api.ErrUnauthorized, Message: "This device is not signed in."}
		}
		if res != nil && res.StatusCode == http.StatusNotFound {
			return &Error{Status: res.StatusCode, Code: api.ErrSessionInvalid, Message: "That session is no longer active."}
		}
		return err
	}
	defer conn.CloseNow()

	if handler.Connected != nil {
		handler.Connected(true)
	}

	for {
		_, payload, err := conn.Read(ctx)
		if err != nil {
			return err
		}

		var event api.Event
		if err := json.Unmarshal(payload, &event); err != nil {
			continue
		}
		dispatch(event, handler)
	}
}

// dispatch turns one event into one call. An unknown type is ignored, so a
// newer server can add events without breaking an older daemon.
func dispatch(event api.Event, handler RealtimeHandler) {
	switch event.Type {
	case api.EventPeerOnline:
		var data api.PeerOnlineData
		if json.Unmarshal(event.Data, &data) == nil && handler.PeerOnline != nil {
			handler.PeerOnline(data.Peer)
		}

	case api.EventPeerOffline:
		var data api.PeerOfflineData
		if json.Unmarshal(event.Data, &data) == nil && handler.PeerOffline != nil {
			handler.PeerOffline(data.DeviceID)
		}

	case api.EventPeerEndpointUpdated:
		var data api.PeerEndpointUpdatedData
		if json.Unmarshal(event.Data, &data) == nil && handler.PeerEndpointUpdated != nil {
			handler.PeerEndpointUpdated(data.DeviceID, data.Endpoint)
		}

	case api.EventPeerRenamed:
		var data api.PeerRenamedData
		if json.Unmarshal(event.Data, &data) == nil && handler.PeerRenamed != nil {
			handler.PeerRenamed(data.DeviceID, data.Nickname)
		}

	case api.EventGroupDeleted:
		var data api.GroupDeletedData
		if json.Unmarshal(event.Data, &data) == nil && handler.GroupDeleted != nil {
			handler.GroupDeleted(data.GroupID)
		}

	case api.EventMembershipRevoked:
		if handler.MembershipRevoked != nil {
			handler.MembershipRevoked()
		}

	case api.EventSessionInvalidated:
		if handler.SessionInvalidated != nil {
			handler.SessionInvalidated()
		}
	}
}

func isSessionGone(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == api.ErrSessionInvalid
}
