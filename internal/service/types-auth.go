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
	ListAuthUsers(context.Context, string, uint) ([]AuthUser, error)           // after ID, bounded limit
	EnableAuthUser(context.Context, string) (bool, error)                      // always revoke
	SetAuthUserPassword(context.Context, string, string, *int64) (bool, error) // optional verified version; always revoke
	CreateAuthSession(context.Context, AuthSession) error
	ResolveAuthSession(context.Context, string) (*AuthUser, time.Time, error)
	DeleteAuthSession(context.Context, string) error
	InvalidateAuthUser(context.Context, string, bool) (bool, error) // optionally disable, always revoke
}
