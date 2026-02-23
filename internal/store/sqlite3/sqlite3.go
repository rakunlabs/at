package sqlite3

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

	// encKey is the AES-256 key used to encrypt/decrypt sensitive provider
	// fields. nil means encryption is disabled. Protected by encKeyMu.
	encKey   []byte
	encKeyMu sync.RWMutex
}

func New(ctx context.Context, cfg *config.StoreSQLite, encKey []byte) (*SQLite, error) {
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
		encKey:         encKey,
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

	s.encKeyMu.RLock()
	encKey := s.encKey
	s.encKeyMu.RUnlock()

	var result []service.ProviderRecord
	for rows.Next() {
		var row providerRow
		if err := rows.Scan(&row.ID, &row.Key, &row.Config, &row.CreatedAt, &row.UpdatedAt); err != nil {
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

	s.encKeyMu.RLock()
	encKey := s.encKey
	s.encKeyMu.RUnlock()

	return rowToRecord(row, encKey)
}

func (s *SQLite) CreateProvider(ctx context.Context, key string, cfg config.LLMConfig) (*service.ProviderRecord, error) {
	s.encKeyMu.RLock()
	encKey := s.encKey
	s.encKeyMu.RUnlock()

	storeCfg, err := atcrypto.EncryptLLMConfig(cfg, encKey)
	if err != nil {
		return nil, fmt.Errorf("encrypt config: %w", err)
	}

	configJSON, err := json.Marshal(storeCfg)
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
	s.encKeyMu.RLock()
	encKey := s.encKey
	s.encKeyMu.RUnlock()

	storeCfg, err := atcrypto.EncryptLLMConfig(cfg, encKey)
	if err != nil {
		return nil, fmt.Errorf("encrypt config: %w", err)
	}

	configJSON, err := json.Marshal(storeCfg)
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

func rowToRecord(row providerRow, encKey []byte) (*service.ProviderRecord, error) {
	var cfg config.LLMConfig
	if err := json.Unmarshal([]byte(row.Config), &cfg); err != nil {
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
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}, nil
}

// ─── Key Rotation ───

// RotateEncryptionKey decrypts all provider configs with the current key,
// re-encrypts them with newKey, and updates the rows atomically.
// Passing nil as newKey disables encryption (stores plaintext).
func (s *SQLite) RotateEncryptionKey(ctx context.Context, newKey []byte) error {
	s.encKeyMu.Lock()
	defer s.encKeyMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Read all provider rows within the transaction.
	selectQuery, _, err := s.goqu.From(s.tableProviders).
		Select("id", "key", "config").
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
		config string
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
		if err := json.Unmarshal([]byte(r.config), &cfg); err != nil {
			return fmt.Errorf("unmarshal config for %q: %w", r.key, err)
		}

		// Decrypt with the current key.
		cfg, err := atcrypto.DecryptLLMConfig(cfg, s.encKey)
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

		updateQuery, _, err := s.goqu.Update(s.tableProviders).Set(
			goqu.Record{"config": string(configJSON)},
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
	s.encKey = newKey

	slog.Info("encryption key rotated", "providers_updated", len(allRows))

	return nil
}

// SetEncryptionKey updates the in-memory encryption key without re-encrypting
// database rows. Used by peer instances when they receive a key rotation
// broadcast from the instance that performed the actual rotation.
func (s *SQLite) SetEncryptionKey(newKey []byte) {
	s.encKeyMu.Lock()
	s.encKey = newKey
	s.encKeyMu.Unlock()
}
