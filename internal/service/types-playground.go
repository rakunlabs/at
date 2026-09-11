package service

import (
	"context"
	"errors"
)

var (
	// ErrPlaygroundNotFound is returned for unknown conversations and for
	// conversations owned by another user. The two cases are deliberately
	// indistinguishable so ownership cannot be probed.
	ErrPlaygroundNotFound = errors.New("playground conversation not found")
	// ErrPlaygroundConflict is returned when a write cannot be serialized
	// against a concurrent one.
	ErrPlaygroundConflict = errors.New("playground conversation conflict")
	// ErrPlaygroundTooLarge is returned when a request would copy more rows
	// than the store is willing to move in one transaction. Refusing beats
	// truncating: a partially copied transcript is a wrong transcript.
	ErrPlaygroundTooLarge = errors.New("playground conversation too large")
)

// PlaygroundForkMaxMessages caps how many messages a single fork copies. A
// fork reads the whole prefix and rewrites it inside one transaction that
// holds the source row lock, so an unbounded prefix would block every
// concurrent append to the source for an unbounded time. A longer prefix is
// refused with ErrPlaygroundTooLarge (413 at the HTTP layer) rather than
// silently shortened: a partial transcript is a wrong fork, not a smaller one.
const PlaygroundForkMaxMessages = 2000

// PlaygroundConversation is a per-user private record of a Chat playground
// session. It is owner scoped only: there is no workspace, no sharing and no
// workspace capability governing it.
type PlaygroundConversation struct {
	ID           string `json:"id" db:"id"`
	OwnerUserID  string `json:"owner_user_id" db:"owner_user_id"`
	Title        string `json:"title" db:"title"`
	SystemPrompt string `json:"system_prompt" db:"system_prompt"`
	ProviderKey  string `json:"provider_key" db:"provider_key"`
	Model        string `json:"model" db:"model"`
	// Config is opaque client state (selected tools, MCP urls/headers, MCP
	// sets, skills). The server never interprets it.
	Config map[string]any `json:"config"`
	// ForkedFromID and ForkedFromSequence record where this conversation
	// branched off: the source conversation and the last message sequence
	// that was copied from it. Both are zero for a conversation started from
	// scratch, and ForkedFromID reverts to zero when the source is deleted
	// (the column is ON DELETE SET NULL, so the fork itself survives).
	ForkedFromID       string `json:"forked_from_id,omitempty" db:"forked_from_id"`
	ForkedFromSequence int64  `json:"forked_from_sequence,omitempty" db:"forked_from_sequence"`
	CreatedAt          string `json:"created_at" db:"created_at"`
	UpdatedAt          string `json:"updated_at" db:"updated_at"`
}

// PlaygroundMessage is one durable transcript entry.
type PlaygroundMessage struct {
	ID             string `json:"id" db:"id"`
	ConversationID string `json:"conversation_id" db:"conversation_id"`
	// Sequence is assigned by the store, never by the client. It is gapless
	// and strictly increasing within a conversation, starting at 1.
	Sequence    int64  `json:"sequence" db:"sequence"`
	Role        string `json:"role" db:"role"`
	ProviderKey string `json:"provider_key" db:"provider_key"`
	Model       string `json:"model" db:"model"`
	// Data is the opaque OpenAI-shaped message body (content, tool_calls,
	// tool_call_id, name, usage, error, attachments). The server never
	// interprets it beyond a size cap.
	Data      map[string]any `json:"data"`
	CreatedAt string         `json:"created_at" db:"created_at"`
}

// PlaygroundRoles are the only accepted message roles.
var PlaygroundRoles = []string{"user", "assistant", "tool"}

// ValidPlaygroundRole reports whether role is storable.
func ValidPlaygroundRole(role string) bool {
	for _, v := range PlaygroundRoles {
		if v == role {
			return true
		}
	}
	return false
}

// PlaygroundStorer deliberately does not extend the composite Storer: the
// server type-asserts it so a backend without playground support degrades to
// 503 instead of failing to satisfy Storer. Every method takes the owner as
// its first scoping argument and must filter on it; a foreign or unknown
// conversation resolves to ErrPlaygroundNotFound so existence never leaks.
type PlaygroundStorer interface {
	ListPlaygroundConversations(ctx context.Context, owner, before string, limit uint) ([]PlaygroundConversation, error)
	CreatePlaygroundConversation(ctx context.Context, c PlaygroundConversation) (*PlaygroundConversation, error)
	GetPlaygroundConversation(ctx context.Context, owner, id string) (*PlaygroundConversation, error)
	PatchPlaygroundConversation(ctx context.Context, owner, id string, patch map[string]any) (*PlaygroundConversation, error)
	DeletePlaygroundConversation(ctx context.Context, owner, id string) error
	// ForkPlaygroundConversation branches a new conversation off an existing
	// one: it copies the source settings and every message up to and
	// including fromSequence, renumbered gaplessly from 1. An empty title
	// derives one from the source. fromSequence must name a message that
	// exists in the source, otherwise ErrPlaygroundConflict.
	ForkPlaygroundConversation(ctx context.Context, owner, id string, fromSequence int64, title string) (*PlaygroundConversation, error)
	ListPlaygroundMessages(ctx context.Context, owner, id, before string, limit uint) ([]PlaygroundMessage, error)
	// AppendPlaygroundMessages assigns sequences atomically and bumps the
	// conversation's updated_at in the same transaction.
	AppendPlaygroundMessages(ctx context.Context, owner, id string, items []PlaygroundMessage) ([]PlaygroundMessage, error)
	// TruncatePlaygroundMessages removes every message at or after
	// fromSequence, which is how retry and edit rewind a transcript.
	TruncatePlaygroundMessages(ctx context.Context, owner, id string, fromSequence int64) error
}
