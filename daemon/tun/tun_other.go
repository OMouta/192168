//go:build !windows

// Package tun is the virtual network adapter.
//
// Windows is the only platform the app ships on. This build exists so the
// daemon compiles and its other packages can be tested elsewhere, and every
// call fails.
package tun

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
)

// ErrNeedsAdmin means the adapter could not be created for lack of rights.
var ErrNeedsAdmin = errors.New("tun: creating the network adapter needs administrator rights")

// ErrMissingDriver means the adapter driver is not installed.
var ErrMissingDriver = errors.New("tun: the adapter driver is missing")

// ErrClosed means the adapter is going away, or already has.
var ErrClosed = errors.New("tun: adapter closed")

// ErrUnsupported means this build has no adapter at all.
var ErrUnsupported = errors.New("tun: virtual adapters are only supported on Windows")

// Device is an open adapter. Never one of these here.
type Device struct{}

// Open always fails.
func Open(name string, address netip.Prefix, mtu int, log *slog.Logger) (*Device, error) {
	return nil, ErrUnsupported
}

// Remove has nothing to remove, so it succeeds.
func Remove(name string, log *slog.Logger) error { return nil }

func (d *Device) Read(ctx context.Context) ([]byte, error) { return nil, ErrUnsupported }

func (d *Device) Write(packet []byte) error { return ErrUnsupported }

func (d *Device) Close() error { return nil }
