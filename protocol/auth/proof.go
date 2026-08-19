// Package auth defines how a device proves who it is and how a group password
// is turned into something safe to send and store.
//
// The rules both sides follow:
//
//   - The server never receives a group password. The client runs a KDF over it
//     first and sends the result, called the proof.
//   - The server never stores the proof either. It stores an Argon2id verifier
//     over the proof, so a stolen database does not hand out group access.
//   - A device proves it holds its private key by signing its registration.
//     After that it carries a bearer token.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Client-side KDF parameters. These run once when a user creates or joins a
// group, so they are set for an attacker's cost rather than the user's
// convenience. An offline attack on a leaked verifier pays this per guess.
const (
	proofTime    uint32 = 3
	proofMemory  uint32 = 64 * 1024 // KiB
	proofThreads uint8  = 4
	proofLength  uint32 = 32
)

// Server-side verifier parameters. The input is already the output of the
// parameters above, so this layer only has to stop a leaked database from being
// replayed directly. It runs on every join attempt and stays cheap on purpose.
const (
	verifierTime    uint32 = 2
	verifierMemory  uint32 = 19 * 1024 // KiB
	verifierThreads uint8  = 1
	verifierLength  uint32 = 32
	verifierSaltLen        = 16
)

// proofContext keeps this KDF output from being useful anywhere else, and
// changing it invalidates every existing proof, so it carries a version.
const proofContext = "192168-group-proof-v1:"

// DeriveGroupProof turns a group password into the value the client sends to
// the server. Two people joining the same group have to derive the same proof,
// so the salt is the group name rather than something random.
//
// The group name is matched case-insensitively and with surrounding whitespace
// removed. It is not Unicode-normalized, so a name containing combining
// characters has to reach this function in the same form it was created with.
func DeriveGroupProof(password, groupName string) string {
	salt := []byte(proofContext + NormalizeGroupName(groupName))
	sum := argon2.IDKey([]byte(password), salt, proofTime, proofMemory, proofThreads, proofLength)
	return base64.RawStdEncoding.EncodeToString(sum)
}

// NormalizeGroupName is how a group name is compared and salted. The server
// uses it for lookups so that the name a user types finds the group they meant.
func NormalizeGroupName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// NewGroupVerifier hashes a proof for storage. The result carries its own
// parameters, so raising them later does not break existing groups.
func NewGroupVerifier(proof string) (string, error) {
	if proof == "" {
		return "", fmt.Errorf("auth: empty proof")
	}
	salt := make([]byte, verifierSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	sum := argon2.IDKey([]byte(proof), salt, verifierTime, verifierMemory, verifierThreads, verifierLength)
	return encodeVerifier(params{
		time:    verifierTime,
		memory:  verifierMemory,
		threads: verifierThreads,
	}, salt, sum), nil
}

// VerifyGroupProof reports whether a proof matches a stored verifier. It
// re-reads the parameters from the verifier rather than assuming the current
// constants, and compares in constant time.
func VerifyGroupProof(verifier, proof string) (bool, error) {
	p, salt, want, err := decodeVerifier(verifier)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(proof), salt, p.time, p.memory, p.threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

type params struct {
	time    uint32
	memory  uint32
	threads uint8
}

// encodeVerifier writes the PHC string format, the same layout the reference
// Argon2 tools produce.
func encodeVerifier(p params, salt, sum []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.memory, p.time, p.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum),
	)
}

func decodeVerifier(verifier string) (params, []byte, []byte, error) {
	fields := strings.Split(verifier, "$")
	// A leading $ means the first field is empty: "", alg, version, params, salt, hash.
	if len(fields) != 6 || fields[0] != "" {
		return params{}, nil, nil, fmt.Errorf("auth: malformed verifier")
	}
	if fields[1] != "argon2id" {
		return params{}, nil, nil, fmt.Errorf("auth: unsupported algorithm %q", fields[1])
	}

	var version int
	if _, err := fmt.Sscanf(fields[2], "v=%d", &version); err != nil {
		return params{}, nil, nil, fmt.Errorf("auth: malformed verifier version: %w", err)
	}
	if version != argon2.Version {
		return params{}, nil, nil, fmt.Errorf("auth: unsupported argon2 version %d", version)
	}

	var p params
	if _, err := fmt.Sscanf(fields[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return params{}, nil, nil, fmt.Errorf("auth: malformed verifier parameters: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(fields[4])
	if err != nil {
		return params{}, nil, nil, fmt.Errorf("auth: malformed verifier salt: %w", err)
	}
	sum, err := base64.RawStdEncoding.DecodeString(fields[5])
	if err != nil {
		return params{}, nil, nil, fmt.Errorf("auth: malformed verifier hash: %w", err)
	}
	return p, salt, sum, nil
}
