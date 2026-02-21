package store

import (
	"context"
	"errors"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres"
)

// StorerClose combines the ProviderStorer and APITokenStorer interfaces with a Close method.
type StorerClose interface {
	service.ProviderStorer
	service.APITokenStorer
	Close()
}

// New creates a StorerClose based on the given store configuration.
// Currently only PostgreSQL is supported.
func New(ctx context.Context, cfg config.Store) (StorerClose, error) {
	var store StorerClose
	var err error

	if cfg.Postgres != nil {
		store, err = postgres.New(ctx, cfg.Postgres)
		if err != nil {
			return nil, err
		}
	}

	if store == nil {
		return nil, errors.New("no store configured")
	}

	return store, nil
}
