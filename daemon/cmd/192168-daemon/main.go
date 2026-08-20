// Command 192168-daemon is the networking daemon.
//
// It owns everything below the UI: device identity, the coordination server
// connection, the virtual adapter, NAT traversal, and the encrypted peer
// sessions that carry game traffic. The Windows client talks to it over a named
// pipe and renders the state it reports.
//
// The daemon runs as its own process so that peer sessions survive the UI being
// closed.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/OMouta/192168/daemon/config"
	"github.com/OMouta/192168/daemon/core"
	"github.com/OMouta/192168/daemon/identity"
	"github.com/OMouta/192168/daemon/ipcserver"
	"github.com/OMouta/192168/protocol/ipc"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		log.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	id, err := identity.Load(cfg.DataDir)
	if err != nil {
		log.Error("cannot load the device identity", "error", err)
		os.Exit(1)
	}

	brain, err := core.New(ctx, id, cfg.DataDir, cfg.ServerURL, log)
	if err != nil {
		log.Error("cannot start", "error", err)
		os.Exit(1)
	}
	defer brain.Close()

	// The core and the IPC server need each other, so one is built first and
	// told about the other.
	server := ipcserver.New(brain, log)
	brain.SetEvents(server)

	listener, err := ipcserver.Listen()
	if err != nil {
		log.Error("cannot open the control channel", "error", err)
		os.Exit(1)
	}
	defer listener.Close()

	log.Info("started",
		"deviceId", id.DeviceID,
		"pipe", ipc.PipeName,
		"dataDir", cfg.DataDir)

	if err := server.Serve(ctx, listener); err != nil {
		log.Error("the control channel failed", "error", err)
		os.Exit(1)
	}

	log.Info("shutting down")
}
