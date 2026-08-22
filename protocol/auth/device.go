// Package auth defines how a device proves who it is.
//
// A device signs its registration with the private half of a key pair that
// never leaves the machine, which is what makes the registration trustworthy.
// After that it carries a bearer token.
//
// Groups have no secret of their own. Getting into one means holding its invite
// code, and a code is checked by looking it up rather than by proving anything.
package auth

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TokenScheme is the Authorization scheme the control API uses. Everything
// after device registration carries "Authorization: Bearer <device token>".
const TokenScheme = "Bearer"

// registerContext domain-separates the registration signature, so a signature
// captured here cannot be replayed as any other signed message.
const registerContext = "192168-device-register-v1"

// RegisterMaxSkew is how far a registration timestamp may be from the server's
// clock. It bounds how long a captured registration stays replayable.
const RegisterMaxSkew = 5 * time.Minute

// EncodePublicKey renders a device public key for transport and storage.
func EncodePublicKey(pub ed25519.PublicKey) string {
	return base64.RawStdEncoding.EncodeToString(pub)
}

// DecodePublicKey parses a device public key and rejects anything that is not
// a well formed Ed25519 key.
func DecodePublicKey(encoded string) (ed25519.PublicKey, error) {
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("auth: malformed public key: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("auth: public key is %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// RegisterSigningInput builds the exact bytes both sides sign and verify. It is
// a fixed field order with newline separators, so there is no canonical JSON
// problem to get wrong.
//
// The transport key is in here too. It is the key peers authenticate each other
// with, so letting it be swapped in transit would hand an attacker the whole
// point of the handshake.
func RegisterSigningInput(deviceID, publicKey, transportKey string, issuedAt time.Time, nonce string) []byte {
	var b strings.Builder
	b.WriteString(registerContext)
	b.WriteByte('\n')
	b.WriteString(deviceID)
	b.WriteByte('\n')
	b.WriteString(publicKey)
	b.WriteByte('\n')
	b.WriteString(transportKey)
	b.WriteByte('\n')
	b.WriteString(strconv.FormatInt(issuedAt.Unix(), 10))
	b.WriteByte('\n')
	b.WriteString(nonce)
	return []byte(b.String())
}

// SignRegister signs a registration with the device's private key, proving the
// device holds the key it is registering.
func SignRegister(priv ed25519.PrivateKey, deviceID, publicKey, transportKey string, issuedAt time.Time, nonce string) string {
	sig := ed25519.Sign(priv, RegisterSigningInput(deviceID, publicKey, transportKey, issuedAt, nonce))
	return base64.RawStdEncoding.EncodeToString(sig)
}

// VerifyRegister checks a registration signature against the public key being
// registered. The server still has to reject a stale issuedAt and a replayed
// nonce, which this cannot see.
func VerifyRegister(publicKey, deviceID, transportKey, signature string, issuedAt time.Time, nonce string) error {
	pub, err := DecodePublicKey(publicKey)
	if err != nil {
		return err
	}
	sig, err := base64.RawStdEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("auth: malformed signature: %w", err)
	}
	if !ed25519.Verify(pub, RegisterSigningInput(deviceID, publicKey, transportKey, issuedAt, nonce), sig) {
		return fmt.Errorf("auth: signature does not verify")
	}
	return nil
}
