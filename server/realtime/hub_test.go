package realtime

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/OMouta/192168/protocol/api"
)

func newHub() *Hub { return NewHub(slog.New(slog.NewTextHandler(io.Discard, nil))) }

// receive reads one queued event, or fails if there is none waiting.
func receive(t *testing.T, sub *Subscriber) api.Event {
	t.Helper()
	select {
	case payload, ok := <-sub.Events():
		if !ok {
			t.Fatal("the subscriber was closed")
		}
		var event api.Event
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		return event
	default:
		t.Fatal("no event was delivered")
		return api.Event{}
	}
}

func expectNothing(t *testing.T, sub *Subscriber) {
	t.Helper()
	select {
	case payload := <-sub.Events():
		t.Fatalf("an unexpected event arrived: %s", payload)
	default:
	}
}

func TestBroadcastSkipsTheDeviceThatCausedIt(t *testing.T) {
	h := newHub()
	joiner := h.Subscribe("grp_1", "dev_joiner")
	watcher := h.Subscribe("grp_1", "dev_watcher")

	h.Broadcast("grp_1", "dev_joiner", api.EventPeerOnline, api.PeerOnlineData{
		Peer: api.Peer{DeviceID: "dev_joiner", VirtualIP: "10.69.0.2"},
	})

	event := receive(t, watcher)
	if event.Type != api.EventPeerOnline {
		t.Errorf("type = %q, want %q", event.Type, api.EventPeerOnline)
	}
	var data api.PeerOnlineData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if data.Peer.VirtualIP != "10.69.0.2" {
		t.Errorf("peer = %+v", data.Peer)
	}

	// Nobody needs to be told they came online.
	expectNothing(t, joiner)
}

func TestBroadcastStaysInsideItsGroup(t *testing.T) {
	h := newHub()
	inside := h.Subscribe("grp_1", "dev_1")
	outside := h.Subscribe("grp_2", "dev_2")

	h.Broadcast("grp_1", "", api.EventPeerOffline, api.PeerOfflineData{DeviceID: "dev_3"})

	receive(t, inside)
	expectNothing(t, outside)
}

func TestSendToReachesOneDevice(t *testing.T) {
	h := newHub()
	target := h.Subscribe("grp_1", "dev_1")
	other := h.Subscribe("grp_1", "dev_2")

	h.SendTo("grp_1", "dev_1", api.EventMembershipRevoked, api.PeerOfflineData{DeviceID: "dev_1"})

	if event := receive(t, target); event.Type != api.EventMembershipRevoked {
		t.Errorf("type = %q", event.Type)
	}
	expectNothing(t, other)
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	h := newHub()
	sub := h.Subscribe("grp_1", "dev_1")

	h.Unsubscribe(sub)
	// Unsubscribing twice happens when the reader and the writer both decide
	// the connection is over, and it must not panic.
	h.Unsubscribe(sub)

	h.Broadcast("grp_1", "", api.EventPeerOffline, api.PeerOfflineData{DeviceID: "dev_2"})

	if _, ok := <-sub.Events(); ok {
		t.Error("an event arrived after unsubscribing")
	}
}

func TestASubscriberThatFallsBehindIsDropped(t *testing.T) {
	h := newHub()
	slow := h.Subscribe("grp_1", "dev_slow")

	// Nothing reads from this subscriber, so the buffer fills and then it is
	// cut loose rather than being allowed to grow without bound.
	for range sendBuffer + 5 {
		h.Broadcast("grp_1", "", api.EventPeerOffline, api.PeerOfflineData{DeviceID: "dev_other"})
	}

	drained := 0
	for range slow.Events() {
		drained++
	}
	if drained > sendBuffer {
		t.Errorf("delivered %d events, more than the %d buffer", drained, sendBuffer)
	}

	h.mu.Lock()
	remaining := len(h.groups["grp_1"])
	h.mu.Unlock()
	if remaining != 0 {
		t.Errorf("%d subscribers left, want the slow one gone", remaining)
	}
}
