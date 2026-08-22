// Package identity is the device's long-lived name and keys.
//
// One installation, one identity, created on first run and reused forever
// after. It is separate from group membership and from an active session: the
// same identity belongs to every group this machine joins, and outlives all of
// them.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/OMouta/192168/daemon/secret"
	"github.com/OMouta/192168/protocol/auth"
	"github.com/OMouta/192168/protocol/session"
)

const fileName = "identity.json"

// Identity is this installation.
type Identity struct {
	// DeviceID names this installation to the server. It is random rather than
	// derived from anything about the machine.
	DeviceID string
	// Name is the machine's own name, which nobody chose.
	Name string
	// Nickname is what the person wants to be called, and what the rest of a
	// group sees. The server holds the real one; this is a copy, so the app can
	// say who you are before it has reached anything.
	Nickname string
	// Signing proves the device holds its identity key, which is what makes
	// registration trustworthy. It signs nothing else.
	Signing ed25519.PrivateKey
	// Transport is the static Curve25519 keypair peers authenticate against.
	Transport session.Keypair
	// Token authenticates every request after registration. Empty until the
	// device has registered with the current server.
	Token string
	// ServerURL is the server the token belongs to. Pointing the app at a
	// different server means registering again, since a token is only good
	// where it was issued.
	ServerURL string

	dir string
}

// storedIdentity is the on-disk shape. Public parts are readable, and
// everything that would let someone impersonate this device sits inside one
// protected blob.
type storedIdentity struct {
	DeviceID     string `json:"deviceId"`
	Name         string `json:"name"`
	Nickname     string `json:"nickname,omitempty"`
	PublicKey    string `json:"publicKey"`
	TransportKey string `json:"transportKey"`
	ServerURL    string `json:"serverUrl,omitempty"`
	Secrets      string `json:"secrets"`
}

// protectedSecrets is what goes inside the blob.
type protectedSecrets struct {
	SigningSeed  string `json:"signingSeed"`
	TransportKey string `json:"transportPrivateKey"`
	Token        string `json:"token,omitempty"`
}

// Load reads the identity in dir, creating one on first run.
func Load(dir string) (*Identity, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("identity: create %s: %w", dir, err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, fileName))
	if os.IsNotExist(err) {
		return create(dir)
	}
	if err != nil {
		return nil, fmt.Errorf("identity: read: %w", err)
	}

	var stored storedIdentity
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("identity: parse: %w", err)
	}

	sealed, err := base64.RawStdEncoding.DecodeString(stored.Secrets)
	if err != nil {
		return nil, fmt.Errorf("identity: decode secrets: %w", err)
	}
	opened, err := secret.Unprotect(sealed)
	if err != nil {
		// This is what a copied identity file looks like, or one from another
		// user account. Failing loudly beats silently starting a new identity
		// and losing every group membership.
		return nil, fmt.Errorf("identity: cannot unprotect %s, it may belong to another user or machine: %w", fileName, err)
	}

	var secrets protectedSecrets
	if err := json.Unmarshal(opened, &secrets); err != nil {
		return nil, fmt.Errorf("identity: parse secrets: %w", err)
	}

	seed, err := base64.RawStdEncoding.DecodeString(secrets.SigningSeed)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("identity: signing key is malformed")
	}
	transportPrivate, err := base64.RawStdEncoding.DecodeString(secrets.TransportKey)
	if err != nil || len(transportPrivate) != session.KeySize {
		return nil, fmt.Errorf("identity: transport key is malformed")
	}
	transportPublic, err := base64.RawStdEncoding.DecodeString(stored.TransportKey)
	if err != nil || len(transportPublic) != session.KeySize {
		return nil, fmt.Errorf("identity: transport public key is malformed")
	}

	return &Identity{
		DeviceID:  stored.DeviceID,
		Name:      stored.Name,
		Nickname:  stored.Nickname,
		Signing:   ed25519.NewKeyFromSeed(seed),
		Transport: session.Keypair{Private: transportPrivate, Public: transportPublic},
		Token:     secrets.Token,
		ServerURL: stored.ServerURL,
		dir:       dir,
	}, nil
}

