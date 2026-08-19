package transport

import (
	"bytes"
	"errors"
	"testing"

	"github.com/OMouta/192168/protocol"
)

func TestHeaderRoundTrip(t *testing.T) {
	want := Header{
		Version: protocol.TransportVersion,
		Type:    MsgData,
		Sender:  0xDEADBEEFCAFEF00D,
		Counter: 1 << 40,
	}
	payload := []byte("encrypted-ip-packet")

	pkt := want.Encode(nil, payload)
	if len(pkt) != HeaderSize+len(payload) {
		t.Fatalf("encoded length = %d, want %d", len(pkt), HeaderSize+len(payload))
	}

	got, rest, err := Decode(pkt)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != want {
		t.Errorf("header = %+v, want %+v", got, want)
	}
	if !bytes.Equal(rest, payload) {
		t.Errorf("payload = %q, want %q", rest, payload)
	}
}

func TestDecodeRejectsBadPackets(t *testing.T) {
	valid := Header{Version: protocol.TransportVersion, Type: MsgKeepalive}.Encode(nil, nil)

	tests := []struct {
		name string
		pkt  []byte
		want error
	}{
		{"empty", nil, ErrShortPacket},
		{"truncated", valid[:HeaderSize-1], ErrShortPacket},
		{"foreign traffic", append([]byte{0x00, 0x01}, valid[2:]...), ErrBadMagic},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := Decode(tt.pkt); !errors.Is(err, tt.want) {
				t.Errorf("Decode err = %v, want %v", err, tt.want)
			}
		})
	}

	future := append([]byte(nil), valid...)
	future[2] = protocol.TransportVersion + 1
	_, _, err := Decode(future)
	var vErr UnsupportedVersionError
	if !errors.As(err, &vErr) {
		t.Fatalf("Decode err = %v, want UnsupportedVersionError", err)
	}
	if vErr.Version != protocol.TransportVersion+1 {
		t.Errorf("reported version = %d, want %d", vErr.Version, protocol.TransportVersion+1)
	}
}

// The transport socket also carries STUN, whose messages always start with two
// zero bits. Keeping those bits set in Magic is what lets the two be told
// apart without parsing.
func TestMagicCannotBeConfusedWithSTUN(t *testing.T) {
	if Magic>>14 == 0 {
		t.Errorf("Magic = %#04x: leading two bits are zero, which collides with STUN", Magic)
	}
}
