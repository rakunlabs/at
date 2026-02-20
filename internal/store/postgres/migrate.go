package postgres

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/muz"
)

//go:embed migrations/*
var migrationFS embed.FS

func MigrateDB(ctx context.Context, cfg *config.Migrate, db *sql.DB) error {
	if db == nil {
		return errors.New("migrate database connection is nil")
	}

	table := cfg.DBTable
	if table == "" {
		table = "migrations"
	}

	m := muz.Migrate{
		Path:      "migrations",
		FS:        migrationFS,
		Extension: ".sql",
		Values:    cfg.Values,
	}

	driver := muz.NewPostgresDriver(db, table, slog.Default())

	if err := m.Migrate(ctx, driver); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	return nil
}
