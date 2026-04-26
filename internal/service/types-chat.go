package service

import (
	"context"
	"encoding/json"

	"github.com/rakunlabs/query"
)

// ─── Chat Sessions ───

// ChatSessionConfig holds extensible session metadata.
type ChatSessionConfig struct {
	Platform          string `json:"platform,omitempty"`
	PlatformUserID    string `json:"platform_user_id,omitempty"`
	PlatformChannelID string `json:"platform_channel_id,omitempty"`
	// BotConfigID scopes a bot-driven session to the specific BotConfig
	// that received the message. Without this, two bots talking to the
	// same Telegram/Discord chat would share a single session row.
	BotConfigID string `json:"bot_config_id,omitempty"`
}

// ChatSession represents a persistent chat session tied to an agent.
type ChatSession struct {
	ID             string            `json:"id"`
	AgentID        string            `json:"agent_id"`
	TaskID         string            `json:"task_id,omitempty"`
	OrganizationID string            `json:"organization_id,omitempty"`
	Name           string            `json:"name"`
	Config         ChatSessionConfig `json:"config"`
	CreatedAt      string            `json:"created_at"`
	UpdatedAt      string            `json:"updated_at"`
	CreatedBy      string            `json:"created_by"`
	UpdatedBy      string            `json:"updated_by"`
}

// ChatMessageData holds the extensible payload of a chat message.
type ChatMessageData struct {
	Content    any    `json:"content"`                // string or []ContentBlock
	ToolCalls  any    `json:"tool_calls,omitempty"`   // []ToolCall for assistant messages
	ToolCallID string `json:"tool_call_id,omitempty"` // for tool result messages
}

// ChatMessage represents a single message in a chat session.
type ChatMessage struct {
	ID        string          `json:"id"`
	SessionID string          `json:"session_id"`
	Role      string          `json:"role"` // "user", "assistant", "system", "tool"
	Data      ChatMessageData `json:"data"`
	CreatedAt string          `json:"created_at"`
}

// ChatSessionStorer defines CRUD operations for chat sessions and messages.
type ChatSessionStorer interface {
	ListChatSessions(ctx context.Context, q *query.Query) (*ListResult[ChatSession], error)
	GetChatSession(ctx context.Context, id string) (*ChatSession, error)
	GetChatSessionByPlatform(ctx context.Context, platform, platformUserID, platformChannelID, botConfigID string) (*ChatSession, error)
	GetChatSessionByTaskID(ctx context.Context, taskID string) (*ChatSession, error)
	CreateChatSession(ctx context.Context, session ChatSession) (*ChatSession, error)
	UpdateChatSession(ctx context.Context, id string, session ChatSession) (*ChatSession, error)
	DeleteChatSession(ctx context.Context, id string) error
	ListChatMessages(ctx context.Context, sessionID string) ([]ChatMessage, error)
	CreateChatMessage(ctx context.Context, msg ChatMessage) (*ChatMessage, error)
	CreateChatMessages(ctx context.Context, msgs []ChatMessage) error
	DeleteChatMessages(ctx context.Context, sessionID string) error
}

// ─── Bot Configs ───

// BotConfig represents a Discord or Telegram bot configuration stored in the database.
type BotConfig struct {
	ID              string            `json:"id"`
	Platform        string            `json:"platform"`
	Name            string            `json:"name"`
	Token           string            `json:"token"`
	DefaultAgentID  string            `json:"default_agent_id"`
	ChannelAgents   map[string]string `json:"channel_agents,omitempty"`
	AllowedAgentIDs []string          `json:"allowed_agent_ids,omitempty"`
	AccessMode      string            `json:"access_mode"`
	PendingApproval bool              `json:"pending_approval"`
	AllowedUsers    []string          `json:"allowed_users"`
	PendingUsers    []string          `json:"pending_users"`
	Enabled         bool              `json:"enabled"`
	UserContainers  bool              `json:"user_containers,omitempty"`
	ContainerImage  string            `json:"container_image,omitempty"`
	ContainerCPU    string            `json:"container_cpu,omitempty"`
	ContainerMemory string            `json:"container_memory,omitempty"`
	SpeechToText    string            `json:"speech_to_text,omitempty"`
	WhisperModel    string            `json:"whisper_model,omitempty"`
	CreatedAt       string            `json:"created_at"`
	UpdatedAt       string            `json:"updated_at"`
	CreatedBy       string            `json:"created_by"`
	UpdatedBy       string            `json:"updated_by"`
}

// BotConfigStorer defines CRUD operations for bot configurations.
type BotConfigStorer interface {
	ListBotConfigs(ctx context.Context, q *query.Query) (*ListResult[BotConfig], error)
	GetBotConfig(ctx context.Context, id string) (*BotConfig, error)
	CreateBotConfig(ctx context.Context, bot BotConfig) (*BotConfig, error)
	UpdateBotConfig(ctx context.Context, id string, bot BotConfig) (*BotConfig, error)
	DeleteBotConfig(ctx context.Context, id string) error
}

// ─── User Preferences ───

// UserPreference stores a per-user key-value preference.
type UserPreference struct {
	ID        string          `json:"id"`
	UserID    string          `json:"user_id"`
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	Secret    bool            `json:"secret"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

// UserPreferenceStorer defines operations for per-user preferences.
type UserPreferenceStorer interface {
	ListUserPreferences(ctx context.Context, userID string) ([]UserPreference, error)
	GetUserPreference(ctx context.Context, userID, key string) (*UserPreference, error)
	SetUserPreference(ctx context.Context, pref UserPreference) error
	DeleteUserPreference(ctx context.Context, userID, key string) error
}

// ─── Marketplace Sources ───

// MarketplaceSource represents a configurable skill marketplace source.
type MarketplaceSource struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	SearchURL string `json:"search_url"`
	TopURL    string `json:"top_url"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// MarketplaceSourceStorer defines CRUD operations for marketplace source configurations.
type MarketplaceSourceStorer interface {
	ListMarketplaceSources(ctx context.Context) ([]MarketplaceSource, error)
	GetMarketplaceSource(ctx context.Context, id string) (*MarketplaceSource, error)
	CreateMarketplaceSource(ctx context.Context, src MarketplaceSource) (*MarketplaceSource, error)
	UpdateMarketplaceSource(ctx context.Context, id string, src MarketplaceSource) (*MarketplaceSource, error)
	DeleteMarketplaceSource(ctx context.Context, id string) error
}
