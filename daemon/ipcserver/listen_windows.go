//go:build windows

package ipcserver

import (
	"fmt"
	"net"
	"os/user"

	"github.com/Microsoft/go-winio"

	"github.com/OMouta/192168/protocol/ipc"
)

// Listen opens the named pipe the client connects to.
//
// Group passwords cross this pipe in the clear, because the daemon owns all
// cryptography and the UI never implements a KDF. So the pipe is restricted to
// the account that owns the daemon, and nothing else on the machine can open
// it. If the daemon ever becomes a service running as SYSTEM, this has to grant
// the interactive user instead of whoever the daemon is.
func Listen() (net.Listener, error) {
	sddl, err := currentUserOnly()
	if err != nil {
		return nil, err
	}

	listener, err := winio.ListenPipe(ipc.PipeName, &winio.PipeConfig{
		SecurityDescriptor: sddl,
		MessageMode:        false,
	})
	if err != nil {
		return nil, fmt.Errorf("ipcserver: listen on %s: %w", ipc.PipeName, err)
	}
	return listener, nil
}

// currentUserOnly builds a security descriptor granting full access to this
// account and to SYSTEM, and to nobody else. The P in D:P blocks inherited
// entries, which is what stops a permissive parent from widening this.
func currentUserOnly() (string, error) {
	me, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("ipcserver: current user: %w", err)
	}
	// On Windows Uid is the account's SID in string form.
	return fmt.Sprintf("D:P(A;;GA;;;SY)(A;;GA;;;%s)", me.Uid), nil
}
