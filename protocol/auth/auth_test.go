package auth

import (
	"crypto/ed25519"
	"testing"
	"time"
)

func TestRegisterSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	encoded := EncodePublicKey(pub)
	const transportKey = "dHJhbnNwb3J0LWtleQ"
	issuedAt := time.Unix(1_800_000_000, 0)

	sig := SignRegister(priv, "dev_123", encoded, transportKey, issuedAt, "nonce-1")
	if err := VerifyRegister(encoded, "dev_123", transportKey, sig, issuedAt, "nonce-1"); err != nil {
		t.Fatalf("VerifyRegister: %v", err)
	}

	// Every signed field has to actually be covered by the signature.
	tampered := []struct {
		name         string
		deviceID     string
		transportKey string
		issuedAt     time.Time
		nonce        string
	}{
		{"device id", "dev_456", transportKey, issuedAt, "nonce-1"},
		{"transport key", "dev_123", "c3dhcHBlZC1rZXk", issuedAt, "nonce-1"},
		{"timestamp", "dev_123", transportKey, issuedAt.Add(time.Second), "nonce-1"},
		{"nonce", "dev_123", transportKey, issuedAt, "nonce-2"},
	}
	for _, tt := range tampered {
		t.Run(tt.name, func(t *testing.T) {
			if err := VerifyRegister(encoded, tt.deviceID, tt.transportKey, sig, tt.issuedAt, tt.nonce); err == nil {
				t.Errorf("a changed %s still verified", tt.name)
			}
		})
	}

	otherPub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if err := VerifyRegister(EncodePublicKey(otherPub), "dev_123", transportKey, sig, issuedAt, "nonce-1"); err == nil {
		t.Error("a signature verified against the wrong public key")
	}
}

func TestDecodePublicKeyRejectsJunk(t *testing.T) {
	for _, v := range []string{"", "!!!!", "c2hvcnQ"} {
		if _, err := DecodePublicKey(v); err == nil {
			t.Errorf("DecodePublicKey(%q) returned no error", v)
		}
	}
}
