//go:build windows

package config

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// logDirAccess lets the signed-in user write in the log directory.
//
// The service creates it as LocalSystem, and the app writes its own log beside
// the daemon's as an ordinary user. Without this the two end up in different
// places and nobody can find both.
//
// 0x1301bf is modify: read, write, delete, and list. OICI makes it apply to the
// files inside. The P blocks inheritance from ProgramData widening it.
const logDirAccess = "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1301bf;;;BU)"

// shareLogDir opens the log directory to local users.
//
// Only the owner can do this, which as a service means LocalSystem. Run in the
// foreground the call fails and the daemon logs to a console anyway, so the
// error is worth returning and not worth acting on.
func shareLogDir(dir string) error {
	descriptor, err := windows.SecurityDescriptorFromString(logDirAccess)
	if err != nil {
		return fmt.Errorf("parse the log directory access: %w", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("read the log directory access: %w", err)
	}

	err = windows.SetNamedSecurityInfo(
		dir,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	)
	if err != nil {
		return fmt.Errorf("apply the log directory access: %w", err)
	}
	return nil
}
