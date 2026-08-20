// Command 192168-server is the coordination server: it introduces peers to
// each other and never carries game traffic.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/OMouta/192168/server/api"
	"github.com/OMouta/192168/server/config"
	"github.com/OMouta/192168/server/storage"
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

	store, err := storage.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("cannot open the database", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	handler := api.New(cfg, store, log)
	go handler.ExpireSessions(ctx, api.ExpiryFrequency, api.SessionTimeout)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// A realtime connection is held open for the life of a group session,
		// so there is no write deadline to set here.
	}

	go func() {
		log.Info("listening", "addr", cfg.Addr, "publicUrl", cfg.PublicURL)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server failed", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown failed", "error", err)
		os.Exit(1)
	}
}
