//go:build windows

// Package secret protects local secrets at rest.
//
// On Windows that means DPAPI, so the device's private keys and its server
// token are encrypted with a key derived from the user account. Another user on
// the same machine cannot read them, and neither can anything that copies the
// file elsewhere.
package secret

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// entropy is mixed into the DPAPI key so a blob from this application cannot be
// swapped for one produced by anything else running as the same user.
var entropy = []byte("192168-local-secret-v1")

// Available reports whether secrets get real protection here.
func Available() bool { return true }

// Protect encrypts plaintext for the current user account.
func Protect(plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, fmt.Errorf("secret: nothing to protect")
	}

	in := blob(plaintext)
	extra := blob(entropy)
	var out windows.DataBlob

	// UI_FORBIDDEN because a daemon has no window to prompt from. If DPAPI
	// would need to ask the user something, it has to fail instead.
	err := windows.CryptProtectData(&in, nil, &extra, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	if err != nil {
		return nil, fmt.Errorf("secret: protect: %w", err)
	}
	return copyOut(&out), nil
}

// Unprotect decrypts a blob written by Protect. It fails if the blob came from
// another user account or another machine, which is the point.
func Unprotect(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, fmt.Errorf("secret: nothing to unprotect")
	}

	in := blob(ciphertext)
	extra := blob(entropy)
	var out windows.DataBlob

	err := windows.CryptUnprotectData(&in, nil, &extra, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	if err != nil {
		return nil, fmt.Errorf("secret: unprotect: %w", err)
	}
	return copyOut(&out), nil
}

func blob(b []byte) windows.DataBlob {
	return windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

// copyOut copies the result out of the buffer DPAPI allocated and frees it.
func copyOut(out *windows.DataBlob) []byte {
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	result := make([]byte, out.Size)
	copy(result, unsafe.Slice(out.Data, out.Size))
	return result
}
