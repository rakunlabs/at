package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/query"
)

// ─── Provider Management ───

// ProviderRecord represents a provider configuration stored in the database.
type ProviderRecord struct {
	WorkspaceID string           `json:"workspace_id,omitempty"`
	OwnerUserID string           `json:"owner_user_id,omitempty"`
	ID          string           `json:"id"`
	Key         string           `json:"key"`
	Reference   string           `json:"reference,omitempty"`
	Scope       string           `json:"scope,omitempty"`
	Config      config.LLMConfig `json:"config"`
	CreatedAt   string           `json:"created_at"`
	UpdatedAt   string           `json:"updated_at"`
	CreatedBy   string           `json:"created_by"`
	UpdatedBy   string           `json:"updated_by"`
}

const (
	ProviderScopePersonal  = "personal"
	ProviderScopeWorkspace = "workspace"
	ProviderScopeGlobal    = "global"

	providerReferencePrefix = "provider:"
)

func PersonalProviderReference(id string) string {
	if id == "" {
		return ""
	}
	return providerReferencePrefix + id
}

func ParsePersonalProviderReference(value string) (string, bool) {
	id, ok := strings.CutPrefix(value, providerReferencePrefix)
	return id, ok && id != ""
}

func ValidProviderScope(scope string) bool {
	return scope == ProviderScopePersonal || scope == ProviderScopeWorkspace || scope == ProviderScopeGlobal
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

// PersonalProviderStorer owns account-scoped credentials independently of a
// workspace lifecycle. Personal records are addressed by immutable ID; legacy
// workspace/global providers keep their key-based contract.
type PersonalProviderStorer interface {
	ListPersonalProviders(ctx context.Context, q *query.Query) (*ListResult[ProviderRecord], error)
	GetPersonalProvider(ctx context.Context, id string) (*ProviderRecord, error)
	CreatePersonalProvider(ctx context.Context, record ProviderRecord) (*ProviderRecord, error)
	UpdatePersonalProvider(ctx context.Context, id string, record ProviderRecord) (*ProviderRecord, error)
	DeletePersonalProvider(ctx context.Context, id string) error
	SetPersonalProviderScope(ctx context.Context, id, scope, createdBy string) (*ProviderRecord, error)
	SetPersonalProviderDisabled(ctx context.Context, id string, disabled bool, updatedBy string) error
}

// ErrProviderDisabled is returned when a provider would otherwise be admitted
// but is disabled. It wraps ErrAccessDenied so callers that only distinguish
// denial keep working, while error text can name the real reason.
var ErrProviderDisabled = fmt.Errorf("provider is disabled: %w", ErrAccessDenied)

// ProviderDisableStorer is implemented by stores that can park a provider
// without touching its credentials. It is deliberately a separate interface
// from ProviderStorer: the flag is an availability decision, so ordinary
// config updates must not carry it.
type ProviderDisableStorer interface {
	SetProviderDisabled(ctx context.Context, key string, disabled bool, updatedBy string) error
}

type WorkspaceProviderGrant struct {
	WorkspaceID   string   `json:"workspace_id" db:"workspace_id"`
	ProviderID    string   `json:"provider_id" db:"provider_id"`
	ModelPatterns []string `json:"model_patterns"`
}

// ResolveWorkspaceProviderForUse is internal credential resolution for one model
// admission, never a credential DTO endpoint. Every call rechecks live grants.
type WorkspaceProviderStorer interface {
	ResolveWorkspaceProviderForUse(context.Context, string, string) (*ProviderRecord, error)
	ListWorkspaceProviderGrants(context.Context) ([]WorkspaceProviderGrant, error)
	SaveWorkspaceProviderGrant(context.Context, WorkspaceProviderGrant) error
	DeleteWorkspaceProviderGrant(context.Context, string) error
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
