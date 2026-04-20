package store

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/rakunlabs/at/internal/config"
	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres"
	"github.com/rakunlabs/at/internal/store/sqlite3"
)

// defaultSQLiteDatasource is the on-disk SQLite file used when no store
// backend is configured. Lives under ./data/ so Docker users can bind-mount
// the host's persistent volume to /data in a single step:
//
//	docker run -v $HOME/at-data:/data ...
//
// There is no in-memory fallback: AT always persists to disk.
const defaultSQLiteDatasource = "data/at.db"

// StorerClose combines all store interfaces with a Close method.
type StorerClose interface {
	service.Storer
	Close()
}

// New creates a StorerClose based on the given store configuration.
//
// Priority:
//  1. Postgres, if cfg.Postgres is set.
//  2. SQLite, if cfg.SQLite is set.
//  3. Default SQLite at ./data/at.db, if neither is set.
func New(ctx context.Context, cfg config.Store) (StorerClose, error) {
	// Derive the AES-256 encryption key from the config passphrase.
	// If no encryption key is configured, encKey stays nil and
	// encryption is transparently disabled.
	var encKey []byte
	if cfg.EncryptionKey != "" {
		var err error
		encKey, err = atcrypto.DeriveKey(cfg.EncryptionKey)
		if err != nil {
			return nil, err
		}
	}

	if cfg.Postgres != nil {
		return postgres.New(ctx, cfg.Postgres, encKey)
	}

	sqliteCfg := cfg.SQLite
	if sqliteCfg == nil {
		slog.Info("store: no backend configured, using default sqlite",
			"datasource", defaultSQLiteDatasource)
		sqliteCfg = &config.StoreSQLite{
			Datasource: defaultSQLiteDatasource,
		}
	}

	// Ensure the parent directory exists. SQLite does not create missing
	// directories on open, and this is the common first-run failure mode
	// for the default ./data/at.db path.
	if err := ensureParentDir(sqliteCfg.Datasource); err != nil {
		return nil, fmt.Errorf("prepare sqlite directory: %w", err)
	}

	return sqlite3.New(ctx, sqliteCfg, encKey)
}

// ensureParentDir creates the parent directory of a file-based SQLite
// datasource if it does not already exist. It is a no-op for non-file
// datasources (`:memory:`, URIs without a path, etc.).
func ensureParentDir(datasource string) error {
	if datasource == "" || datasource == ":memory:" {
		return nil
	}
	// Strip the optional "file:" prefix and any "?..." query suffix used by
	// SQLite URI datasources (e.g. "file:/tmp/x.db?cache=shared").
	path := datasource
	if len(path) > 5 && path[:5] == "file:" {
		path = path[5:]
	}
	if i := indexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	dir := filepath.Dir(path)
	if dir == "" || dir == "." || dir == "/" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

// indexByte is a tiny helper to avoid importing strings for one call.
func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
