package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/config"
	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/postgres"
	"github.com/doug-martin/goqu/v9/exp"
)

var (
	ConnMaxLifetime = 15 * time.Minute
	MaxIdleConns    = 3
	MaxOpenConns    = 3

	DefaultTablePrefix = "at_"
)

type Postgres struct {
	db   *sql.DB
	goqu *goqu.Database

	tableProviders        exp.IdentifierExpression
	tableAPITokens        exp.IdentifierExpression
	tableWorkflows        exp.IdentifierExpression
	tableWorkflowVersions exp.IdentifierExpression
	tableTriggers         exp.IdentifierExpression
	tableSkills           exp.IdentifierExpression
	tableVariables        exp.IdentifierExpression
	tableNodeConfigs      exp.IdentifierExpression

	// encKey is the AES-256 key used to encrypt/decrypt sensitive provider
	// fields. nil means encryption is disabled. Protected by encKeyMu.
	encKey   []byte
	encKeyMu sync.RWMutex
}

func New(ctx context.Context, cfg *config.StorePostgres, encKey []byte) (*Postgres, error) {
	if cfg == nil {
		return nil, errors.New("postgres configuration is nil")
	}

	if cfg.Datasource == "" {
		return nil, errors.New("postgres datasource is required")
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

	if migrate.Schema == "" {
		migrate.Schema = cfg.Schema
	}

	migrate.Table = tablePrefix + migrate.Table
	if migrate.Values == nil {
		migrate.Values = make(map[string]string)
	}
	migrate.Values["TABLE_PREFIX"] = tablePrefix

	if err := MigrateDB(ctx, &migrate); err != nil {
		return nil, fmt.Errorf("migrate store postgres: %w", err)
	}
	// /////////////////////////////////////////////

	db, err := sql.Open("pgx", cfg.Datasource)
	if err != nil {
		return nil, fmt.Errorf("open postgres connection: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		db.Close()

		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	// Set schema search path if configured.
	if cfg.Schema != "" {
		if _, err := db.ExecContext(ctx, fmt.Sprintf("SET search_path TO %s", cfg.Schema)); err != nil {
			db.Close()

			return nil, fmt.Errorf("set search_path: %w", err)
		}
	}

	if cfg.ConnMaxLifetime != nil {
		ConnMaxLifetime = *cfg.ConnMaxLifetime
	}
	if cfg.MaxIdleConns != nil {
		MaxIdleConns = *cfg.MaxIdleConns
	}
	if cfg.MaxOpenConns != nil {
		MaxOpenConns = *cfg.MaxOpenConns
	}

	db.SetConnMaxLifetime(ConnMaxLifetime)
	db.SetMaxIdleConns(MaxIdleConns)
	db.SetMaxOpenConns(MaxOpenConns)

	slog.Info("connected to store postgres")

	dbGoqu := goqu.New("postgres", db)

	return &Postgres{
		db:                    db,
		goqu:                  dbGoqu,
		tableProviders:        goqu.T(tablePrefix + "providers"),
		tableAPITokens:        goqu.T(tablePrefix + "tokens"),
		tableWorkflows:        goqu.T(tablePrefix + "workflows"),
		tableWorkflowVersions: goqu.T(tablePrefix + "workflow_versions"),
		tableTriggers:         goqu.T(tablePrefix + "triggers"),
		tableSkills:           goqu.T(tablePrefix + "skills"),
		tableVariables:        goqu.T(tablePrefix + "variables"),
		tableNodeConfigs:      goqu.T(tablePrefix + "node_configs"),
		encKey:                encKey,
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
	CreatedBy string          `db:"created_by" goqu:"skipupdate"`
	UpdatedBy string          `db:"updated_by"`
}

func (p *Postgres) ListProviders(ctx context.Context) ([]service.ProviderRecord, error) {
	query, _, err := p.goqu.From(p.tableProviders).
		Select("id", "key", "config", "created_at", "updated_at", "created_by", "updated_by").
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

	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

	var result []service.ProviderRecord
	for rows.Next() {
		var row providerRow
		if err := rows.Scan(&row.ID, &row.Key, &row.Config, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy); err != nil {
			return nil, fmt.Errorf("scan provider row: %w", err)
		}

		rec, err := rowToRecord(row, encKey)
		if err != nil {
			return nil, err
		}
		result = append(result, *rec)
	}

	return result, rows.Err()
}

func (p *Postgres) GetProvider(ctx context.Context, key string) (*service.ProviderRecord, error) {
	query, _, err := p.goqu.From(p.tableProviders).
		Select("id", "key", "config", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("key").Eq(key)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get query: %w", err)
	}

	var row providerRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Key, &row.Config, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get provider %q: %w", key, err)
	}

	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

	return rowToRecord(row, encKey)
}

func (p *Postgres) CreateProvider(ctx context.Context, record service.ProviderRecord) (*service.ProviderRecord, error) {
	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

	storeCfg, err := atcrypto.EncryptLLMConfig(record.Config, encKey)
	if err != nil {
		return nil, fmt.Errorf("encrypt config: %w", err)
	}

	configJSON, err := json.Marshal(storeCfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	key := record.Key
	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := p.goqu.Insert(p.tableProviders).Rows(
		goqu.Record{
			"id":         id,
			"key":        key,
			"config":     configJSON,
			"created_at": now,
			"updated_at": now,
			"created_by": record.CreatedBy,
			"updated_by": record.UpdatedBy,
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
		Config:    record.Config,
		CreatedAt: now.Format(time.RFC3339),
		UpdatedAt: now.Format(time.RFC3339),
		CreatedBy: record.CreatedBy,
		UpdatedBy: record.UpdatedBy,
	}, nil
}

func (p *Postgres) UpdateProvider(ctx context.Context, key string, record service.ProviderRecord) (*service.ProviderRecord, error) {
	p.encKeyMu.RLock()
	encKey := p.encKey
	p.encKeyMu.RUnlock()

	storeCfg, err := atcrypto.EncryptLLMConfig(record.Config, encKey)
	if err != nil {
		return nil, fmt.Errorf("encrypt config: %w", err)
	}

	configJSON, err := json.Marshal(storeCfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	now := time.Now().UTC()

	query, _, err := p.goqu.Update(p.tableProviders).Set(
		goqu.Record{
			"config":     configJSON,
			"updated_at": now,
			"updated_by": record.UpdatedBy,
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

func rowToRecord(row providerRow, encKey []byte) (*service.ProviderRecord, error) {
	var cfg config.LLMConfig
	if err := json.Unmarshal(row.Config, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal provider config for %q: %w", row.Key, err)
	}

	cfg, err := atcrypto.DecryptLLMConfig(cfg, encKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt provider config for %q: %w", row.Key, err)
	}

	return &service.ProviderRecord{
		ID:        row.ID,
		Key:       row.Key,
		Config:    cfg,
		CreatedAt: row.CreatedAt.Format(time.RFC3339),
		UpdatedAt: row.UpdatedAt.Format(time.RFC3339),
		CreatedBy: row.CreatedBy,
		UpdatedBy: row.UpdatedBy,
	}, nil
}

// ─── Key Rotation ───

// RotateEncryptionKey decrypts all provider configs with the current key,
// re-encrypts them with newKey, and updates the rows atomically.
// Passing nil as newKey disables encryption (stores plaintext).
func (p *Postgres) RotateEncryptionKey(ctx context.Context, newKey []byte) error {
	p.encKeyMu.Lock()
	defer p.encKeyMu.Unlock()

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Read all provider rows within the transaction with FOR UPDATE to
	// prevent concurrent CRUD writes from inserting rows encrypted with
	// the old key while rotation is in progress.
	selectQuery, _, err := p.goqu.From(p.tableProviders).
		Select("id", "key", "config").
		ForUpdate(exp.Wait).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build select query: %w", err)
	}

	rows, err := tx.QueryContext(ctx, selectQuery)
	if err != nil {
		return fmt.Errorf("list providers for rotation: %w", err)
	}

	type rowData struct {
		id     string
		key    string
		config json.RawMessage
	}

	var allRows []rowData
	for rows.Next() {
		var r rowData
		if err := rows.Scan(&r.id, &r.key, &r.config); err != nil {
			rows.Close()
			return fmt.Errorf("scan provider row: %w", err)
		}
		allRows = append(allRows, r)
	}
	rows.Close()

	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate provider rows: %w", err)
	}

	// Re-encrypt each row: decrypt with old key, encrypt with new key.
	for _, r := range allRows {
		var cfg config.LLMConfig
		if err := json.Unmarshal(r.config, &cfg); err != nil {
			return fmt.Errorf("unmarshal config for %q: %w", r.key, err)
		}

		// Decrypt with the current key.
		cfg, err := atcrypto.DecryptLLMConfig(cfg, p.encKey)
		if err != nil {
			return fmt.Errorf("decrypt config for %q: %w", r.key, err)
		}

		// Re-encrypt with the new key (nil newKey = store as plaintext).
		cfg, err = atcrypto.EncryptLLMConfig(cfg, newKey)
		if err != nil {
			return fmt.Errorf("re-encrypt config for %q: %w", r.key, err)
		}

		configJSON, err := json.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("marshal config for %q: %w", r.key, err)
		}

		updateQuery, _, err := p.goqu.Update(p.tableProviders).Set(
			goqu.Record{"config": configJSON},
		).Where(goqu.I("id").Eq(r.id)).ToSQL()
		if err != nil {
			return fmt.Errorf("build update query for %q: %w", r.key, err)
		}

		if _, err := tx.ExecContext(ctx, updateQuery); err != nil {
			return fmt.Errorf("update provider %q: %w", r.key, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	// Update the in-memory key only after a successful commit.
	p.encKey = newKey

	slog.Info("encryption key rotated", "providers_updated", len(allRows))

	return nil
}

// SetEncryptionKey updates the in-memory encryption key without re-encrypting
// database rows. Used by peer instances when they receive a key rotation
// broadcast from the instance that performed the actual rotation.
func (p *Postgres) SetEncryptionKey(newKey []byte) {
	p.encKeyMu.Lock()
	p.encKey = newKey
	p.encKeyMu.Unlock()
}
