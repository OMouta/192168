package transport

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
)

// ErrMalformedPayload means a packet had a valid envelope but its body did not
// match the message type in the header.
var ErrMalformedPayload = errors.New("transport: malformed payload")

// Ping is the body of MsgProbe and MsgKeepalive, which are the same shape
// because they ask the same question: can you hear me.
//
// The sender picks a Token, remembers when it sent it, and the peer echoes it
// back with Reply set. Round trip time is the gap between those two moments,
// measured locally, so neither side has to trust the other's clock.
//
// A probe travels before there are session keys, so it is unauthenticated and
// proves nothing on its own. It only tells a daemon that a path exists and is
// worth running a handshake over. A keepalive is inside an established session
// and is authenticated like any other packet.
type Ping struct {
	Token uint64
	Reply bool
}

// PingSize is the encoded size of a Ping.
const PingSize = 9

// Encode appends the ping to dst.
func (p Ping) Encode(dst []byte) []byte {
	var buf [PingSize]byte
	binary.BigEndian.PutUint64(buf[0:8], p.Token)
	if p.Reply {
		buf[8] = 1
	}
	return append(dst, buf[:]...)
}

// DecodePing parses the body of a probe or keepalive packet.
func DecodePing(payload []byte) (Ping, error) {
	if len(payload) < PingSize {
		return Ping{}, fmt.Errorf("%w: ping is %d bytes, want %d", ErrMalformedPayload, len(payload), PingSize)
	}
	return Ping{
		Token: binary.BigEndian.Uint64(payload[0:8]),
		Reply: payload[8] == 1,
	}, nil
}

// HandshakeInit is the body of MsgHandshakeInit.
//
// DeviceID tells the responder who is calling, so it can look up the public key
// the coordination server gave it for that peer. Without it the responder would
// have to try every peer's key against the message.
//
// KeyExchange carries the key agreement message itself and stays opaque here on
// purpose. The envelope does not care which construction produces those bytes,
// so choosing one does not change the wire format.
type HandshakeInit struct {
	DeviceID    string
	KeyExchange []byte
}

// Encode appends the handshake to dst.
func (h HandshakeInit) Encode(dst []byte) ([]byte, error) {
	if len(h.DeviceID) > 255 {
		return nil, fmt.Errorf("transport: device id is %d bytes, max 255", len(h.DeviceID))
	}
	if len(h.KeyExchange) > 65535 {
		return nil, fmt.Errorf("transport: key exchange is %d bytes, max 65535", len(h.KeyExchange))
	}
	dst = append(dst, byte(len(h.DeviceID)))
	dst = append(dst, h.DeviceID...)
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(h.KeyExchange)))
	return append(dst, h.KeyExchange...), nil
}

// DecodeHandshakeInit parses the body of a handshake init packet. The returned
// KeyExchange aliases payload.
func DecodeHandshakeInit(payload []byte) (HandshakeInit, error) {
	if len(payload) < 1 {
		return HandshakeInit{}, fmt.Errorf("%w: handshake init is empty", ErrMalformedPayload)
	}
	idLen := int(payload[0])
	rest := payload[1:]
	if len(rest) < idLen+2 {
		return HandshakeInit{}, fmt.Errorf("%w: handshake init truncated", ErrMalformedPayload)
	}

	h := HandshakeInit{DeviceID: string(rest[:idLen])}
	rest = rest[idLen:]

	keyLen := int(binary.BigEndian.Uint16(rest[0:2]))
	rest = rest[2:]
	if len(rest) < keyLen {
		return HandshakeInit{}, fmt.Errorf("%w: key exchange truncated", ErrMalformedPayload)
	}
	h.KeyExchange = rest[:keyLen]
	return h, nil
}

// HandshakeResponse is the body of MsgHandshakeResponse. The responder is
// already identified by the header's Sender, so this only has to carry the
// second key agreement message.
type HandshakeResponse struct {
	KeyExchange []byte
}

// Encode appends the response to dst.
func (h HandshakeResponse) Encode(dst []byte) ([]byte, error) {
	if len(h.KeyExchange) > 65535 {
		return nil, fmt.Errorf("transport: key exchange is %d bytes, max 65535", len(h.KeyExchange))
	}
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(h.KeyExchange)))
	return append(dst, h.KeyExchange...), nil
}

// DecodeHandshakeResponse parses the body of a handshake response packet. The
// returned KeyExchange aliases payload.
func DecodeHandshakeResponse(payload []byte) (HandshakeResponse, error) {
	if len(payload) < 2 {
		return HandshakeResponse{}, fmt.Errorf("%w: handshake response is empty", ErrMalformedPayload)
	}
	keyLen := int(binary.BigEndian.Uint16(payload[0:2]))
	rest := payload[2:]
	if len(rest) < keyLen {
		return HandshakeResponse{}, fmt.Errorf("%w: key exchange truncated", ErrMalformedPayload)
	}
	return HandshakeResponse{KeyExchange: rest[:keyLen]}, nil
}

// Forward is the body of MsgForward: one peer's packet on its way to another
// peer through a third.
//
// What it carries is a complete transport packet between the two ends, already
// sealed with keys only those two hold. The relay authenticates the hop it
// received, reads the two addresses, and passes the rest on without being able
// to open it. That is the difference between lending somebody a path and
// reading their traffic.
type Forward struct {
	// HopLimit is how many more times this packet may be passed on. A relay
	// that receives one at zero drops it, so a mistake in the routing costs a
	// packet rather than becoming one that circles forever.
	HopLimit uint8

	// Source and Destination are virtual addresses rather than device ids
	// because every daemon already indexes its peers by them, and because four
	// bytes each is what the tunnel can afford: this header is paid on top of a
	// packet that may already be full size.
	Source      netip.Addr
	Destination netip.Addr

	// Packet is the transport packet being carried, envelope and all.
	Packet []byte
}

// ForwardHeaderSize is the fixed part of a forward payload, in front of the
// packet it carries.
//
//	hopLimit(1) | source(4) | destination(4)
const ForwardHeaderSize = 9

// Encode appends the forward to dst. Both addresses have to be IPv4, which
// every virtual address in a group is.
func (f Forward) Encode(dst []byte) ([]byte, error) {
	if !f.Source.Is4() || !f.Destination.Is4() {
		return nil, fmt.Errorf("transport: forward needs IPv4 addresses, got %s and %s", f.Source, f.Destination)
	}

	source, destination := f.Source.As4(), f.Destination.As4()
	dst = append(dst, f.HopLimit)
	dst = append(dst, source[:]...)
	dst = append(dst, destination[:]...)
	return append(dst, f.Packet...), nil
}

// DecodeForward parses the body of a forward packet. The returned Packet
// aliases payload.
func DecodeForward(payload []byte) (Forward, error) {
	if len(payload) < ForwardHeaderSize+HeaderSize {
		return Forward{}, fmt.Errorf("%w: forward is %d bytes, too short to carry a packet", ErrMalformedPayload, len(payload))
	}
	return Forward{
		HopLimit:    payload[0],
		Source:      netip.AddrFrom4([4]byte(payload[1:5])),
		Destination: netip.AddrFrom4([4]byte(payload[5:9])),
		Packet:      payload[ForwardHeaderSize:],
	}, nil
}
