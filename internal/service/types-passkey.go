package service

import (
	"context"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/strategy/passkey"
)

// AuthPasskey's public metadata is separated from verifier state.
type AuthPasskey struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	CreatedAt  time.Time          `json:"created_at"`
	LastUsedAt *time.Time         `json:"last_used_at"`
	UserID     string             `json:"-"`
	Credential passkey.Credential `json:"-"`
}

// AuthChallenge is exclusively server-held; clients only receive WebAuthn options.
type AuthChallenge struct {
	Hash        string
	Purpose     string
	UserID      string
	Version     int64
	SessionHash string
	Remember    bool
	Name        string
	Data        passkey.SessionData
}

type AuthPasskeyStorer interface {
	ListAuthPasskeys(context.Context, string) ([]AuthPasskey, error)
	SaveAuthChallenge(context.Context, AuthChallenge) error
	ConsumeAuthChallenge(context.Context, string) (*AuthChallenge, error)
	CreateAuthPasskey(context.Context, AuthChallenge, passkey.Credential) error
	AdvanceAuthPasskey(context.Context, string, string, int64, uint32, uint32, time.Time) error
	DeleteAuthPasskey(context.Context, string, string, int64, string) error
}
