package service

import (
	"context"

	"github.com/rakunlabs/query"
)

// ─── Connector Registry ───
//
// A Connector describes a TYPE of external-service connection: its auth kind,
// OAuth2 endpoints/scopes, and the credential field schema that drives the UI.
// Connectors are the data-driven replacement for the formerly hardcoded
// google/youtube OAuth catalog (see internal/server/oauth.go). They hold NO
// secrets — only definitions — so they are stored unencrypted. Credential
// instances live in Connection rows (encrypted), bound to a connector by its
// Slug (which equals Connection.Provider).
//
// Connectors come from two sources, merged at read time by the server:
//  1. Built-in definitions embedded as JSON (internal/server/connectors/*.json)
//  2. User-defined / override rows persisted in the connectors table
//
// A DB row with the same Slug overrides the built-in of that slug.

// Connector auth kinds.
const (
	ConnectorAuthOAuth2 = "oauth2"
	ConnectorAuthToken  = "token"
	ConnectorAuthCustom = "custom"
)

// Connector field types.
const (
	ConnectorFieldText   = "text"
	ConnectorFieldSecret = "secret"
)

// ConnectorField describes a single credential input the connector needs.
// Well-known keys (client_id, client_secret, refresh_token, api_key) map onto
// the ConnectionCredentials struct fields; any other key is stored in the
// Extra bag keyed by its full name.
type ConnectorField struct {
	Key         string `json:"key"`
	Label       string `json:"label,omitempty"`
	Type        string `json:"type,omitempty"` // "text" | "secret"
	Required    bool   `json:"required,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Help        string `json:"help,omitempty"`
}

// ConnectorOAuth holds the OAuth2 flow parameters for an oauth2 connector.
type ConnectorOAuth struct {
	AuthURL  string   `json:"auth_url"`
	TokenURL string   `json:"token_url"`
	Scopes   []string `json:"scopes,omitempty"`
	// AccessType (e.g. "offline") and Prompt (e.g. "consent") are Google-style
	// hints; left empty they are simply omitted from the authorize URL.
	AccessType string `json:"access_type,omitempty"`
	Prompt     string `json:"prompt,omitempty"`
	// UsePKCE enables the Proof Key for Code Exchange flow (required by X,
	// public OAuth clients, etc.).
	UsePKCE bool `json:"use_pkce,omitempty"`
	// UserinfoURL + AccountLabelPath drive the optional best-effort fetch of a
	// human-readable account label after a successful exchange. AccountLabelPath
	// is a dot-path into the userinfo JSON (e.g. "email" or "items.0.snippet.title").
	UserinfoURL      string `json:"userinfo_url,omitempty"`
	AccountLabelPath string `json:"account_label_path,omitempty"`
	// ExtraAuthParams are appended verbatim to the authorize URL query.
	ExtraAuthParams map[string]string `json:"extra_auth_params,omitempty"`
}

// Connector is a data-driven definition of an external-service connection type.
type Connector struct {
	Slug        string           `json:"slug"`
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Icon        string           `json:"icon,omitempty"`
	AuthKind    string           `json:"auth_kind"` // oauth2 | token | custom
	OAuth       *ConnectorOAuth  `json:"oauth,omitempty"`
	Fields      []ConnectorField `json:"fields,omitempty"`
	Builtin     bool             `json:"builtin,omitempty"`
	CreatedAt   string           `json:"created_at,omitempty"`
	UpdatedAt   string           `json:"updated_at,omitempty"`
	CreatedBy   string           `json:"created_by,omitempty"`
	UpdatedBy   string           `json:"updated_by,omitempty"`
}

// ConnectorStorer defines CRUD operations for user-defined connector rows.
// Built-in connectors are NOT stored here; the server merges them in at
// runtime, with DB rows overriding built-ins by slug.
type ConnectorStorer interface {
	ListConnectors(ctx context.Context, q *query.Query) (*ListResult[Connector], error)
	GetConnector(ctx context.Context, slug string) (*Connector, error)
	CreateConnector(ctx context.Context, c Connector) (*Connector, error)
	UpdateConnector(ctx context.Context, slug string, c Connector) (*Connector, error)
	DeleteConnector(ctx context.Context, slug string) error
}
