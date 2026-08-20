//go:build !windows

// Package secret protects local secrets at rest.
//
// Windows is the only platform the app ships on. This build exists so the
// daemon compiles and its tests run elsewhere, and it does not protect
// anything, which is why Available reports false.
package secret

import "bytes"

const marker = "192168-unprotected-v1:"

// Available reports whether secrets get real protection here. They do not.
func Available() bool { return false }

// Protect tags the plaintext and hands it back. Callers that care about real
// protection check Available first.
func Protect(plaintext []byte) ([]byte, error) {
	return append([]byte(marker), plaintext...), nil
}

// Unprotect strips the tag.
func Unprotect(ciphertext []byte) ([]byte, error) {
	return bytes.TrimPrefix(ciphertext, []byte(marker)), nil
}
