package service

import (
	"context"
	"time"
)

// AuthSecurityState is private, bounded state operated on only under the account
// row lock. Secret fields are encrypted by the store, never serialized to HTTP.
type AuthSecurityState struct {
	Secret       string
	LastStep     int64
	Generation   int64
	BackupHashes []string
	Transactions []AuthTransaction
	Window       time.Time
	Attempts     int
}

type AuthTransaction struct {
	Hash       string
	Binding    string
	Purpose    string
	Method     string
	SessionID  string
	Version    int64
	Generation int64
	Expires    time.Time
	Attempts   int
	Remember   bool
	Secret     string
	Data       []byte
}

// AuthSecurityUpdate is an atomic account-security unit of work. A callback
// rejection can be committed as an attempt by returning nil and recording the
// public error outside the callback. Session and reset are applied at commit.
type AuthSecurityUpdate struct {
	User           AuthUser
	State          AuthSecurityState
	Now            time.Time
	Session        *AuthSession
	Revoke         bool
	ResetPassword  string
	PasswordHash   string
	Deadline       time.Time
	RecoveryAction string
	RecoverySource string
}

type AuthSecurityStorer interface {
	UpdateAuthSecurity(context.Context, string, string, int64, func(*AuthSecurityUpdate) error) error
}

type AuthSecurityAdmissionStorer interface {
	AdmitAuthSecuritySource(context.Context, string) (bool, error)
}

type AuthRecoveryIssuer interface {
	IssueAuthRecovery(context.Context, string, string, int64, string, string, string) error
}
