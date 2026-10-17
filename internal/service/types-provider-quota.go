package service

import (
	"context"
	"errors"
	"time"
)

var ErrProviderQuotaExceeded = errors.New("provider_quota_exceeded")

type ProviderQuotaError struct {
	ResetAt time.Time
}

func (e *ProviderQuotaError) Error() string {
	return "provider quota reached; resets at " + e.ResetAt.UTC().Format(time.RFC3339)
}
func (e *ProviderQuotaError) Unwrap() error { return ErrProviderQuotaExceeded }

type ProviderQuotaReservation struct {
	ID             string
	GuardOverrides bool
	TokenLimited   bool
}

type ProviderQuotaUsage struct {
	Model        string    `json:"model"`
	Period       string    `json:"period"`
	ResetAt      time.Time `json:"reset_at"`
	Requests     int64     `json:"requests"`
	Tokens       int64     `json:"tokens"`
	Reserved     int64     `json:"reserved_tokens"`
	RequestLimit int64     `json:"request_limit"`
	TokenLimit   int64     `json:"token_limit"`
}

// Internal accounting interface. providerID must be the resolved physical row,
// never a caller's bare provider key. No credentials or identities are returned.
type ProviderQuotaStorer interface {
	ReserveProviderQuota(context.Context, string, string, int64) (*ProviderQuotaReservation, error)
	SettleProviderQuota(context.Context, string, int64, bool) error
	ProviderQuotaUsage(context.Context, string) ([]ProviderQuotaUsage, error)
}
