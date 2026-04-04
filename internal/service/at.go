// Package service defines the core domain types and store interfaces for the AT platform.
//
// Types are organized across domain-specific files:
//
//   - types_llm.go     — LLM provider interfaces, message types, chat options
//   - types_media.go   — Image, audio, embedding provider interfaces and types
//   - types_provider.go — Provider record, storer, key rotation interfaces
//   - types_token.go   — API token and token usage types
//   - types_workflow.go — Workflow, trigger, and node config types
//   - types_agent.go   — Agent, heartbeat, runtime state, memory types
//   - types_org.go     — Organization, org-agent membership, goal, project types
//   - types_task.go    — Task, issue comment, label, approval types
//   - types_budget.go  — Agent budget, cost event, audit types
//   - types_chat.go    — Chat session, bot config, user preference, marketplace types
//   - types_rag.go     — RAG collection, state, page types
//   - types_mcp.go     — MCP server, MCP set types
package service

import (
	"context"

	"github.com/rakunlabs/query"
)

// ListMeta contains pagination metadata.
type ListMeta struct {
	Total  uint64 `json:"total,omitempty"`
	Offset uint64 `json:"offset,omitempty"`
	Limit  uint64 `json:"limit,omitempty"`
}

// ListResult is a generic paginated response.
type ListResult[T any] struct {
	Data []T      `json:"data"`
	Meta ListMeta `json:"meta"`
}

// Storer is the composite interface aggregating all domain store interfaces.
// Store backends (postgres, sqlite3, memory) implement this interface.
type Storer interface {
	ProviderStorer
	APITokenStorer
	TokenUsageStorer
	WorkflowStorer
	WorkflowVersionStorer
	TriggerStorer
	SkillStorer
	VariableStorer
	NodeConfigStorer
	AgentStorer
	ChatSessionStorer
	RAGCollectionStorer
	RAGStateStorer
	RAGPageStorer
	MCPServerStorer
	MCPSetStorer
	BotConfigStorer
	MarketplaceSourceStorer
	UserPreferenceStorer
	OrganizationStorer
	GoalStorer
	TaskStorer
	AgentBudgetStorer
	AuditStorer
	AgentHeartbeatStorer
	ProjectStorer
	IssueCommentStorer
	LabelStorer
	HeartbeatRunStorer
	WakeupRequestStorer
	AgentRuntimeStateStorer
	AgentTaskSessionStorer
	ApprovalStorer
	AgentConfigRevisionStorer
	CostEventStorer
	OrganizationAgentStorer
	AgentMemoryStorer
}

// ─── Skill Management ───

// Skill represents a reusable skill that bundles a system prompt fragment
// and a set of tools. Skills can be attached to agent_call workflow nodes
// to provide the agent with domain-specific capabilities.
type Skill struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	SystemPrompt string `json:"system_prompt"` // Prompt fragment appended to the agent's system prompt
	Tools        []Tool `json:"tools"`         // Built-in tool definitions (may include JS handlers)
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	CreatedBy    string `json:"created_by"`
	UpdatedBy    string `json:"updated_by"`
}

// SkillStorer defines CRUD operations for skill definitions.
type SkillStorer interface {
	ListSkills(ctx context.Context, q *query.Query) (*ListResult[Skill], error)
	GetSkill(ctx context.Context, id string) (*Skill, error)
	GetSkillByName(ctx context.Context, name string) (*Skill, error)
	CreateSkill(ctx context.Context, s Skill) (*Skill, error)
	UpdateSkill(ctx context.Context, id string, s Skill) (*Skill, error)
	DeleteSkill(ctx context.Context, id string) error
}

// ─── Variable Management ───

// Variable represents a key-value variable stored in the database.
// Variables can be secret (encrypted at rest, redacted in list responses)
// or non-secret (stored as plaintext, shown in list responses).
// Accessed from workflow JS handlers via getVar() and bash handlers via $VAR_<KEY>.
type Variable struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Value       string `json:"value"`
	Description string `json:"description"`
	Secret      bool   `json:"secret"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	CreatedBy   string `json:"created_by"`
	UpdatedBy   string `json:"updated_by"`
}

// VariableStorer defines CRUD operations for variables.
type VariableStorer interface {
	ListVariables(ctx context.Context, q *query.Query) (*ListResult[Variable], error)
	GetVariable(ctx context.Context, id string) (*Variable, error)
	GetVariableByKey(ctx context.Context, key string) (*Variable, error)
	CreateVariable(ctx context.Context, v Variable) (*Variable, error)
	UpdateVariable(ctx context.Context, id string, v Variable) (*Variable, error)
	DeleteVariable(ctx context.Context, id string) error
}
