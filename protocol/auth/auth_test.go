package auth

import (
	"crypto/ed25519"
	"strings"
	"testing"
	"time"
)

func TestGroupProofIsStableAcrossDevices(t *testing.T) {
	// Two people typing the same password for the same group have to arrive at
	// the same proof, or only the creator could ever join.
	a := DeriveGroupProof("correct horse battery staple", "Friday Night")
	b := DeriveGroupProof("correct horse battery staple", "  friday night ")
	if a != b {
		t.Errorf("proofs differ across name spelling: %q vs %q", a, b)
	}

	other := DeriveGroupProof("correct horse battery staple", "BeamNG")
	if a == other {
		t.Error("the same password in different groups derives the same proof")
	}

	wrong := DeriveGroupProof("Correct horse battery staple", "Friday Night")
	if a == wrong {
		t.Error("proof ignores password case")
	}
}

func TestGroupVerifier(t *testing.T) {
	proof := DeriveGroupProof("hunter2", "The Boys")

	verifier, err := NewGroupVerifier(proof)
	if err != nil {
		t.Fatalf("NewGroupVerifier: %v", err)
	}
	if strings.Contains(verifier, proof) {
		t.Fatal("verifier contains the proof it is meant to hide")
	}

	ok, err := VerifyGroupProof(verifier, proof)
	if err != nil {
		t.Fatalf("VerifyGroupProof: %v", err)
	}
	if !ok {
		t.Error("the right proof did not verify")
	}

	ok, err = VerifyGroupProof(verifier, DeriveGroupProof("hunter3", "The Boys"))
	if err != nil {
		t.Fatalf("VerifyGroupProof: %v", err)
	}
	if ok {
		t.Error("the wrong proof verified")
	}
}

func TestGroupVerifierIsSaltedPerGroup(t *testing.T) {
	proof := DeriveGroupProof("hunter2", "The Boys")

	first, err := NewGroupVerifier(proof)
	if err != nil {
		t.Fatalf("NewGroupVerifier: %v", err)
	}
	second, err := NewGroupVerifier(proof)
	if err != nil {
		t.Fatalf("NewGroupVerifier: %v", err)
	}
	if first == second {
		t.Error("two verifiers for the same proof are identical, so the salt is not random")
	}
}

func TestVerifyGroupProofRejectsMalformedVerifiers(t *testing.T) {
	tests := []string{
		"",
		"not-a-verifier",
		"$argon2i$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=99$m=19456,t=2,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=19456,t=2$c2FsdA$aGFzaA",
	}
	for _, v := range tests {
		if _, err := VerifyGroupProof(v, "proof"); err == nil {
			t.Errorf("VerifyGroupProof(%q) returned no error", v)
		}
	}
}

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
