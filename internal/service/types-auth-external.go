package service

import (
	"context"
	"encoding/json"
	"time"
)

// AuthIdentityProvider is installation configuration, never a workspace grant.
//
// Every provider is a plain OAuth2 authorization-code client whose endpoints
// are configured one by one. There is deliberately no issuer URL and no OIDC
// discovery: discovery turned one stored string into four endpoints fetched
// over the network on every sign-in, so a provider's real configuration was
// whatever the remote document happened to say, and a login could not start
// while that document was unreachable. Explicit endpoints are also the only
// way to point at an IdP that publishes no discovery document.
type AuthIdentityProvider struct {
	ID              string `json:"id"`
	Label           string `json:"label"`
	Enabled         bool   `json:"enabled"`
	Version         int64  `json:"version"`
	ClientID        string `json:"client_id"`
	ClientSecret    string `json:"-"`
	HasClientSecret bool   `json:"has_client_secret"`
	AuthURL         string `json:"auth_url"`
	TokenURL        string `json:"token_url"`
	// UserInfoURL and JWKSURL are the two claim sources, and at least one is
	// required. UserInfoURL is read with the access token, so the response
	// speaks for the user it describes. JWKSURL instead verifies the
	// id_token's signature, audience, expiry and nonce. Configuring both
	// additionally binds the two together: the userinfo subject must match
	// the id_token subject.
	UserInfoURL     string   `json:"userinfo_url"`
	JWKSURL         string   `json:"jwks_url"`
	Scopes          []string `json:"scopes"`
	SubjectClaim    string   `json:"subject_claim"`
	AuthHeaderStyle string   `json:"auth_header_style"`
	// RolesClaims are claim paths whose values join the roles assertion used by
	// workspace permission mappings. Empty keeps the previous behaviour: only
	// top-level roles/groups/permissions/scope claims are recorded.
	RolesClaims []string `json:"roles_claims,omitempty"`
	// AllowedEmails restricts which identities this provider may assert:
	// `person@example.com` admits one address, `@example.com` admits a domain.
	// Empty admits everyone the provider authenticates, which is the previous
	// and still-default behaviour.
	//
	// This is admission, not authorization — it decides whether an account
	// exists at all, and grants nothing once it does. It is checked on every
	// sign-in rather than only at provisioning, so removing an entry actually
	// removes access instead of leaving the already-linked accounts (the ones
	// an administrator is trying to cut off) untouched.
	AllowedEmails []string `json:"allowed_emails,omitempty"`
}

// AuthIdentityNamespace is the identity space a provider's subjects live in.
// It is the provider ID rather than an issuer URL, because the issuer was the
// discovery root and went away with discovery; the ID is stable for the life
// of the provider and is what `auth_identity_links.issuer` stores.
func AuthIdentityNamespace(providerID string) string { return "oauth2:" + providerID }

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
	// Username is the handle the provider reports (OIDC preferred_username).
	// Display metadata refreshed from every sign-in, never an identity key.
	Username string `json:"username,omitempty" db:"username"`
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
