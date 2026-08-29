// Command migrate applies (or rolls back) the database schema. It is a
// separate binary from cmd/api, per PROMPT.md's Compose layout: a one-shot
// "migrator" service runs it and exits, and the api service only starts once
// that succeeds, so the API never boots against a half-migrated schema.
package main

import (
	"avito-kitchen/internal/config"
	"avito-kitchen/internal/platform/logger"
	"avito-kitchen/migrations"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"
)

func main() {
	if err := run(); err != nil {
		slog.Error("migrate failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log := logger.New(cfg.LogLevel)

	// database/sql, not pgxpool: goose drives migrations through the standard
	// library interface. The blank pgx/v5/stdlib import registers "pgx" as an
	// sql.DB driver, so this still runs on the one Postgres driver the project
	// otherwise standardizes on (section 4 of PROMPT.md).
	sqlDB, err := sql.Open("pgx", cfg.PostgresDSN())
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer sqlDB.Close()

	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}

	ctx := context.Background()
	if err := goose.RunContext(ctx, command, sqlDB, ".", os.Args[2:]...); err != nil {
		return fmt.Errorf("run migration command %q: %w", command, err)
	}

	log.Info("migrate command finished", "command", command)
	return nil
}
