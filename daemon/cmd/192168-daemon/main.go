// Command 192168-daemon is the networking daemon.
//
// It owns everything below the UI: device identity, membership credentials,
// the coordination-server connection, the virtual adapter, NAT traversal, and
// the encrypted peer sessions that carry game traffic. The Windows client
// talks to it over a named pipe and renders the state it reports.
//
// The daemon runs as its own process so that peer sessions survive the UI
// being closed.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/OMouta/192168/daemon/config"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		log.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	log.Info("starting", "serverUrl", cfg.ServerURL, "serverConfigured", cfg.ServerURL != "", "dataDir", cfg.DataDir)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// TODO: load or create the device identity, serve the IPC pipe, and drive
	// group sessions. See docs/architecture.md for the intended layout.

	<-ctx.Done()
	log.Info("shutting down")
}
