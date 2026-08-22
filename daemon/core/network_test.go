package core

import (
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/OMouta/192168/protocol/ipc"
)

// stunResponder is the smallest thing that can answer a binding request: it
// tells whoever asked what address it arrived from.
//
// The daemons under test are on loopback, so what they learn is their loopback
// address and the port the OS gave them. That is enough for them to punch at
// each other, and it is what a real STUN server does with a real address.
func stunResponder(t *testing.T) string {
	t.Helper()

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	go func() {
		buffer := make([]byte, 512)
		for {
			n, from, err := conn.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			if n < 20 || binary.BigEndian.Uint16(buffer[0:2]) != 0x0001 {
				continue
			}
			conn.WriteToUDP(bindingSuccess(buffer[8:20], from), from)
		}
	}()

	return "stun:" + conn.LocalAddr().String()
}

// bindingSuccess builds a reply carrying an XOR-MAPPED-ADDRESS.
func bindingSuccess(transaction []byte, from *net.UDPAddr) []byte {
	const magic = 0x2112A442

	ip := from.IP.To4()
	port := uint16(from.Port) ^ uint16(magic>>16)

	var key [16]byte
	binary.BigEndian.PutUint32(key[0:4], magic)
	copy(key[4:], transaction)

	value := make([]byte, 8)
	value[1] = 0x01 // IPv4
	binary.BigEndian.PutUint16(value[2:4], port)
	for i := range 4 {
		value[4+i] = ip[i] ^ key[i]
	}

	attribute := make([]byte, 4+len(value))
	binary.BigEndian.PutUint16(attribute[0:2], 0x0020) // XOR-MAPPED-ADDRESS
	binary.BigEndian.PutUint16(attribute[2:4], uint16(len(value)))
	copy(attribute[4:], value)

	packet := make([]byte, 20+len(attribute))
	binary.BigEndian.PutUint16(packet[0:2], 0x0101) // binding success
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(attribute)))
	binary.BigEndian.PutUint32(packet[4:8], magic)
	copy(packet[8:20], transaction)
	copy(packet[20:], attribute)
	return packet
}

// waitForPeerState polls the state a UI would render.
func waitForPeerState(t *testing.T, c *Core, want ipc.PeerState) ipc.PeerView {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)
	var last ipc.State
	for time.Now().Before(deadline) {
		state, err := c.GetState(t.Context())
		if err != nil {
			t.Fatalf("GetState: %v", err)
		}
		last = state
		for _, peer := range state.Peers {
			if peer.State == want {
				return peer
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("no peer reached %s, last state was %+v", want, last)
	return ipc.PeerView{}
}

// Two daemons, one group, and a link that opens without anything else being
// told what to do. This is the whole daemon short of the virtual adapter.
func TestTwoDaemonsReachEachOther(t *testing.T) {
	url := liveServerWithStun(t, stunResponder(t))

	host, hostEvents := newCore(t, url)
	guest, guestEvents := newCore(t, url)
	ctx := t.Context()
	named(t, host, "Tiago")
	named(t, guest, "João")

	group, err := host.CreateGroup(ctx, ipc.CreateGroupParams{
		Name: "Friday Night",
	})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if _, err := guest.JoinGroup(ctx, ipc.JoinGroupParams{Code: group.InviteCode}); err != nil {
		t.Fatalf("JoinGroup: %v", err)
	}

	if err := host.Connect(ctx, group.GroupID); err != nil {
		t.Fatalf("host Connect: %v", err)
	}
	hostEvents.waitFor(t, ipc.EventGroupConnected)

	if err := guest.Connect(ctx, group.GroupID); err != nil {
		t.Fatalf("guest Connect: %v", err)
	}
	guestEvents.waitFor(t, ipc.EventGroupConnected)

	// The guest was told about the host at connect time. The host only hears
	// about the guest over the realtime channel, so both directions being
	// reported proves that channel is working too.
	peer := waitForPeerState(t, guest, ipc.PeerDirect)
	if peer.Nickname != "Tiago" || peer.VirtualIP != "10.69.0.1" {
		t.Errorf("peer = %+v", peer)
	}

	fromHost := waitForPeerState(t, host, ipc.PeerDirect)
	if fromHost.Nickname != "João" {
		t.Errorf("peer = %+v", fromHost)
	}

	// A round trip is measured once a link is open, so a latency should appear
	// without anything else happening.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		state, _ := guest.GetState(ctx)
		if len(state.Peers) > 0 && state.Peers[0].LatencyMS != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("no latency was ever reported")
}
