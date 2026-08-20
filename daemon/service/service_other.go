//go:build !windows

// Package service runs the daemon as a Windows service and manages its
// registration.
//
// Windows is the only platform the app ships on. This build exists so the
// daemon compiles and its other packages can be tested elsewhere, and every
// call fails.
package service

import (
	"context"
	"errors"
	"log/slog"
)

// Name is how the service is known to the Service Control Manager.
const Name = "192168"

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

// ErrUnsupported means this build has no services at all.
var ErrUnsupported = errors.New("service: Windows services are only supported on Windows")

func Install() error                                      { return ErrUnsupported }
func Uninstall(removeAdapter func() error) error          { return ErrUnsupported }
func Start() error                                        { return ErrUnsupported }
func Stop() error                                         { return ErrUnsupported }
func Status() (State, error)                              { return StateAbsent, ErrUnsupported }
func Run(func(context.Context) error, *slog.Logger) error { return ErrUnsupported }

// IsService is never true off Windows, so the daemon runs in the foreground.
func IsService() bool { return false }