func create(dir string) (*Identity, error) {
	deviceID, err := newDeviceID()
	if err != nil {
		return nil, err
	}
	_, signing, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("identity: generate signing key: %w", err)
	}
	transport, err := session.GenerateKeypair()
	if err != nil {
		return nil, err
	}

	name, err := os.Hostname()
	if err != nil || name == "" {
		name = "unknown"
	}

	// Until somebody says otherwise, the machine's name is what to call the
	// person at it. It is a guess, but it is a better first impression than an
	// empty space where a name should be.
	id := &Identity{
		DeviceID:  deviceID,
		Name:      name,
		Nickname:  name,
		Signing:   signing,
		Transport: transport,
		dir:       dir,
	}
	if err := id.save(); err != nil {
		return nil, err
	}
	return id, nil
}

// PublicKey is the encoded Ed25519 identity key, as the server stores it.
func (i *Identity) PublicKey() string {
	return auth.EncodePublicKey(i.Signing.Public().(ed25519.PublicKey))
}

// TransportKey is the encoded Curve25519 static key peers authenticate against.
func (i *Identity) TransportKey() string {
	return base64.RawStdEncoding.EncodeToString(i.Transport.Public)
}

// SetToken records the credential a server issued and which server issued it.
func (i *Identity) SetToken(serverURL, token string) error {
	i.ServerURL = serverURL
	i.Token = token
	return i.save()
}

// SetNickname records what this device is called. The server is told
// separately; this is the copy the app reads when it cannot reach one.
func (i *Identity) SetNickname(nickname string) error {
	if nickname == i.Nickname {
		return nil
	}
	i.Nickname = nickname
	return i.save()
}

// ClearToken forgets the current credential, which is what happens when a
// server rejects it or the user switches servers.
func (i *Identity) ClearToken() error {
	i.Token = ""
	i.ServerURL = ""
	return i.save()
}

// RegisteredWith reports whether this identity already holds a token for a
// server, so the daemon can skip registering again.
func (i *Identity) RegisteredWith(serverURL string) bool {
	return i.Token != "" && i.ServerURL == serverURL
}

func (i *Identity) save() error {
	secrets, err := json.Marshal(protectedSecrets{
		SigningSeed:  base64.RawStdEncoding.EncodeToString(i.Signing.Seed()),
		TransportKey: base64.RawStdEncoding.EncodeToString(i.Transport.Private),
		Token:        i.Token,
	})
	if err != nil {
		return fmt.Errorf("identity: encode secrets: %w", err)
	}
	sealed, err := secret.Protect(secrets)
	if err != nil {
		return err
	}

	body, err := json.MarshalIndent(storedIdentity{
		DeviceID:     i.DeviceID,
		Name:         i.Name,
		Nickname:     i.Nickname,
		PublicKey:    i.PublicKey(),
		TransportKey: i.TransportKey(),
		ServerURL:    i.ServerURL,
		Secrets:      base64.RawStdEncoding.EncodeToString(sealed),
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("identity: encode: %w", err)
	}

	// Write to a temporary file and rename, so a crash halfway through leaves
	// the previous identity intact rather than a truncated one. Losing this
	// file means losing every group membership.
	path := filepath.Join(i.dir, fileName)
	temp := path + ".new"
	if err := os.WriteFile(temp, body, 0o600); err != nil {
		return fmt.Errorf("identity: write: %w", err)
	}
	if err := os.Rename(temp, path); err != nil {
		os.Remove(temp)
		return fmt.Errorf("identity: replace: %w", err)
	}
	return nil
}

// deviceIDAlphabet leaves out the characters that get misread when an ID is
// copied out of a log.
var deviceIDAlphabet = base32.NewEncoding("abcdefghijkmnpqrstuvwxyz23456789").WithPadding(base32.NoPadding)

func newDeviceID() (string, error) {
	raw := make([]byte, 10)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("identity: new device id: %w", err)
	}
	return "dev_" + deviceIDAlphabet.EncodeToString(raw), nil
}
