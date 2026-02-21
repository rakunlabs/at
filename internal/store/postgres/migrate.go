package postgres

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/muz"
)

//go:embed migrations/*
var migrationFS embed.FS

func MigrateDB(ctx context.Context, cfg *config.Migrate) error {
	if cfg.Datasource == "" {
		return fmt.Errorf("migrate datasource is required")
	}

	db, err := sql.Open("pgx", cfg.Datasource)
	if err != nil {
		return fmt.Errorf("open postgres connection: %w", err)
	}

	m := muz.Migrate{
		Path:      "migrations",
		FS:        migrationFS,
		Extension: ".sql",
		Values:    cfg.Values,
	}

	driver := muz.NewPostgresDriver(db, cfg.Table, slog.Default())

	if err := m.Migrate(ctx, driver); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	return nil
}
