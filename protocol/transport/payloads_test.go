package transport

import (
	"bytes"
	"errors"
	"net/netip"
	"testing"
)

func TestPingRoundTrip(t *testing.T) {
	for _, want := range []Ping{
		{Token: 0, Reply: false},
		{Token: 1 << 63, Reply: true},
	} {
		got, err := DecodePing(want.Encode(nil))
		if err != nil {
			t.Fatalf("DecodePing: %v", err)
		}
		if got != want {
			t.Errorf("ping = %+v, want %+v", got, want)
		}
	}
}

func TestDecodePingRejectsShortPayloads(t *testing.T) {
	full := Ping{Token: 7, Reply: true}.Encode(nil)
	for _, pkt := range [][]byte{nil, full[:PingSize-1]} {
		if _, err := DecodePing(pkt); !errors.Is(err, ErrMalformedPayload) {
			t.Errorf("DecodePing(%d bytes) err = %v, want ErrMalformedPayload", len(pkt), err)
		}
	}
}

func TestHandshakeInitRoundTrip(t *testing.T) {
	want := HandshakeInit{DeviceID: "dev_123", KeyExchange: []byte("noise-message")}

	encoded, err := want.Encode(nil)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := DecodeHandshakeInit(encoded)
	if err != nil {
		t.Fatalf("DecodeHandshakeInit: %v", err)
	}
	if got.DeviceID != want.DeviceID {
		t.Errorf("device id = %q, want %q", got.DeviceID, want.DeviceID)
	}
	if !bytes.Equal(got.KeyExchange, want.KeyExchange) {
		t.Errorf("key exchange = %q, want %q", got.KeyExchange, want.KeyExchange)
	}
}

func TestDecodeHandshakeInitRejectsTruncation(t *testing.T) {
	full, err := HandshakeInit{DeviceID: "dev_123", KeyExchange: []byte("noise")}.Encode(nil)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// Every prefix of a valid packet has to be rejected rather than read past.
	for n := range len(full) {
		if _, err := DecodeHandshakeInit(full[:n]); !errors.Is(err, ErrMalformedPayload) {
			t.Errorf("DecodeHandshakeInit(%d of %d bytes) err = %v, want ErrMalformedPayload", n, len(full), err)
		}
	}
}

func TestHandshakeInitRejectsOversizedFields(t *testing.T) {
	long := make([]byte, 256)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := (HandshakeInit{DeviceID: string(long)}).Encode(nil); err == nil {
		t.Error("a 256 byte device id encoded, which would truncate to 0")
	}
}

func TestHandshakeResponseRoundTrip(t *testing.T) {
	want := HandshakeResponse{KeyExchange: []byte("noise-reply")}

	encoded, err := want.Encode(nil)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := DecodeHandshakeResponse(encoded)
	if err != nil {
		t.Fatalf("DecodeHandshakeResponse: %v", err)
	}
	if !bytes.Equal(got.KeyExchange, want.KeyExchange) {
		t.Errorf("key exchange = %q, want %q", got.KeyExchange, want.KeyExchange)
	}
}

func TestDecodeHandshakeResponseRejectsTruncation(t *testing.T) {
	full, err := HandshakeResponse{KeyExchange: []byte("noise-reply")}.Encode(nil)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	for n := range len(full) {
		if _, err := DecodeHandshakeResponse(full[:n]); !errors.Is(err, ErrMalformedPayload) {
			t.Errorf("DecodeHandshakeResponse(%d of %d bytes) err = %v, want ErrMalformedPayload", n, len(full), err)
		}
	}
}

func TestForwardRoundTrip(t *testing.T) {
	carried := Header{Version: 1, Type: MsgData, Sender: 9, Counter: 3}.Encode(nil, []byte("sealed"))
	want := Forward{
		HopLimit:    1,
		Source:      netip.MustParseAddr("10.69.0.2"),
		Destination: netip.MustParseAddr("10.69.0.5"),
		Packet:      carried,
	}

	encoded, err := want.Encode(nil)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := DecodeForward(encoded)
	if err != nil {
		t.Fatalf("DecodeForward: %v", err)
	}
	if got.HopLimit != want.HopLimit || got.Source != want.Source || got.Destination != want.Destination {
		t.Errorf("forward = %+v, want %+v", got, want)
	}
	if !bytes.Equal(got.Packet, want.Packet) {
		t.Errorf("packet = %q, want %q", got.Packet, want.Packet)
	}
}

func TestDecodeForwardRejectsTruncation(t *testing.T) {
	full, err := Forward{
		Source:      netip.MustParseAddr("10.69.0.2"),
		Destination: netip.MustParseAddr("10.69.0.5"),
		Packet:      Header{Version: 1, Type: MsgData}.Encode(nil, nil),
	}.Encode(nil)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// Anything too short to hold a packet is not one, and reading the header
	// out of it would read past the payload.
	for n := range len(full) {
		if _, err := DecodeForward(full[:n]); !errors.Is(err, ErrMalformedPayload) {
			t.Errorf("DecodeForward(%d of %d bytes) err = %v, want ErrMalformedPayload", n, len(full), err)
		}
	}
}

func TestForwardRejectsIPv6(t *testing.T) {
	if _, err := (Forward{
		Source:      netip.MustParseAddr("::1"),
		Destination: netip.MustParseAddr("10.69.0.5"),
	}).Encode(nil); err == nil {
		t.Error("an IPv6 source encoded, and there are only four bytes for it")
	}
}

// A forwarded packet is the largest thing on the wire, and a receive buffer
// sized for anything less would truncate one without saying so.
func TestMaxDatagramHoldsAForwardedPacket(t *testing.T) {
	full, err := Forward{
		Source:      netip.MustParseAddr("10.69.0.2"),
		Destination: netip.MustParseAddr("10.69.0.5"),
		Packet:      make([]byte, MaxPacketSize),
	}.Encode(nil)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if got := HeaderSize + len(full) + AEADTagSize; got != MaxDatagramSize {
		t.Errorf("a full forwarded datagram is %d bytes, MaxDatagramSize is %d", got, MaxDatagramSize)
	}
}
