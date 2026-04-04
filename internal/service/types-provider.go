package service

import (
	"context"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/query"
)

// ─── Provider Management ───

// ProviderRecord represents a provider configuration stored in the database.
type ProviderRecord struct {
	ID        string           `json:"id"`
	Key       string           `json:"key"`
	Config    config.LLMConfig `json:"config"`
	CreatedAt string           `json:"created_at"`
	UpdatedAt string           `json:"updated_at"`
	CreatedBy string           `json:"created_by"`
	UpdatedBy string           `json:"updated_by"`
}

// ProviderStorer defines CRUD operations for provider configurations
// stored in a persistent backend (e.g., PostgreSQL).
type ProviderStorer interface {
	ListProviders(ctx context.Context, q *query.Query) (*ListResult[ProviderRecord], error)
	GetProvider(ctx context.Context, key string) (*ProviderRecord, error)
	CreateProvider(ctx context.Context, record ProviderRecord) (*ProviderRecord, error)
	UpdateProvider(ctx context.Context, key string, record ProviderRecord) (*ProviderRecord, error)
	DeleteProvider(ctx context.Context, key string) error
}

// KeyRotator is optionally implemented by stores that support encryption
// key rotation for provider credentials. The method decrypts all provider
// configs with the current key, re-encrypts them with newKey, and updates
// the rows atomically within a transaction. Passing nil as newKey disables
// encryption (all values are stored as plaintext).
type KeyRotator interface {
	RotateEncryptionKey(ctx context.Context, newKey []byte) error
}

// EncryptionKeyUpdater is optionally implemented by stores that support
// updating the in-memory encryption key without re-encrypting database rows.
// This is used by peer instances in a cluster when they receive a key rotation
// broadcast from the instance that performed the actual DB rotation.
type EncryptionKeyUpdater interface {
	SetEncryptionKey(newKey []byte)
}
