// Package session is the cryptography behind a peer link.
//
// The handshake is Noise IK over Curve25519, ChaCha20-Poly1305, and BLAKE2s.
// IK fits because the coordination server already told the initiator which
// static key its peer has, so the first message can be encrypted to that peer
// and answered in one round trip. Two messages and the link is up.
//
// Nothing here is new cryptography. The pattern, the primitives, and the
// implementation are all off the shelf, and this package only wires them to the
// packet envelope in protocol/transport.
package session

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strconv"

	"github.com/OMouta/192168/protocol"
	"github.com/flynn/noise"
)

// KeySize is the length of a static or ephemeral Curve25519 public key.
const KeySize = 32

// prologue binds a handshake to this transport version, so a handshake for one
// version can never be replayed into another.
var prologue = []byte(protocol.Name + "-transport-v" + strconv.Itoa(protocol.TransportVersion))

var cipherSuite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashBLAKE2s)

// Keypair is a device's static Curve25519 keypair. It is separate from the
// Ed25519 identity key that signs device registration. One signs, one does key
// agreement, and neither has to do a job it was not built for.
type Keypair = noise.DHKey

// GenerateKeypair creates a device's static keypair. The private half stays on
// the machine and the public half goes to the server, which hands it to peers.
func GenerateKeypair() (Keypair, error) {
	kp, err := noise.DH25519.GenerateKeypair(rand.Reader)
	if err != nil {
		return Keypair{}, fmt.Errorf("session: generate keypair: %w", err)
	}
	return kp, nil
}

// Handshake runs one side of the two message exchange. A Handshake is used once
// and then thrown away.
type Handshake struct {
	state     *noise.HandshakeState
	initiator bool
}

// NewInitiator starts a handshake toward a peer whose static public key came
// from the coordination server. If that key is wrong the peer cannot read the
// first message, which is what stops an endpoint from impersonating anyone.
func NewInitiator(static Keypair, peerStatic []byte) (*Handshake, error) {
	if len(peerStatic) != KeySize {
		return nil, fmt.Errorf("session: peer key is %d bytes, want %d", len(peerStatic), KeySize)
	}
	state, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:   cipherSuite,
		Random:        rand.Reader,
		Pattern:       noise.HandshakeIK,
		Initiator:     true,
		Prologue:      prologue,
		StaticKeypair: static,
		PeerStatic:    peerStatic,
	})
	if err != nil {
		return nil, fmt.Errorf("session: new initiator: %w", err)
	}
	return &Handshake{state: state, initiator: true}, nil
}

// NewResponder waits for a handshake. The responder does not know who is
// calling until it reads the first message.
func NewResponder(static Keypair) (*Handshake, error) {
	state, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:   cipherSuite,
		Random:        rand.Reader,
		Pattern:       noise.HandshakeIK,
		Initiator:     false,
		Prologue:      prologue,
		StaticKeypair: static,
	})
	if err != nil {
		return nil, fmt.Errorf("session: new responder: %w", err)
	}
	return &Handshake{state: state}, nil
}

// WriteInit produces the initiator's message, which goes in the body of a
// MsgHandshakeInit packet.
func (h *Handshake) WriteInit() ([]byte, error) {
	if !h.initiator {
		return nil, errors.New("session: responder cannot write the init message")
	}
	msg, _, _, err := h.state.WriteMessage(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("session: write init: %w", err)
	}
	return msg, nil
}

// ReadInit consumes the initiator's message. After this the responder can read
// PeerStatic and decide whether that device belongs in the group.
func (h *Handshake) ReadInit(msg []byte) error {
	if h.initiator {
		return errors.New("session: initiator cannot read the init message")
	}
	if _, _, _, err := h.state.ReadMessage(nil, msg); err != nil {
		return fmt.Errorf("session: read init: %w", err)
	}
	return nil
}

// WriteResponse produces the responder's reply and opens the session. The reply
// goes in the body of a MsgHandshakeResponse packet.
func (h *Handshake) WriteResponse() ([]byte, *Session, error) {
	if h.initiator {
		return nil, nil, errors.New("session: initiator cannot write the response")
	}
	// The two cipher states always come back in the same order. The first
	// carries initiator to responder traffic and the second carries the reply,
	// so a responder sends on the second and receives on the first.
	msg, toResponder, toInitiator, err := h.state.WriteMessage(nil, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("session: write response: %w", err)
	}
	if toResponder == nil || toInitiator == nil {
		return nil, nil, errors.New("session: handshake ended without keys")
	}
	return msg, &Session{
		send:       toInitiator,
		recv:       toResponder,
		peerStatic: h.state.PeerStatic(),
	}, nil
}

// ReadResponse consumes the responder's reply and opens the session.
func (h *Handshake) ReadResponse(msg []byte) (*Session, error) {
	if !h.initiator {
		return nil, errors.New("session: responder cannot read the response")
	}
	_, toResponder, toInitiator, err := h.state.ReadMessage(nil, msg)
	if err != nil {
		return nil, fmt.Errorf("session: read response: %w", err)
	}
	if toResponder == nil || toInitiator == nil {
		return nil, errors.New("session: handshake ended without keys")
	}
	return &Session{
		send:       toResponder,
		recv:       toInitiator,
		peerStatic: h.state.PeerStatic(),
	}, nil
}

// PeerStatic is the other side's static public key, known to the responder once
// it has read the init message and to the initiator from the start. The caller
// has to check it against the group's peer list, because a valid handshake only
// proves the key is held, not that it is welcome.
func (h *Handshake) PeerStatic() []byte { return h.state.PeerStatic() }

// Session encrypts and decrypts packets for one peer link.
//
// The nonce is the counter from the packet header rather than a count kept
// inside the cipher state, so a receiver can tell a replay from a reordered
// packet and reject the first without dropping the second.
type Session struct {
	send       *noise.CipherState
	recv       *noise.CipherState
	peerStatic []byte
}

// PeerStatic is the static public key of the peer on the other end.
func (s *Session) PeerStatic() []byte { return s.peerStatic }

// Seal encrypts plaintext for the peer and appends it to dst. The additional
// data is the packet header, which travels in the clear and must not be
// changeable.
func (s *Session) Seal(dst, additionalData, plaintext []byte, counter uint64) ([]byte, error) {
	s.send.SetNonce(counter)
	out, err := s.send.Encrypt(dst, additionalData, plaintext)
	if err != nil {
		return nil, fmt.Errorf("session: seal: %w", err)
	}
	return out, nil
}

// Open decrypts a packet from the peer and appends the plaintext to dst. A
// failure means the packet was forged, corrupted, or altered, and the caller
// drops it without changing any state.
func (s *Session) Open(dst, additionalData, ciphertext []byte, counter uint64) ([]byte, error) {
	s.recv.SetNonce(counter)
	out, err := s.recv.Decrypt(dst, additionalData, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("session: open: %w", err)
	}
	return out, nil
}
