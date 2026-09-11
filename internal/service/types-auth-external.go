package service

import (
	"context"
	"encoding/json"
	"time"
)

// AuthIdentityProvider is installation configuration, never a workspace grant.
type AuthIdentityProvider struct {
	ID              string   `json:"id"`
	Label           string   `json:"label"`
	Mode            string   `json:"mode"`
	Enabled         bool     `json:"enabled"`
	Version         int64    `json:"version"`
	Issuer          string   `json:"issuer"`
	ClientID        string   `json:"client_id"`
	ClientSecret    string   `json:"-"`
	HasClientSecret bool     `json:"has_client_secret"`
	AuthURL         string   `json:"auth_url"`
	TokenURL        string   `json:"token_url"`
	UserInfoURL     string   `json:"userinfo_url"`
	JWKSURL         string   `json:"jwks_url"`
	Scopes          []string `json:"scopes"`
	SubjectClaim    string   `json:"subject_claim"`
	AuthHeaderStyle string   `json:"auth_header_style"`
}

type AuthExternalAccount struct {
	UserID    string
	SessionID string
	Version   int64
}

// AuthExternalFlow payload is encrypted, and its lookup/browser handle is hashed.
type AuthExternalFlow struct {
	Hash            string
	UserID          string
	ProviderID      string
	ProviderVersion int64
	Source          string
	ExpiresAt       time.Time
	Payload         json.RawMessage
}

type AuthIdentityLink struct {
	ID            string `json:"id" db:"id"`
	ProviderID    string `json:"provider_id" db:"provider_id"`
	Issuer        string `json:"issuer" db:"issuer"`
	Subject       string `json:"subject" db:"subject"`
	UserID        string `json:"-" db:"user_id"`
	Email         string `json:"email,omitempty" db:"email"`
	EmailVerified bool   `json:"email_verified" db:"email_verified"`
	// AssertedPermissions is raw provider-qualified metadata, not local authority.
	AssertedPermissions json.RawMessage `json:"asserted_permissions" db:"asserted_permissions"`
}

type AuthExternalCompletion struct {
	ProviderID      string
	ProviderVersion int64
	LinkID          string
	Remember        bool
	Deadline        time.Time
	Account         AuthExternalAccount
	ReauthPurpose   string
	MobileRequestID string
}

type authExternalCompletionContextKey struct{}

// ContextWithAuthExternalCompletion carries only server-verified provenance.
// Common MFA persists this value and restores it for final session creation.
func ContextWithAuthExternalCompletion(ctx context.Context, c AuthExternalCompletion) context.Context {
	return context.WithValue(ctx, authExternalCompletionContextKey{}, c)
}

func AuthExternalCompletionFromContext(ctx context.Context) (AuthExternalCompletion, bool) {
	c, ok := ctx.Value(authExternalCompletionContextKey{}).(AuthExternalCompletion)
	return c, ok
}

type AuthExternalStorer interface {
	ListAuthIdentityProviders(context.Context) ([]AuthIdentityProvider, error)
	GetAuthIdentityProvider(context.Context, string) (*AuthIdentityProvider, error)
	SaveAuthIdentityProvider(context.Context, AuthIdentityProvider, *string) (*AuthIdentityProvider, error)
	SaveAuthExternalFlow(context.Context, AuthExternalFlow) error
	ConsumeAuthExternalFlow(context.Context, string, string, int64) (*AuthExternalFlow, error)
	CompleteAuthExternalIdentity(context.Context, AuthIdentityLink, int64, *AuthExternalAccount, time.Time) (*AuthUser, *AuthIdentityLink, error)
	ListAuthIdentityLinks(context.Context, string) ([]AuthIdentityLink, error)
	UnlinkAuthIdentity(context.Context, string, AuthExternalAccount) error
}
