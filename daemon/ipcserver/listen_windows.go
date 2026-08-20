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
// cryptography and the UI never implements a KDF, so who may open it matters.
//
// In the foreground the daemon runs as the person using it and the pipe is
// theirs alone. As a service it runs as SYSTEM, which matches no user account,
// so the pipe names who may use it instead.
func Listen(asService bool) (net.Listener, error) {
	sddl, err := descriptor(asService)
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

// descriptor builds the pipe's security descriptor. The P in D:P blocks
// inherited entries, which is what stops a permissive parent from widening it.
//
// The service variant grants INTERACTIVE: accounts signed in at this machine.
// On a shared PC another person at the console could drive the tunnel and read
// the group passwords crossing it. Narrowing that further needs the pipe to
// authenticate callers, which it does not do.
func descriptor(asService bool) (string, error) {
	if asService {
		return "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;IU)", nil
	}

	me, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("ipcserver: current user: %w", err)
	}
	// On Windows Uid is the account's SID in string form.
	return fmt.Sprintf("D:P(A;;GA;;;SY)(A;;GA;;;%s)", me.Uid), nil
}
