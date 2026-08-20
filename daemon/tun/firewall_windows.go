//go:build windows

package tun

import (
	"fmt"
	"log/slog"
	"os/exec"
	"syscall"
)

// firewallRule is the name every rule this creates is filed under, so they can
// all be removed again without remembering which ones were added.
const firewallRule = "192168 virtual network"

// AllowSubnet lets the machine answer traffic from the virtual network.
//
// Windows blocks unsolicited inbound traffic, and a fresh adapter is a network
// it has never seen, so by default nothing on the other side can reach this
// machine at all: pings go unanswered, and a game hosted here cannot be joined.
// One person can reach the other and not the reverse, which reads as the tunnel
// being broken when it is working perfectly.
//
// The rule is scoped to the group's own subnet, so it opens this machine to the
// people in the group and to nobody else. It is not a hole in the firewall for
// the internet: nothing outside the tunnel has one of these addresses.
func AllowSubnet(subnet string, log *slog.Logger) error {
	// Removing first keeps this from stacking up a rule per connect.
	_ = removeFirewallRules()

	for _, direction := range []string{"in", "out"} {
		args := []string{
			"advfirewall", "firewall", "add", "rule",
			"name=" + firewallRule,
			"dir=" + direction,
			"action=allow",
			"remoteip=" + subnet,
			"profile=any",
		}
		if err := runNetsh(args); err != nil {
			return fmt.Errorf("tun: allow %s traffic on %s: %w", direction, subnet, err)
		}
	}

	log.Info("firewall opened to the virtual network", "subnet", subnet)
	return nil
}

// BlockSubnet takes the rules back out. Leaving them behind would mean an
// uninstalled app that still has firewall rules.
func BlockSubnet(log *slog.Logger) {
	if err := removeFirewallRules(); err != nil {
		// Worth saying and not worth failing a disconnect over.
		log.Warn("cannot remove the firewall rules", "error", err)
		return
	}
	log.Info("firewall rules removed")
}

func removeFirewallRules() error {
	return runNetsh([]string{
		"advfirewall", "firewall", "delete", "rule", "name=" + firewallRule,
	})
}

// runNetsh runs netsh without letting a console window appear. The daemon is a
// service and has no console, but it is also run in the foreground during
// development, where a window flashing up on every connect is noise.
func runNetsh(args []string) error {
	cmd := exec.Command("netsh", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("netsh %v: %w: %s", args, err, output)
	}
	return nil
}
