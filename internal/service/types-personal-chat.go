package service

import (
	"context"
	"errors"
	"time"
)

var ErrPersonalChatNotFound = errors.New("conversation or message not found")
var ErrPersonalChatConflict = errors.New("conversation busy or request conflict")

type PersonalConversation struct {
	ID           string    `json:"id" db:"id"`
	OwnerUserID  string    `json:"owner_user_id" db:"owner_user_id"`
	Title        string    `json:"title" db:"title"`
	ProviderKey  string    `json:"provider_key" db:"provider_key"`
	Model        string    `json:"model" db:"model"`
	SystemPrompt string    `json:"system_prompt" db:"system_prompt"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time `json:"updated_at" db:"updated_at"`
}

type PersonalChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
	ReasoningTokens  int `json:"reasoning_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type PersonalChatMessage struct {
	ID             string            `json:"id"`
	Sequence       int64             `json:"sequence"`
	ConversationID string            `json:"conversation_id"`
	RequestID      string            `json:"request_id"`
	Role           string            `json:"role"`
	Content        string            `json:"content"`
	Status         string            `json:"status"`
	ProviderKey    string            `json:"provider_key"`
	Model          string            `json:"model"`
	FinishReason   string            `json:"finish_reason"`
	Error          string            `json:"error"`
	Usage          PersonalChatUsage `json:"usage"`
	CreatedAt      time.Time         `json:"created_at"`
}

func (m PersonalChatMessage) Active() bool { return m.Status == "pending" || m.Status == "streaming" }

type PersonalChatTurn struct {
	User         PersonalChatMessage  `json:"user"`
	Assistant    PersonalChatMessage  `json:"assistant"`
	Replay       bool                 `json:"replay"`
	LeaseToken   string               `json:"-"`
	Conversation PersonalConversation `json:"-"`
}

// PersonalChatStorer deliberately does not extend the composite Storer.
// Every operation, including worker writes, is scoped to a nonempty native subject.
type PersonalChatStorer interface {
	CreatePersonalConversation(context.Context, PersonalConversation) (*PersonalConversation, error)
	ListPersonalConversations(context.Context, string, string, uint) ([]PersonalConversation, error)
	GetPersonalConversation(context.Context, string, string) (*PersonalConversation, error)
	PatchPersonalConversation(context.Context, string, string, map[string]string) (*PersonalConversation, error)
	DeletePersonalConversation(context.Context, string, string) error
	ListPersonalMessages(context.Context, string, string, string, uint) ([]PersonalChatMessage, error)
	GetPersonalMessage(context.Context, string, string, string) (*PersonalChatMessage, error)
	BeginPersonalTurn(context.Context, string, string, string, string) (*PersonalChatTurn, error)
	CheckpointPersonalTurn(context.Context, string, string, string, PersonalChatMessage) (*PersonalChatMessage, error)
	CancelPersonalTurn(context.Context, string, string, string) (*PersonalChatMessage, error)
}
