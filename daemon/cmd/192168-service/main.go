// Command 192168-service is the networking daemon.
//
// It owns everything below the UI: device identity, the coordination server
// connection, the virtual adapter, NAT traversal, and the encrypted peer
// sessions that carry game traffic. The Windows client talks to it over a named
// pipe and renders the state it reports.
//
// The daemon runs as its own process so that peer sessions survive the UI being
// closed. On an installed machine that process is a Windows service, because
// creating the adapter needs rights the app does not have; run from a console
// it behaves exactly the same way, which is what makes it developable.
//
// Usage:
//
//	192168-service                    run in the foreground
//	192168-service service install    register the service (needs admin)
//	192168-service service uninstall  stop it, remove the adapter, deregister
//	192168-service service start      start it
//	192168-service service stop       stop it
//	192168-service service status     print absent, stopped, running, or pending
package main

//go:generate go tool goversioninfo -o resource.syso

import (
	"context"
	"fmt"
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
	"github.com/OMouta/192168/daemon/service"
	"github.com/OMouta/192168/daemon/tun"
	"github.com/OMouta/192168/protocol"
	"github.com/OMouta/192168/protocol/ipc"
)

func main() {
	if len(os.Args) > 1 {
		os.Exit(serviceCommand(os.Args[1:]))
	}

	// Started by the SCM decides the data directory, the pipe ACL, and where
	// the log goes.
	asService := service.IsService()

	log, file, err := openLogger(asService)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if file != nil {
		defer file.Close()
	}

	if asService {
		if err := service.Run(func(ctx context.Context) error { return run(ctx, asService, log, file) }, log); err != nil {
			log.Error("the service stopped", "error", err)
			os.Exit(1)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, asService, log, file); err != nil {
		log.Error("stopped", "error", err)
		os.Exit(1)
	}
}

// run is the daemon itself. It returns when ctx is cancelled, which is a signal
// in the foreground and a stop request as a service, and not before the adapter
// is down.
func run(ctx context.Context, asService bool, log *slog.Logger, mainLog *config.RollingLog) error {
	cfg, err := config.Load(asService)
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	id, err := identity.Load(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("cannot load the device identity: %w", err)
	}

	brain, err := core.New(ctx, id, cfg.DataDir, cfg.ServerURL, log)
	if err != nil {
		return fmt.Errorf("cannot start: %w", err)
	}
	// Closing the core takes the adapter down.
	defer brain.Close()
	brain.SetLogs(mainLog)

	// The core and the IPC server need each other, so one is built first and
	// told about the other.
	server := ipcserver.New(brain, log)
	brain.SetEvents(server)

	listener, err := ipcserver.Listen(asService)
	if err != nil {
		return fmt.Errorf("cannot open the control channel: %w", err)
	}
	defer listener.Close()

	log.Info("started",
		"deviceId", id.DeviceID,
		"pipe", ipc.PipeName,
		"dataDir", cfg.DataDir,
		"service", asService)

	if err := server.Serve(ctx, listener); err != nil {
		return fmt.Errorf("the control channel failed: %w", err)
	}

	log.Info("shutting down")
	return nil
}

// serviceCommand handles the service subcommands and returns the exit code.
func serviceCommand(args []string) int {
	if args[0] != "service" || len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: 192168-service service install|uninstall|start|stop|status")
		return 2
	}

	var err error
	switch args[1] {
	case "install":
		if err = service.Install(); err == nil {
			fmt.Printf("Installed %s. It is set to start on demand and will not start on its own.\n", service.Name)
		}
	case "uninstall":
		// The adapter outlives the service, so uninstall removes it too.
		err = service.Uninstall(func() error {
			return tun.Remove(protocol.Name, slog.New(slog.NewTextHandler(os.Stdout, nil)))
		})
		if err == nil {
			fmt.Printf("Removed %s and its network adapter.\n", service.Name)
		}
	case "start":
		if err = service.Start(); err == nil {
			fmt.Printf("%s is running.\n", service.Name)
		}
	case "stop":
		if err = service.Stop(); err == nil {
			fmt.Printf("%s is stopped.\n", service.Name)
		}
	case "status":
		var state service.State
		if state, err = service.Status(); err == nil {
			fmt.Println(state)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: 192168-service service install|uninstall|start|stop|status")
		return 2
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// openLogger builds the daemon logger and returns the file behind it, which is
// nil in the foreground. A service has no console, so its log is a file, and
// that file is also the one the app can ask to have emptied.
func openLogger(asService bool) (*slog.Logger, *config.RollingLog, error) {
	options := &slog.HandlerOptions{Level: config.LogLevel()}

	if !asService {
		log := slog.New(slog.NewJSONHandler(os.Stdout, options))
		captureStandardLog(log)
		return log, nil, nil
	}

	dataDir, err := config.DataDir(asService)
	if err != nil {
		return nil, nil, err
	}
	file, err := config.OpenLog(dataDir)
	if err != nil {
		return nil, nil, err
	}

	log := slog.New(slog.NewJSONHandler(file, options))
	captureStandardLog(log)
	return log, file, nil
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
