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
	stdlog "log"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/OMouta/192168/daemon/config"
	"github.com/OMouta/192168/daemon/core"
	"github.com/OMouta/192168/daemon/identity"
	"github.com/OMouta/192168/daemon/ipcserver"
	"github.com/OMouta/192168/protocol/ipc"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: config.LogLevel()}))
	captureStandardLog(log)

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

// captureStandardLog sends anything logged through the standard library into
// the daemon's handler.
//
// The Wintun driver logs that way, so without this the daemon's output is JSON
// with plain sentences scattered through it, carrying their own timestamps in
// their own format. A log that is half one shape and half another cannot be
// read by anything, which matters because the adapter's messages are exactly
// the ones worth reading when a tunnel will not come up.
func captureStandardLog(log *slog.Logger) {
	// The standard logger's own date and time would be a second timestamp
	// inside a record that already has one.
	stdlog.SetFlags(0)
	stdlog.SetPrefix("")
	stdlog.SetOutput(slogWriter{log: log})
}

// slogWriter turns each line written to the standard logger into one record.
//
// The severity is lost before it reaches here: Wintun knows whether a message
// was informational or an error, but it hands everything to log.Println, so
// info is the only honest level to use.
type slogWriter struct{ log *slog.Logger }

func (w slogWriter) Write(line []byte) (int, error) {
	w.log.Info(strings.TrimRight(string(line), "\n"), "source", "driver")
	return len(line), nil
}
