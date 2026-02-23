package store

import (
	"context"

	"github.com/rakunlabs/at/internal/config"
	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/memory"
	"github.com/rakunlabs/at/internal/store/postgres"
	"github.com/rakunlabs/at/internal/store/sqlite3"
)

// StorerClose combines the ProviderStorer and APITokenStorer interfaces with a Close method.
type StorerClose interface {
	service.ProviderStorer
	service.APITokenStorer
	Close()
}

// New creates a StorerClose based on the given store configuration.
// Falls back to an in-memory store if no backend is configured.
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

	var store StorerClose
	var err error

	if cfg.Postgres != nil {
		store, err = postgres.New(ctx, cfg.Postgres, encKey)
		if err != nil {
			return nil, err
		}
	}

	if store == nil && cfg.SQLite != nil {
		store, err = sqlite3.New(ctx, cfg.SQLite, encKey)
		if err != nil {
			return nil, err
		}
	}

	if store == nil {
		store = memory.New()
	}

	return store, nil
}
