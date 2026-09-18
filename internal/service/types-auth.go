package service

import (
	"context"
	"errors"
	"time"
)

var ErrAuthConflict = errors.New("auth conflict")
var ErrAuthSessionLimit = errors.New("maximum live sessions reached")

// AuthRefreshEarlyError is a non-consuming refusal, unlike credential replay.
type AuthRefreshEarlyError struct{ RetryAfter time.Duration }

func (e *AuthRefreshEarlyError) Error() string { return "refresh too early" }

// AuthUser is private credential state, not an API response or a tenant scope.
type AuthUser struct {
	ID             string
	Username       string
	PasswordHash   string `json:"-"`
	Admin          bool
	Disabled       bool
	SessionVersion int64 `json:"-"`
}

// AuthUserQuery bounds an administrator's user listing. Search is deliberately
// part of the store query rather than a client-side filter over one page: an
// externally provisioned account is named `external-<ulid>`, which nobody can
// recognise or type, so the only way to find one is to match the email its
// identity link carries.
type AuthUserQuery struct {
	After  string
	Search string
	Limit  uint
}

// AuthUserIdentity is the administrator-visible part of an identity link. It
// carries no asserted permissions: this answers "who is this account", not
// "what may it do".
type AuthUserIdentity struct {
	ProviderID string `json:"provider_id" db:"provider_id"`
	Subject    string `json:"subject" db:"subject"`
	// Username is what the provider calls this person. The local username of an
	// externally provisioned account is `external-<ulid>`, so without it the
	// directory has no name a human recognises unless the provider also
	// released an email.
	Username      string `json:"username" db:"username"`
	Email         string `json:"email" db:"email"`
	EmailVerified bool   `json:"email_verified" db:"email_verified"`
}

// AuthUserWorkspace is one membership row, reported so an administrator can see
// why an account reaches nothing without opening every workspace in turn.
type AuthUserWorkspace struct {
	WorkspaceID string `json:"workspace_id" db:"workspace_id"`
	Name        string `json:"name" db:"name"`
	Role        string `json:"role" db:"role"`
	Status      string `json:"status" db:"status"`
}

// AuthUserDirectory is optional: it is what turns an opaque account into a
// person. Kept off AuthStorer because it reads tables (identity links,
// workspace memberships) that native authentication itself never needs.
type AuthUserDirectory interface {
	ListAuthUserIdentities(context.Context, []string) (map[string][]AuthUserIdentity, error)
	ListAuthUserWorkspaces(context.Context, string) ([]AuthUserWorkspace, error)
}

type AuthSession struct {
	Transport       string
	AccessHash      string `json:"-"`
	RefreshHash     string `json:"-"`
	AccessExpiresAt time.Time
	Remember        bool
	// Hash is the stable, nonsecret family ID (the retained v25 column name).
	Hash      string `json:"-"`
	UserID    string
	Version   int64
	ExpiresAt time.Time
	// AdmissionDeadline bounds passkey/mobile issuance; zero means password login.
	// It is not persisted as the session expiry and is never client supplied.
	AdmissionDeadline time.Time `json:"-"`
}

type AuthMobileRequest struct {
	ID            string    `db:"id"`
	Challenge     string    `db:"challenge"`
	State         string    `db:"state"`
	Remember      bool      `db:"remember"`
	DeviceName    string    `db:"device_name"`
	CreatedAt     time.Time `db:"created_at"`
	ExpiresAt     time.Time `db:"expires_at"`
	UserID        string    `db:"user_id"`
	Version       int64     `db:"version"`
	WebSession    string    `db:"web_session"`
	CodeHash      string    `db:"code_hash"`
	CodeExpiresAt time.Time `db:"code_expires_at"`
}

type AuthMobileStorer interface {
	CreateAuthMobileRequest(context.Context, AuthMobileRequest) error
	GetAuthMobileRequest(context.Context, string) (*AuthMobileRequest, error)
	DecideAuthMobileRequest(context.Context, string, string, string, string) (*AuthMobileRequest, error)
	RedeemAuthMobileCode(context.Context, string, string, AuthSession, time.Duration, time.Duration) (*AuthUser, *AuthSession, error)
	RotateAuthRefreshTransport(context.Context, string, string, string, string) (*AuthUser, *AuthSession, error)
	RevokeAuthCredentialTransport(context.Context, string, string, string) error
	CleanupAuthMobileRequests(context.Context, uint) error
}

// AuthCredentialStorer keeps bearer credentials separate from stable session IDs.
type AuthCredentialStorer interface {
	ResolveAuthAccess(context.Context, string) (*AuthUser, *AuthSession, error)
	RotateAuthRefresh(context.Context, string, string, string) (*AuthUser, *AuthSession, error)
	RevokeAuthCredential(context.Context, string) error
	CleanupAuthCredentials(context.Context, uint) (int64, error)
}

// AuthStorer is deliberately separate from Storer to keep unrelated stores and
// test doubles independent of native authentication.
type AuthStorer interface {
	CreateAuthUser(context.Context, AuthUser, bool) (*AuthUser, error)
	GetAuthUser(context.Context, string) (*AuthUser, error) // normalized username
	GetAuthUserByID(context.Context, string) (*AuthUser, error)
	ListAuthUsers(context.Context, AuthUserQuery) ([]AuthUser, error)
	EnableAuthUser(context.Context, string) (bool, error)                      // always revoke
	SetAuthUserPassword(context.Context, string, string, *int64) (bool, error) // optional verified version; always revoke
	CreateAuthSession(context.Context, AuthSession) error
	ResolveAuthSession(context.Context, string) (*AuthUser, time.Time, error)
	DeleteAuthSession(context.Context, string) error
	InvalidateAuthUser(context.Context, string, bool) (bool, error) // optionally disable, always revoke
	// DeleteAuthUser removes the account and everything keyed on it. It is
	// irreversible and refuses the last active administrator, so disable stays
	// the reversible option.
	DeleteAuthUser(context.Context, string) (bool, error)
}
