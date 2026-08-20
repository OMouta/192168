//go:build windows

// Package service runs the daemon as a Windows service and manages its
// registration.
//
// Creating the virtual adapter and its routes needs administrator rights.
// Running the daemon as a service is what avoids a UAC prompt on every launch.
//
// Start type is manual and the DACL below lets any interactive user stop it.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	// Name is how the service is known to the Service Control Manager.
	Name = "192168"

	// DisplayName is what the Services list and Task Manager show.
	DisplayName = "192168 Virtual LAN"

	// Description is what Windows shows beside it in the Services list.
	Description = "Creates the virtual network adapter 192168 uses to put you on a LAN with your friends. " +
		"Starts on demand and can be stopped from the app."
)

// accessControl is the service's DACL.
//
// SYSTEM and Administrators get full control. Interactive users get query,
// start and stop, so turning it off needs no UAC prompt. They cannot
// reconfigure it, delete it, or rewrite this.
//
//	CC  query config     LC  query status     SW  enumerate dependents
//	RP  start            WP  stop             LO  interrogate
//	RC  read the DACL
const accessControl = "D:" +
	"(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;SY)" +
	"(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;BA)" +
	"(A;;CCLCSWRPWPLORC;;;IU)"

// State is what the service is doing, in the terms the app cares about.
type State string

const (
	// StateAbsent means the service is not registered at all.
	StateAbsent State = "absent"
	// StateStopped means it is registered and not running.
	StateStopped State = "stopped"
	// StateRunning means it is up and serving the pipe.
	StateRunning State = "running"
	// StatePending means Windows is starting or stopping it.
	StatePending State = "pending"
)

// Install registers the service and points it at this executable. Needs
// administrator rights.
func Install() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("service: locate this executable: %w", err)
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("service: open the service manager, which needs administrator rights: %w", err)
	}
	defer m.Disconnect()

	if existing, err := m.OpenService(Name); err == nil {
		existing.Close()
		return fmt.Errorf("service: %s is already installed", Name)
	}

	s, err := m.CreateService(Name, exe, mgr.Config{
		DisplayName: DisplayName,
		Description: Description,
		// Manual, so Windows never starts it on its own. The app starts it
		// when there is something to do.
		StartType: mgr.StartManual,
		// The adapter and its routes are machine-wide, so there is no lesser
		// account this could run under.
		ServiceStartName: "LocalSystem",
	})
	if err != nil {
		return fmt.Errorf("service: create %s: %w", Name, err)
	}
	defer s.Close()

	if err := applyAccessControl(s); err != nil {
		// Roll back rather than leave a service only administrators can stop.
		_ = s.Delete()
		return err
	}
	return nil
}

// Uninstall stops the service, removes any adapter it left behind, and
// deregisters it. Needs administrator rights.
//
// removeAdapter is called while this process still has the rights to do it, and
// is separate so the caller decides what an adapter is called.
func Uninstall(removeAdapter func() error) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("service: open the service manager, which needs administrator rights: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(Name)
	if err != nil {
		// Not registered, but an adapter may still be lying around.
		return removeAdapter()
	}
	defer s.Close()

	if err := stop(s); err != nil {
		return err
	}
	if err := removeAdapter(); err != nil {
		return err
	}
	if err := s.Delete(); err != nil {
		return fmt.Errorf("service: deregister %s: %w", Name, err)
	}
	return nil
}

