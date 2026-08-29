// Command api runs the Avito.Kitchen HTTP API: the public-facing service
// plus, once later stages add it, the background outbox worker.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	adapterhttp "avito-kitchen/internal/adapter/http"
	"avito-kitchen/internal/config"
	"avito-kitchen/internal/platform/httpserver"
	"avito-kitchen/internal/platform/logger"
	"avito-kitchen/internal/platform/pgpool"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal startup error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := logger.New(cfg.LogLevel)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgpool.New(ctx, cfg.PostgresDSN())
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer pool.Close()

	router, dispatcher := adapterhttp.NewRouter(log, pool)
	srv := httpserver.New(fmt.Sprintf(":%d", cfg.HTTPPort), router, log)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		dispatcher.Run(ctx, cfg.OutboxPollInterval)
	}()

	log.Info("service starting", "port", cfg.HTTPPort)
	if err := srv.Run(ctx, cfg.ShutdownTimeout); err != nil {
		return fmt.Errorf("run http server: %w", err)
	}
	wg.Wait()

	log.Info("service stopped gracefully")
	return nil
}
