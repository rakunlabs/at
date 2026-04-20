package service

import (
	"context"

	"github.com/rakunlabs/query"
)

// ─── Connection Management ───
//
// A Connection represents a named, reusable set of credentials for an external
// service (YouTube, Google, Twitter, etc.). Multiple connections can exist for
// the same provider, e.g. several YouTube channels. Agents reference connections
// by ID via AgentConfig.Connections (per-agent default) and SkillRef.Connections
// (per-skill override). One connection can be shared by any number of agents.
//
// The Credentials field is encrypted at rest (AES-256-GCM) using the database
// encryption key configured for the store.

// ConnectionCredentials holds the secret bundle for a connection. Different
// providers populate different subsets of these fields. Marshaled to JSON,
// then encrypted as a single ciphertext blob in the credentials column.
type ConnectionCredentials struct {
	// OAuth2-style fields.
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`

	// Single-token providers (e.g. API key based skills).
	APIKey string `json:"api_key,omitempty"`

	// Free-form bag for additional secrets (multi-key skills, future providers).
	// Keys here are the original variable names (e.g. "openrouter_api_key").
	Extra map[string]string `json:"extra,omitempty"`
}

// Connection represents a named, reusable credential set for a single external
// provider instance.
type Connection struct {
	ID           string                `json:"id"`
	Provider     string                `json:"provider"`                // "youtube", "google", "twitter", or skill slug for token-only
	Name         string                `json:"name"`                    // unique within provider, e.g. "Main Channel"
	AccountLabel string                `json:"account_label,omitempty"` // human-readable identity (channel title, email)
	Description  string                `json:"description,omitempty"`
	Credentials  ConnectionCredentials `json:"credentials"`        // encrypted JSON blob in storage
	Metadata     map[string]any        `json:"metadata,omitempty"` // scopes, expires_at, etc.
	CreatedAt    string                `json:"created_at"`
	UpdatedAt    string                `json:"updated_at"`
	CreatedBy    string                `json:"created_by,omitempty"`
	UpdatedBy    string                `json:"updated_by,omitempty"`
}

// ConnectionStorer defines CRUD operations for connections.
type ConnectionStorer interface {
	ListConnections(ctx context.Context, q *query.Query) (*ListResult[Connection], error)
	ListConnectionsByProvider(ctx context.Context, provider string) ([]Connection, error)
	GetConnection(ctx context.Context, id string) (*Connection, error)
	GetConnectionByName(ctx context.Context, provider, name string) (*Connection, error)
	CreateConnection(ctx context.Context, c Connection) (*Connection, error)
	UpdateConnection(ctx context.Context, id string, c Connection) (*Connection, error)
	DeleteConnection(ctx context.Context, id string) error
}
