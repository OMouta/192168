package identity

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OMouta/192168/daemon/secret"
	"github.com/OMouta/192168/protocol/auth"
)

func TestIdentityIsCreatedOnceAndReused(t *testing.T) {
	dir := t.TempDir()

	first, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.HasPrefix(first.DeviceID, "dev_") {
		t.Errorf("device id = %q", first.DeviceID)
	}
	if first.Name == "" {
		t.Error("the identity has no name")
	}

	// A second run has to find the same device, or every restart would look
	// like a new machine and lose its group memberships.
	second, err := Load(dir)
	if err != nil {
		t.Fatalf("Load again: %v", err)
	}
	if second.DeviceID != first.DeviceID {
		t.Errorf("device id changed: %q then %q", first.DeviceID, second.DeviceID)
	}
	if !second.Signing.Equal(first.Signing) {
		t.Error("the signing key changed between runs")
	}
	if !bytes.Equal(second.Transport.Private, first.Transport.Private) {
		t.Error("the transport key changed between runs")
	}
}

func TestSigningKeySurvivesAReload(t *testing.T) {
	dir := t.TempDir()

	first, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// The point of persisting the key is that a signature made now still
	// verifies after a restart.
	issuedAt := time.Unix(1_800_000_000, 0)
	signature := auth.SignRegister(first.Signing, first.DeviceID, first.PublicKey(), first.TransportKey(), issuedAt, "nonce")

	second, err := Load(dir)
	if err != nil {
		t.Fatalf("Load again: %v", err)
	}
	if err := auth.VerifyRegister(second.PublicKey(), second.DeviceID, second.TransportKey(), signature, issuedAt, "nonce"); err != nil {
		t.Errorf("a signature from before the reload no longer verifies: %v", err)
	}
}

func TestTokenIsRememberedPerServer(t *testing.T) {
	dir := t.TempDir()

	id, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if id.RegisteredWith("https://api.192168.lol") {
		t.Error("a fresh identity claims to be registered")
	}

	if err := id.SetToken("https://api.192168.lol", "token-abc"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}

	reloaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load again: %v", err)
	}
	if reloaded.Token != "token-abc" {
		t.Errorf("token = %q", reloaded.Token)
	}
	if !reloaded.RegisteredWith("https://api.192168.lol") {
		t.Error("the identity forgot it was registered")
	}
	// A token is only good where it was issued, so pointing at another server
	// has to look like not being registered.
	if reloaded.RegisteredWith("https://lan.example.com") {
		t.Error("a token from one server counts as registration with another")
	}

	if err := reloaded.ClearToken(); err != nil {
		t.Fatalf("ClearToken: %v", err)
	}
	again, err := Load(dir)
	if err != nil {
		t.Fatalf("Load after clearing: %v", err)
	}
	if again.Token != "" || again.ServerURL != "" {
		t.Errorf("the token survived being cleared: %+v", again)
	}
}

func TestPrivateKeysAreNotWrittenInTheClear(t *testing.T) {
	dir := t.TempDir()

	id, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := id.SetToken("https://api.192168.lol", "token-abc"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("read identity file: %v", err)
	}

	// The public halves are meant to be readable.
	var stored storedIdentity
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("parse identity file: %v", err)
	}
	if stored.PublicKey != id.PublicKey() || stored.TransportKey != id.TransportKey() {
		t.Error("the public keys on disk do not match the loaded identity")
	}

	if !secret.Available() {
		t.Skip("this build does not protect secrets, so there is nothing to check")
	}
	for _, plaintext := range [][]byte{id.Signing.Seed(), id.Transport.Private, []byte("token-abc")} {
		if bytes.Contains(raw, plaintext) {
			t.Error("a secret appears in the identity file in the clear")
		}
	}
}

func TestACorruptIdentityFailsInsteadOfStartingOver(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}

	path := filepath.Join(dir, fileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read identity file: %v", err)
	}

	var stored storedIdentity
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("parse identity file: %v", err)
	}
	stored.Secrets = "bm90LWEtcmVhbC1ibG9i"
	broken, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, broken, 0o600); err != nil {
		t.Fatalf("write identity file: %v", err)
	}

	// Quietly generating a new identity here would drop every group this
	// machine belongs to, so it has to be an error the user sees.
	if _, err := Load(dir); err == nil && secret.Available() {
		t.Error("a damaged identity file was accepted")
	}
}

func TestEd25519KeyIsUsableAfterReload(t *testing.T) {
	dir := t.TempDir()
	id, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(id.Signing) != ed25519.PrivateKeySize {
		t.Fatalf("signing key is %d bytes, want %d", len(id.Signing), ed25519.PrivateKeySize)
	}
}
