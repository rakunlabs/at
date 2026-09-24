package service

import (
	"context"
	"errors"
)

const (
	ChatShareMaxMessages = 2000
	ChatShareMaxBytes    = 8 << 20
)

var (
	ErrChatShareNotFound = errors.New("chat share not found")
	ErrChatShareConflict = errors.New("chat share conflict")
	ErrChatShareTooLarge = errors.New("chat share too large")
)

type ChatShareOptions struct {
	IncludeSystemPrompt bool `json:"include_system_prompt"`
	IncludeToolOutputs  bool `json:"include_tool_outputs"`
	IncludeAttachments  bool `json:"include_attachments"`
}

type ChatSharePayload struct {
	Title        string              `json:"title"`
	SystemPrompt string              `json:"system_prompt,omitempty"`
	ProviderKey  string              `json:"provider_key,omitempty"`
	Model        string              `json:"model,omitempty"`
	Messages     []PlaygroundMessage `json:"messages"`
}

type ChatShare struct {
	ID                   string           `json:"id" db:"id"`
	WorkspaceID          string           `json:"workspace_id" db:"workspace_id"`
	SourceConversationID string           `json:"source_conversation_id,omitempty" db:"source_conversation_id"`
	SourceOwnerUserID    string           `json:"-" db:"source_owner_user_id"`
	ThroughSequence      int64            `json:"through_sequence" db:"through_sequence"`
	CurrentVersion       int64            `json:"version" db:"current_version"`
	RevokedAt            string           `json:"revoked_at,omitempty" db:"revoked_at"`
	CreatedAt            string           `json:"created_at" db:"created_at"`
	UpdatedAt            string           `json:"updated_at" db:"updated_at"`
	Payload              ChatSharePayload `json:"payload"`
	Options              ChatShareOptions `json:"options"`
}

type ChatShareStorer interface {
	ReadPlaygroundPrefix(context.Context, string, string, int64) (*PlaygroundConversation, []PlaygroundMessage, error)
	CreateChatShare(context.Context, ChatShare, []string) (*ChatShare, error)
	UpdateChatShare(context.Context, ChatShare, []string) (*ChatShare, error)
	GetChatShare(context.Context, string) (*ChatShare, error)
	GetChatShareBySource(context.Context, string, string, string) (*ChatShare, error)
	RevokeChatShare(context.Context, string, string) ([]MediaObject, error)
	RevokeChatSharesForSource(context.Context, string, string) ([]MediaObject, error)
	ImportChatShare(context.Context, string, int64, string, string, string, string, map[string]string) (*PlaygroundConversation, error)
}
