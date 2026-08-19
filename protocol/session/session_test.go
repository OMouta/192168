package session

import (
	"bytes"
	"testing"

	"github.com/OMouta/192168/protocol"
	"github.com/OMouta/192168/protocol/transport"
)

// pair runs a full handshake and returns both open sessions.
func pair(t *testing.T) (initiator, responder *Session) {
	t.Helper()

	initiatorKeys, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	responderKeys, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	// The initiator knows the responder's static key because the coordination
	// server handed it over with the peer list.
	client, err := NewInitiator(initiatorKeys, responderKeys.Public)
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	server, err := NewResponder(responderKeys)
	if err != nil {
		t.Fatalf("NewResponder: %v", err)
	}

	init, err := client.WriteInit()
	if err != nil {
		t.Fatalf("WriteInit: %v", err)
	}
	if err := server.ReadInit(init); err != nil {
		t.Fatalf("ReadInit: %v", err)
	}
	if !bytes.Equal(server.PeerStatic(), initiatorKeys.Public) {
		t.Fatal("responder did not learn the initiator's static key")
	}

	reply, responderSession, err := server.WriteResponse()
	if err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}
	initiatorSession, err := client.ReadResponse(reply)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	return initiatorSession, responderSession
}

func TestHandshakeAgreesOnKeysInBothDirections(t *testing.T) {
	client, server := pair(t)

	header := transport.Header{Version: protocol.TransportVersion, Type: transport.MsgData, Sender: 1, Counter: 7}
	ad := header.Encode(nil, nil)
	plaintext := []byte("an ip packet from a game")

	sealed, err := client.Seal(nil, ad, plaintext, header.Counter)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(sealed, plaintext) {
		t.Fatal("the ciphertext contains the plaintext")
	}

	opened, err := server.Open(nil, ad, sealed, header.Counter)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Errorf("opened = %q, want %q", opened, plaintext)
	}

	// And the same in reverse, which is a different key.
	back := []byte("the reply")
	sealed, err = server.Seal(nil, ad, back, header.Counter)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	opened, err = client.Open(nil, ad, sealed, header.Counter)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened, back) {
		t.Errorf("opened = %q, want %q", opened, back)
	}
}

func TestOpenRejectsATamperedHeader(t *testing.T) {
	client, server := pair(t)

	header := transport.Header{Version: protocol.TransportVersion, Type: transport.MsgData, Sender: 1, Counter: 7}
	ad := header.Encode(nil, nil)

	sealed, err := client.Seal(nil, ad, []byte("payload"), header.Counter)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Someone in the middle rewrites the sender in the cleartext header.
	tampered := transport.Header{Version: protocol.TransportVersion, Type: transport.MsgData, Sender: 2, Counter: 7}
	if _, err := server.Open(nil, tampered.Encode(nil, nil), sealed, header.Counter); err == nil {
		t.Error("a packet with a rewritten header opened")
	}

	// Or claims a different counter than the one it was sealed under.
	if _, err := server.Open(nil, ad, sealed, 8); err == nil {
		t.Error("a packet opened under the wrong counter")
	}
}

func TestInitiatorWithTheWrongPeerKeyGetsNowhere(t *testing.T) {
	responderKeys, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	initiatorKeys, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	strangerKeys, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	// An endpoint that guesses at who it is talking to cannot get in, which is
	// the property that makes a server-supplied address safe to dial.
	client, err := NewInitiator(initiatorKeys, strangerKeys.Public)
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	server, err := NewResponder(responderKeys)
	if err != nil {
		t.Fatalf("NewResponder: %v", err)
	}

	init, err := client.WriteInit()
	if err != nil {
		t.Fatalf("WriteInit: %v", err)
	}
	if err := server.ReadInit(init); err == nil {
		t.Error("a handshake addressed to another key was accepted")
	}
}

func TestHandshakeRejectsOutOfOrderUse(t *testing.T) {
	keys, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	responder, err := NewResponder(keys)
	if err != nil {
		t.Fatalf("NewResponder: %v", err)
	}
	if _, err := responder.WriteInit(); err == nil {
		t.Error("a responder wrote an init message")
	}

	initiator, err := NewInitiator(keys, keys.Public)
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	if err := initiator.ReadInit(nil); err == nil {
		t.Error("an initiator read an init message")
	}
	if _, _, err := initiator.WriteResponse(); err == nil {
		t.Error("an initiator wrote a response")
	}
}

func TestNewInitiatorRejectsAMalformedPeerKey(t *testing.T) {
	keys, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	for _, bad := range [][]byte{nil, make([]byte, KeySize-1), make([]byte, KeySize+1)} {
		if _, err := NewInitiator(keys, bad); err == nil {
			t.Errorf("NewInitiator accepted a %d byte peer key", len(bad))
		}
	}
}
