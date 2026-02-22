package sqlite3

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"

	_ "modernc.org/sqlite"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	"github.com/doug-martin/goqu/v9/exp"
)

var DefaultTablePrefix = "at_"

type SQLite struct {
	db   *sql.DB
	goqu *goqu.Database

	tableProviders exp.IdentifierExpression
	tableAPITokens exp.IdentifierExpression
}

func New(ctx context.Context, cfg *config.StoreSQLite) (*SQLite, error) {
	if cfg == nil {
		return nil, errors.New("sqlite configuration is nil")
	}

	if cfg.Datasource == "" {
		return nil, errors.New("sqlite datasource is required")
	}

	tablePrefix := DefaultTablePrefix
	if cfg.TablePrefix != nil {
		tablePrefix = *cfg.TablePrefix
	}

	// /////////////////////////////////////////////
	// Run migrations.
	migrate := cfg.Migrate
	if migrate.Table == "" {
		migrate.Table = "migrations"
	}

	if migrate.Datasource == "" {
		migrate.Datasource = cfg.Datasource
	}

	migrate.Table = tablePrefix + migrate.Table
	if migrate.Values == nil {
		migrate.Values = make(map[string]string)
	}
	migrate.Values["TABLE_PREFIX"] = tablePrefix

	if err := MigrateDB(ctx, &migrate); err != nil {
		return nil, fmt.Errorf("migrate store sqlite: %w", err)
	}
	// /////////////////////////////////////////////

	db, err := sql.Open("sqlite", cfg.Datasource)
	if err != nil {
		return nil, fmt.Errorf("open sqlite connection: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		db.Close()

		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	// Enable WAL mode for better concurrent read performance.
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		db.Close()

		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	// Enable foreign keys.
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		db.Close()

		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	// SQLite is single-writer; limit connections accordingly.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	slog.Info("connected to store sqlite")

	dbGoqu := goqu.New("sqlite3", db)

	return &SQLite{
		db:             db,
		goqu:           dbGoqu,
		tableProviders: goqu.T(tablePrefix + "providers"),
		tableAPITokens: goqu.T(tablePrefix + "tokens"),
	}, nil
}

func (s *SQLite) Close() {
	if s.db != nil {
		if err := s.db.Close(); err != nil {
			slog.Error("close store sqlite connection", "error", err)
		}
	}
}

// ─── Provider CRUD ───

type providerRow struct {
	ID        string `db:"id"`
	Key       string `db:"key"`
	Config    string `db:"config"`
	CreatedAt string `db:"created_at"`
	UpdatedAt string `db:"updated_at"`
}

func (s *SQLite) ListProviders(ctx context.Context) ([]service.ProviderRecord, error) {
	query, _, err := s.goqu.From(s.tableProviders).
		Select("id", "key", "config", "created_at", "updated_at").
		Order(goqu.I("key").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()

	var result []service.ProviderRecord
	for rows.Next() {
		var row providerRow
		if err := rows.Scan(&row.ID, &row.Key, &row.Config, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan provider row: %w", err)
		}

		rec, err := rowToRecord(row)
		if err != nil {
			return nil, err
		}
		result = append(result, *rec)
	}

	return result, rows.Err()
}

func (s *SQLite) GetProvider(ctx context.Context, key string) (*service.ProviderRecord, error) {
	query, _, err := s.goqu.From(s.tableProviders).
		Select("id", "key", "config", "created_at", "updated_at").
		Where(goqu.I("key").Eq(key)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get query: %w", err)
	}

	var row providerRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Key, &row.Config, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get provider %q: %w", key, err)
	}

	return rowToRecord(row)
}

func (s *SQLite) CreateProvider(ctx context.Context, key string, cfg config.LLMConfig) (*service.ProviderRecord, error) {
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := s.goqu.Insert(s.tableProviders).Rows(
		goqu.Record{
			"id":         id,
			"key":        key,
			"config":     string(configJSON),
			"created_at": now.Format(time.RFC3339),
			"updated_at": now.Format(time.RFC3339),
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create provider %q: %w", key, err)
	}

	return &service.ProviderRecord{
		ID:        id,
		Key:       key,
		Config:    cfg,
		CreatedAt: now.Format(time.RFC3339),
		UpdatedAt: now.Format(time.RFC3339),
	}, nil
}

func (s *SQLite) UpdateProvider(ctx context.Context, key string, cfg config.LLMConfig) (*service.ProviderRecord, error) {
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	now := time.Now().UTC()

	query, _, err := s.goqu.Update(s.tableProviders).Set(
		goqu.Record{
			"config":     string(configJSON),
			"updated_at": now.Format(time.RFC3339),
		},
	).Where(goqu.I("key").Eq(key)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update provider %q: %w", key, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return s.GetProvider(ctx, key)
}

func (s *SQLite) DeleteProvider(ctx context.Context, key string) error {
	query, _, err := s.goqu.Delete(s.tableProviders).
		Where(goqu.I("key").Eq(key)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete provider %q: %w", key, err)
	}

	return nil
}

// ─── Helpers ───

func rowToRecord(row providerRow) (*service.ProviderRecord, error) {
	var cfg config.LLMConfig
	if err := json.Unmarshal([]byte(row.Config), &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal provider config for %q: %w", row.Key, err)
	}

	return &service.ProviderRecord{
		ID:        row.ID,
		Key:       row.Key,
		Config:    cfg,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}, nil
}
