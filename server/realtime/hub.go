// Package realtime pushes group changes to connected daemons.
//
// Without it a daemon would learn the peer list once, when it connects, and
// never hear about anyone who joined afterwards. Polling for that would trade
// latency against load with no good answer, so the server tells them instead.
//
// The channel is a convenience, not a dependency. Losing it leaves established
// peer sessions alone, and a daemon whose connection drops keeps its tunnels
// while it reconnects.
package realtime

import (
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/OMouta/192168/protocol/api"
)

// sendBuffer is how many events a subscriber can fall behind by. A daemon that
// cannot keep up with a handful of presence messages is not going to catch up,
// so it gets dropped and reconnects with a fresh peer list rather than being
// fed a growing backlog.
const sendBuffer = 16

// Hub tracks who is listening to which group.
type Hub struct {
	log *slog.Logger

	mu     sync.Mutex
	groups map[string]map[*Subscriber]struct{}
}

// Subscriber is one connected daemon.
type Subscriber struct {
	DeviceID string
	GroupID  string

	send   chan []byte
	closed bool
	mu     sync.Mutex
}

// NewHub creates an empty hub.
func NewHub(log *slog.Logger) *Hub {
	return &Hub{log: log, groups: map[string]map[*Subscriber]struct{}{}}
}

// Subscribe registers a listener for one group's events.
func (h *Hub) Subscribe(groupID, deviceID string) *Subscriber {
	sub := &Subscriber{
		DeviceID: deviceID,
		GroupID:  groupID,
		send:     make(chan []byte, sendBuffer),
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.groups[groupID] == nil {
		h.groups[groupID] = map[*Subscriber]struct{}{}
	}
	h.groups[groupID][sub] = struct{}{}
	return sub
}

// Unsubscribe removes a listener and closes its channel. Calling it twice is
// safe, which matters because both the reader and the writer side can decide a
// connection is over.
func (h *Hub) Unsubscribe(sub *Subscriber) {
	h.mu.Lock()
	if subs := h.groups[sub.GroupID]; subs != nil {
		delete(subs, sub)
		if len(subs) == 0 {
			delete(h.groups, sub.GroupID)
		}
	}
	h.mu.Unlock()

	sub.close()
}

// Events returns the channel a connection writes from. It is closed when the
// subscriber is removed.
func (s *Subscriber) Events() <-chan []byte { return s.send }

func (s *Subscriber) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.send)
	}
}

// deliver queues an event, reporting false if the subscriber is too far behind
// or already gone.
func (s *Subscriber) deliver(payload []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	select {
	case s.send <- payload:
		return true
	default:
		return false
	}
}

// Broadcast sends an event to everyone in a group except the device that caused
// it, since nobody needs to be told about themselves.
func (h *Hub) Broadcast(groupID, exceptDeviceID string, eventType api.EventType, data any) {
	payload, err := encode(eventType, data)
	if err != nil {
		h.log.Error("cannot encode event", "type", eventType, "error", err)
		return
	}

	h.mu.Lock()
	targets := make([]*Subscriber, 0, len(h.groups[groupID]))
	for sub := range h.groups[groupID] {
		if sub.DeviceID != exceptDeviceID {
			targets = append(targets, sub)
		}
	}
	h.mu.Unlock()

	for _, sub := range targets {
		if !sub.deliver(payload) {
			h.log.Warn("dropping a subscriber that fell behind", "deviceId", sub.DeviceID, "groupId", groupID)
			h.Unsubscribe(sub)
		}
	}
}

// SendTo delivers an event to one device in a group, for the things that
// concern only them, such as having their membership revoked.
func (h *Hub) SendTo(groupID, deviceID string, eventType api.EventType, data any) {
	payload, err := encode(eventType, data)
	if err != nil {
		h.log.Error("cannot encode event", "type", eventType, "error", err)
		return
	}

	h.mu.Lock()
	var targets []*Subscriber
	for sub := range h.groups[groupID] {
		if sub.DeviceID == deviceID {
			targets = append(targets, sub)
		}
	}
	h.mu.Unlock()

	for _, sub := range targets {
		sub.deliver(payload)
	}
}

func encode(eventType api.EventType, data any) ([]byte, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(api.Event{Type: eventType, Data: raw})
}
