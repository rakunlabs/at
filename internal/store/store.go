package store

import (
	"context"
	"errors"

	"github.com/rakunlabs/at/internal/config"
	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres"
)

// StorerClose combines all store interfaces with a Close method.
type StorerClose interface {
	service.Storer
	Close()
}

// New creates a StorerClose based on the given store configuration.
//
// Postgres is the only supported backend. A datasource must be configured
// via `store.postgres.datasource` (YAML) — for local development,
// `make env` starts a matching postgres and the DSN is
// postgresql://postgres@localhost:5432/postgres?sslmode=disable.
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

	if cfg.Postgres == nil {
		return nil, errors.New("store not configured: set store.postgres.datasource (postgres is the only supported backend; run `make env` for a local instance)")
	}

	return postgres.New(ctx, cfg.Postgres, encKey)
}
