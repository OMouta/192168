//go:build !windows

package tun

import "log/slog"

// AllowSubnet has no firewall to open. Windows is the only platform that ships.
func AllowSubnet(subnet string, log *slog.Logger) error { return nil }

// BlockSubnet has nothing to take back out.
func BlockSubnet(log *slog.Logger) {}
