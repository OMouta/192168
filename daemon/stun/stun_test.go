package stun

import (
	"encoding/binary"
	"net/netip"
	"testing"

	"github.com/OMouta/192168/protocol/transport"
)

// reply builds a binding success carrying one address attribute.
func reply(id TransactionID, kind uint16, addr netip.AddrPort, xored bool) []byte {
	ip := addr.Addr()
	raw := ip.AsSlice()
	port := addr.Port()

	var key [16]byte
	binary.BigEndian.PutUint32(key[0:4], magicCookie)
	copy(key[4:], id[:])

	if xored {
		port ^= uint16(magicCookie >> 16)
		for i := range raw {
			raw[i] ^= key[i]
		}
	}

	value := make([]byte, 4+len(raw))
	value[1] = familyIPv4
	if ip.Is6() {
		value[1] = familyIPv6
	}
	binary.BigEndian.PutUint16(value[2:4], port)
	copy(value[4:], raw)

	body := make([]byte, 4+len(value))
	binary.BigEndian.PutUint16(body[0:2], kind)
	binary.BigEndian.PutUint16(body[2:4], uint16(len(value)))
	copy(body[4:], value)

	packet := make([]byte, headerSize+len(body))
	binary.BigEndian.PutUint16(packet[0:2], bindingSuccess)
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(body)))
	binary.BigEndian.PutUint32(packet[4:8], magicCookie)
	copy(packet[8:20], id[:])
	copy(packet[headerSize:], body)
	return packet
}

func TestParseResponse(t *testing.T) {
	req, err := NewRequest()
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	want := netip.MustParseAddrPort("203.0.113.50:51821")

	for _, tt := range []struct {
		name  string
		kind  uint16
		xored bool
	}{
		{"xor mapped address", attrXorMappedAddress, true},
		{"plain mapped address", attrMappedAddress, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseResponse(req.ID, reply(req.ID, tt.kind, want, tt.xored))
			if err != nil {
				t.Fatalf("ParseResponse: %v", err)
			}
			if got != want {
				t.Errorf("address = %v, want %v", got, want)
			}
		})
	}
}

func TestParseResponseRejectsAnotherRequestsReply(t *testing.T) {
	mine, err := NewRequest()
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	theirs, err := NewRequest()
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	// One socket has several requests in flight and receives replies from
	// several servers. Matching the wrong one would record the wrong address.
	packet := reply(theirs.ID, attrXorMappedAddress, netip.MustParseAddrPort("198.51.100.7:1234"), true)
	if _, err := ParseResponse(mine.ID, packet); err == nil {
		t.Error("a reply for another transaction was accepted")
	}
}

func TestParseResponseRejectsJunk(t *testing.T) {
	req, err := NewRequest()
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	good := reply(req.ID, attrXorMappedAddress, netip.MustParseAddrPort("203.0.113.50:51821"), true)

	tests := []struct {
		name   string
		packet []byte
	}{
		{"empty", nil},
		{"truncated header", good[:headerSize-1]},
		{"body shorter than the header claims", func() []byte {
			bad := append([]byte(nil), good...)
			binary.BigEndian.PutUint16(bad[2:4], 999)
			return bad
		}()},
		{"attribute runs past the end", func() []byte {
			bad := append([]byte(nil), good...)
			binary.BigEndian.PutUint16(bad[headerSize+2:headerSize+4], 900)
			return bad
		}()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseResponse(req.ID, tt.packet); err == nil {
				t.Error("junk was accepted")
			}
		})
	}
}

func TestParseResponseRejectsARequest(t *testing.T) {
	req, err := NewRequest()
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	// Punching means both sides send. A request arriving where a reply was
	// expected must not be read as one.
	if _, err := ParseResponse(req.ID, req.Packet); err == nil {
		t.Error("a binding request was parsed as a response")
	}
}

// The socket carries STUN and peer traffic at once, so each has to be
// recognisable without parsing the other.
func TestLooksTellsStunFromPeerTraffic(t *testing.T) {
	req, err := NewRequest()
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if !Looks(req.Packet) {
		t.Error("a real STUN request was not recognised")
	}

	peer := transport.Header{
		Version: 1,
		Type:    transport.MsgData,
		Sender:  1,
		Counter: 1,
	}.Encode(nil, []byte("payload"))
	if Looks(peer) {
		t.Error("a peer packet was mistaken for STUN")
	}

	if Looks([]byte{0, 1, 2}) {
		t.Error("a runt packet was mistaken for STUN")
	}
}
