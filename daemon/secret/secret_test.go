package secret

import (
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	plaintext := []byte("an ed25519 private key")

	sealed, err := Protect(plaintext)
	if err != nil {
		t.Fatalf("Protect: %v", err)
	}
	if bytes.Equal(sealed, plaintext) {
		t.Fatal("Protect returned the input unchanged")
	}
	if Available() && bytes.Contains(sealed, plaintext) {
		t.Fatal("the protected blob still contains the plaintext")
	}

	opened, err := Unprotect(sealed)
	if err != nil {
		t.Fatalf("Unprotect: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Errorf("opened = %q, want %q", opened, plaintext)
	}
}

func TestUnprotectRejectsGarbage(t *testing.T) {
	if !Available() {
		t.Skip("this build does not protect anything, so there is nothing to reject")
	}
	if _, err := Unprotect([]byte("not a real blob")); err == nil {
		t.Error("Unprotect accepted junk")
	}
}
