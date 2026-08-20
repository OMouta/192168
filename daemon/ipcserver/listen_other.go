//go:build !windows

package ipcserver

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// Listen opens a Unix socket. Windows is the only platform the app ships on, so
// this exists to keep the daemon buildable and testable elsewhere.
//
// The socket sits in a directory only this user can enter, which is the closest
// equivalent to the pipe's access control. There are no services here, so
// asService changes nothing.
func Listen(asService bool) (net.Listener, error) {
	dir, err := os.MkdirTemp("", "192168-")
	if err != nil {
		return nil, fmt.Errorf("ipcserver: create socket directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("ipcserver: restrict socket directory: %w", err)
	}

	listener, err := net.Listen("unix", filepath.Join(dir, "control.sock"))
	if err != nil {
		return nil, fmt.Errorf("ipcserver: listen: %w", err)
	}
	return listener, nil
}