// Start starts the service. Anyone logged in at the machine may do this, by the
// permissions Install applied.
func Start() error {
	m, s, err := open(windows.SERVICE_START | windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()

	status, err := s.Query()
	if err == nil && status.State == svc.Running {
		return nil
	}
	if err := s.Start(); err != nil {
		return fmt.Errorf("service: start %s: %w", Name, err)
	}
	return await(s, svc.Running)
}

// Stop stops the service, which takes the adapter down with it.
func Stop() error {
	m, s, err := open(windows.SERVICE_STOP | windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()

	return stop(s)
}

// Status reports what the service is doing.
func Status() (State, error) {
	m, s, err := open(windows.SERVICE_QUERY_STATUS)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return StateAbsent, nil
	}
	if err != nil {
		return "", err
	}
	defer m.Disconnect()
	defer s.Close()

	status, err := s.Query()
	if err != nil {
		return "", fmt.Errorf("service: query %s: %w", Name, err)
	}
	switch status.State {
	case svc.Running:
		return StateRunning, nil
	case svc.Stopped:
		return StateStopped, nil
	default:
		return StatePending, nil
	}
}

// open connects to the service manager and opens the service asking for only
// the access the caller needs.
//
// mgr.Connect and Mgr.OpenService ask for full control, which Windows refuses
// to non-administrators whatever the service DACL says.
func open(access uint32) (*mgr.Mgr, *mgr.Service, error) {
	handle, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return nil, nil, fmt.Errorf("service: open the service manager: %w", err)
	}
	m := &mgr.Mgr{Handle: handle}

	name, err := windows.UTF16PtrFromString(Name)
	if err != nil {
		m.Disconnect()
		return nil, nil, fmt.Errorf("service: %w", err)
	}

	service, err := windows.OpenService(m.Handle, name, access)
	if err != nil {
		m.Disconnect()
		return nil, nil, fmt.Errorf("service: open %s: %w", Name, err)
	}
	return m, &mgr.Service{Name: Name, Handle: service}, nil
}

// IsService reports whether this process was started by the Service Control
// Manager rather than from a console.
func IsService() bool {
	is, err := svc.IsWindowsService()
	return err == nil && is
}

// Run hands the process to the Service Control Manager and calls work, which
// should return when its context is cancelled. Windows is told the stop is
// pending until the adapter is down.
func Run(work func(context.Context) error, log *slog.Logger) error {
	return svc.Run(Name, &handler{work: work, log: log})
}

type handler struct {
	work func(context.Context) error
	log  *slog.Logger
}

func (h *handler) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown

	status <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	failed := make(chan error, 1)
	go func() { failed <- h.work(ctx) }()

	status <- svc.Status{State: svc.Running, Accepts: accepted}

	for {
		select {
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				// Wait for the adapter to come down before reporting stopped.
				if err := <-failed; err != nil {
					h.log.Error("stopped with an error", "error", err)
					return false, 1
				}
				return false, 0
			default:
				h.log.Info("ignoring an unexpected service command", "cmd", request.Cmd)
			}
		case err := <-failed:
			// The daemon stopped on its own.
			status <- svc.Status{State: svc.StopPending}
			if err != nil {
				h.log.Error("the daemon stopped", "error", err)
				return false, 1
			}
			return false, 0
		}
	}
}

// stop asks the service to stop and waits for it.
func stop(s *mgr.Service) error {
	status, err := s.Query()
	if err != nil {
		return fmt.Errorf("service: query %s: %w", Name, err)
	}
	if status.State == svc.Stopped {
		return nil
	}
	if _, err := s.Control(svc.Stop); err != nil {
		return fmt.Errorf("service: stop %s: %w", Name, err)
	}
	return await(s, svc.Stopped)
}

// await waits for the service to reach a state. Start and Control return once
// Windows accepts the request, not once it has acted on it.
func await(s *mgr.Service, want svc.State) error {
	// Adapter teardown is the slow part, measured in seconds.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		status, err := s.Query()
		if err != nil {
			return fmt.Errorf("service: query %s: %w", Name, err)
		}
		if status.State == want {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("service: %s did not reach %v in time", Name, want)
}

// applyAccessControl replaces the default DACL with one an ordinary account can
// start and stop the service through.
func applyAccessControl(s *mgr.Service) error {
	descriptor, err := windows.SecurityDescriptorFromString(accessControl)
	if err != nil {
		return fmt.Errorf("service: parse the access control string: %w", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("service: read the access control list: %w", err)
	}

	// PROTECTED_DACL stops inheritance widening it back out.
	err = windows.SetSecurityInfo(
		windows.Handle(s.Handle),
		windows.SE_SERVICE,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	)
	if err != nil {
		return fmt.Errorf("service: apply the access control list: %w", err)
	}
	return nil
}
