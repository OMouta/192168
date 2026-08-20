// Package stun asks a STUN server what a socket looks like from the outside.
//
// A machine behind NAT cannot see its own public address and port. STUN is a
// server echoing back what it received from, which is the address a peer has to
// send to. RFC 5389, and only the one message that matters.
//
// Requests and responses are bytes here rather than a socket, because the
// answer is only true for the socket it was asked from. Peer traffic and STUN
// share one port, so the transport layer owns the socket and hands STUN
// packets to this package.
package stun

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
)

const (
	// bindingRequest and bindingSuccess are the only message types used.
	bindingRequest uint16 = 0x0001
	bindingSuccess uint16 = 0x0101

	// magicCookie is fixed by the RFC and identifies a STUN message.
	magicCookie uint32 = 0x2112A442

	headerSize = 20

	attrMappedAddress    uint16 = 0x0001
	attrXorMappedAddress uint16 = 0x0020

	familyIPv4 = 0x01
	familyIPv6 = 0x02
)

// ErrNotSTUN means the packet is not a STUN message.
var ErrNotSTUN = errors.New("stun: not a STUN message")

// TransactionID identifies one request and its reply.
type TransactionID [12]byte

// Request is a binding request waiting for its answer.
type Request struct {
	ID     TransactionID
	Packet []byte
}

// NewRequest builds a binding request with a fresh transaction ID.
func NewRequest() (Request, error) {
	var id TransactionID
	if _, err := rand.Read(id[:]); err != nil {
		return Request{}, fmt.Errorf("stun: transaction id: %w", err)
	}

	packet := make([]byte, headerSize)
	binary.BigEndian.PutUint16(packet[0:2], bindingRequest)
	binary.BigEndian.PutUint16(packet[2:4], 0) // no attributes
	binary.BigEndian.PutUint32(packet[4:8], magicCookie)
	copy(packet[8:20], id[:])

	return Request{ID: id, Packet: packet}, nil
}

// Looks reports whether a packet looks like STUN, so the transport can route it
// without parsing. The first two bits are zero in every STUN message and are
// set in every packet this project sends, which is how the socket carries both.
func Looks(packet []byte) bool {
	if len(packet) < headerSize {
		return false
	}
	if packet[0]&0xC0 != 0 {
		return false
	}
	return binary.BigEndian.Uint32(packet[4:8]) == magicCookie
}

// ParseResponse reads the address a STUN server saw. It checks the transaction
// ID, so a stale or spoofed reply for another request is refused.
func ParseResponse(id TransactionID, packet []byte) (netip.AddrPort, error) {
	if !Looks(packet) {
		return netip.AddrPort{}, ErrNotSTUN
	}
	if kind := binary.BigEndian.Uint16(packet[0:2]); kind != bindingSuccess {
		return netip.AddrPort{}, fmt.Errorf("stun: message type %#04x is not a binding success", kind)
	}
	if TransactionID(packet[8:20]) != id {
		return netip.AddrPort{}, errors.New("stun: reply is for a different request")
	}

	length := int(binary.BigEndian.Uint16(packet[2:4]))
	if headerSize+length > len(packet) {
		return netip.AddrPort{}, errors.New("stun: message is shorter than it claims")
	}
	body := packet[headerSize : headerSize+length]

	// A server may send both forms. The XOR one is preferred because middle
	// boxes rewrite addresses they recognise, and the obfuscation hides it.
	var fallback netip.AddrPort
	for len(body) >= 4 {
		kind := binary.BigEndian.Uint16(body[0:2])
		size := int(binary.BigEndian.Uint16(body[2:4]))
		if 4+size > len(body) {
			return netip.AddrPort{}, errors.New("stun: attribute runs past the message")
		}
		value := body[4 : 4+size]

		switch kind {
		case attrXorMappedAddress:
			addr, err := parseAddress(value, id, true)
			if err != nil {
				return netip.AddrPort{}, err
			}
			return addr, nil
		case attrMappedAddress:
			addr, err := parseAddress(value, id, false)
			if err == nil {
				fallback = addr
			}
		}

		// Attributes are padded to a multiple of four bytes.
		advance := 4 + size
		if pad := size % 4; pad != 0 {
			advance += 4 - pad
		}
		if advance > len(body) {
			break
		}
		body = body[advance:]
	}

	if fallback.IsValid() {
		return fallback, nil
	}
	return netip.AddrPort{}, errors.New("stun: the reply carried no address")
}

func parseAddress(value []byte, id TransactionID, xored bool) (netip.AddrPort, error) {
	if len(value) < 4 {
		return netip.AddrPort{}, errors.New("stun: address attribute is too short")
	}

	family := value[1]
	port := binary.BigEndian.Uint16(value[2:4])
	raw := value[4:]

	// The XOR form hides the address from anything that rewrites addresses it
	// recognises in transit.
	var key [16]byte
	binary.BigEndian.PutUint32(key[0:4], magicCookie)
	copy(key[4:], id[:])

	if xored {
		port ^= uint16(magicCookie >> 16)
	}

	switch family {
	case familyIPv4:
		if len(raw) < 4 {
			return netip.AddrPort{}, errors.New("stun: IPv4 address is too short")
		}
		var out [4]byte
		copy(out[:], raw[:4])
		if xored {
			for i := range out {
				out[i] ^= key[i]
			}
		}
		return netip.AddrPortFrom(netip.AddrFrom4(out), port), nil

	case familyIPv6:
		if len(raw) < 16 {
			return netip.AddrPort{}, errors.New("stun: IPv6 address is too short")
		}
		var out [16]byte
		copy(out[:], raw[:16])
		if xored {
			for i := range out {
				out[i] ^= key[i]
			}
		}
		return netip.AddrPortFrom(netip.AddrFrom16(out), port), nil

	default:
		return netip.AddrPort{}, fmt.Errorf("stun: unknown address family %#02x", family)
	}
}
