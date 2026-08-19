// Package transport defines the peer-to-peer UDP wire format.
//
// This is the data plane, so it is binary and deliberately small. Every packet
// on the wire is authenticated: an endpoint learned from the coordination
// server proves nothing on its own, so a peer only accepts packets that verify
// against the session keys established during the handshake.
package transport

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/OMouta/192168/protocol"
)

// Magic marks a packet as ours and lets a daemon drop unrelated UDP noise
// before doing any cryptographic work.
//
// The two most significant bits are set on purpose: the same socket is used
// for STUN, and every STUN message begins with two zero bits, so a peer packet
// can never be mistaken for a STUN reply or the reverse.
const Magic uint16 = 0xC192

// MessageType identifies the packet's role in the peer session.
type MessageType uint8

const (
	// MsgProbe is an unencrypted hole-punch probe. It only carries enough to
	// be recognized and echoed; it is never trusted for state changes.
	MsgProbe MessageType = 1
	// MsgHandshakeInit starts the authenticated key exchange.
	MsgHandshakeInit MessageType = 2
	// MsgHandshakeResponse completes it.
	MsgHandshakeResponse MessageType = 3
	// MsgKeepalive keeps the NAT mapping open and measures round-trip time.
	MsgKeepalive MessageType = 4
	// MsgData carries an encrypted IP packet read from the virtual adapter.
	MsgData MessageType = 5
	// MsgClose tells the peer the session is going away, so the other side
	// does not have to wait for a timeout.
	MsgClose MessageType = 6
	// MsgForward is reserved for peer-assisted routing. Its payload carries
	// the final destination and a hop limit; it is not implemented yet.
	MsgForward MessageType = 7
)

// HeaderSize is the fixed size of the packet envelope in bytes.
//
//	magic(2) | version(1) | type(1) | sender(8) | counter(8)
const HeaderSize = 20

// Header is the envelope in front of every packet. It is transmitted in the
// clear and authenticated as additional data by the AEAD protecting Payload,
// so it can be read for routing before decryption but cannot be tampered with.
type Header struct {
	Version uint8
	Type    MessageType
	// Sender identifies the sending peer session to the receiver. It is
	// assigned during the handshake and is meaningless outside it.
	Sender uint64
	// Counter is a per-session monotonic counter. It doubles as the AEAD
	// nonce input and as the value the replay window is checked against.
	Counter uint64
}

var (
	// ErrShortPacket means the datagram cannot hold a header.
	ErrShortPacket = errors.New("transport: packet shorter than header")
	// ErrBadMagic means the datagram is not ours.
	ErrBadMagic = errors.New("transport: bad magic")
)

// UnsupportedVersionError is returned for a packet from an incompatible peer.
type UnsupportedVersionError struct {
	Version uint8
}

func (e UnsupportedVersionError) Error() string {
	return fmt.Sprintf("transport: unsupported protocol version %d", e.Version)
}

// Encode appends the header and payload to dst and returns the result. Reusing
// a buffer across packets keeps the per-packet path allocation-free.
func (h Header) Encode(dst []byte, payload []byte) []byte {
	var hdr [HeaderSize]byte
	binary.BigEndian.PutUint16(hdr[0:2], Magic)
	hdr[2] = h.Version
	hdr[3] = byte(h.Type)
	binary.BigEndian.PutUint64(hdr[4:12], h.Sender)
	binary.BigEndian.PutUint64(hdr[12:20], h.Counter)
	dst = append(dst, hdr[:]...)
	return append(dst, payload...)
}

// Decode parses the envelope of one received datagram and returns the header
// along with the remaining payload, which aliases pkt.
func Decode(pkt []byte) (Header, []byte, error) {
	if len(pkt) < HeaderSize {
		return Header{}, nil, ErrShortPacket
	}
	if binary.BigEndian.Uint16(pkt[0:2]) != Magic {
		return Header{}, nil, ErrBadMagic
	}
	h := Header{
		Version: pkt[2],
		Type:    MessageType(pkt[3]),
		Sender:  binary.BigEndian.Uint64(pkt[4:12]),
		Counter: binary.BigEndian.Uint64(pkt[12:20]),
	}
	if h.Version != protocol.TransportVersion {
		return h, nil, UnsupportedVersionError{Version: h.Version}
	}
	return h, pkt[HeaderSize:], nil
}
