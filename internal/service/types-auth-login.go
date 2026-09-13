package service

import (
	"context"
	"time"
)

type AuthLoginEvent struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Action    string    `json:"action"`
	SourceIP  string    `json:"source_ip"`
	UserAgent string    `json:"user_agent"`
	ActorID   string    `json:"actor_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type AuthLastLogin struct {
	At       time.Time `json:"at"`
	SourceIP string    `json:"source_ip"`
}

type AuthLoginEventStorer interface {
	RecordAuthLoginEvent(context.Context, AuthLoginEvent) error
	ListAuthLoginEvents(context.Context, string, uint) ([]AuthLoginEvent, error)
	ListAuthLastLogins(context.Context, []string) (map[string]AuthLastLogin, error)
	CleanupAuthLoginEvents(context.Context, uint) error
}
