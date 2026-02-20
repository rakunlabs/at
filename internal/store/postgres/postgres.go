package postgres

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

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/postgres"
)

type Postgres struct {
	db   *sql.DB
	goqu *goqu.Database

	tableProviders string
}

func New(ctx context.Context, cfg *config.StorePostgres) (*Postgres, error) {
	if cfg == nil {
		return nil, errors.New("postgres configuration is nil")
	}

	if cfg.DBDatasource == "" {
		return nil, errors.New("postgres db_datasource is required")
	}

	db, err := sql.Open("pgx", cfg.DBDatasource)
	if err != nil {
		return nil, fmt.Errorf("open postgres connection: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		db.Close()

		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	// Set schema search path if configured.
	if cfg.DBSchema != "" {
		if _, err := db.ExecContext(ctx, fmt.Sprintf("SET search_path TO %s", cfg.DBSchema)); err != nil {
			db.Close()

			return nil, fmt.Errorf("set search_path: %w", err)
		}
	}

	// Run migrations.
	migrate := cfg.Migrate
	if migrate.DBTable == "" {
		migrate.DBTable = "migrations"
	}

	migrate.DBTable = cfg.TablePrefix + migrate.DBTable

	if err := MigrateDB(ctx, &migrate, db); err != nil {
		db.Close()

		return nil, fmt.Errorf("migrate store postgres: %w", err)
	}

	slog.Info("connected to store postgres")

	dbGoqu := goqu.New("postgres", db)

	return &Postgres{
		db:             db,
		goqu:           dbGoqu,
		tableProviders: cfg.TablePrefix + "providers",
	}, nil
}

func (p *Postgres) Close() {
	if p.db != nil {
		if err := p.db.Close(); err != nil {
			slog.Error("close store postgres connection", "error", err)
		}
	}
}

// ─── Provider CRUD ───

type providerRow struct {
	ID        string          `db:"id" goqu:"skipupdate"`
	Key       string          `db:"key"`
	Config    json.RawMessage `db:"config"`
	CreatedAt time.Time       `db:"created_at" goqu:"skipupdate"`
	UpdatedAt time.Time       `db:"updated_at"`
}

func (p *Postgres) ListProviders(ctx context.Context) ([]service.ProviderRecord, error) {
	query, _, err := p.goqu.From(p.tableProviders).
		Select("id", "key", "config", "created_at", "updated_at").
		Order(goqu.I("key").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
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

func (p *Postgres) GetProvider(ctx context.Context, key string) (*service.ProviderRecord, error) {
	query, _, err := p.goqu.From(p.tableProviders).
		Select("id", "key", "config", "created_at", "updated_at").
		Where(goqu.I("key").Eq(key)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get query: %w", err)
	}

	var row providerRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Key, &row.Config, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get provider %q: %w", key, err)
	}

	return rowToRecord(row)
}

func (p *Postgres) CreateProvider(ctx context.Context, key string, cfg config.LLMConfig) (*service.ProviderRecord, error) {
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := p.goqu.Insert(p.tableProviders).Rows(
		goqu.Record{
			"id":         id,
			"key":        key,
			"config":     configJSON,
			"created_at": now,
			"updated_at": now,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
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

func (p *Postgres) UpdateProvider(ctx context.Context, key string, cfg config.LLMConfig) (*service.ProviderRecord, error) {
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	now := time.Now().UTC()

	query, _, err := p.goqu.Update(p.tableProviders).Set(
		goqu.Record{
			"config":     configJSON,
			"updated_at": now,
		},
	).Where(goqu.I("key").Eq(key)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update query: %w", err)
	}

	res, err := p.db.ExecContext(ctx, query)
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

	return p.GetProvider(ctx, key)
}

func (p *Postgres) DeleteProvider(ctx context.Context, key string) error {
	query, _, err := p.goqu.Delete(p.tableProviders).
		Where(goqu.I("key").Eq(key)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete provider %q: %w", key, err)
	}

	return nil
}

// ─── Helpers ───

func rowToRecord(row providerRow) (*service.ProviderRecord, error) {
	var cfg config.LLMConfig
	if err := json.Unmarshal(row.Config, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal provider config for %q: %w", row.Key, err)
	}

	return &service.ProviderRecord{
		ID:        row.ID,
		Key:       row.Key,
		Config:    cfg,
		CreatedAt: row.CreatedAt.Format(time.RFC3339),
		UpdatedAt: row.UpdatedAt.Format(time.RFC3339),
	}, nil
}
