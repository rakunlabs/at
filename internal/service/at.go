// Package service defines the core domain types and store interfaces for the AT platform.
//
// Types are organized across domain-specific files:
//
//   - types_llm.go     — LLM provider interfaces, message types, chat options
//   - types_media.go   — Image, audio, embedding provider interfaces and types
//   - types_provider.go — Provider record, storer, key rotation interfaces
//   - types_token.go   — API token and token usage types
//   - types_workflow.go — Workflow, trigger, and node config types
//   - types_agent.go   — Agent, heartbeat, runtime state, config revision types
//   - types_org.go     — Organization, org-agent membership, goal, project types
//   - types_task.go    — Task, issue comment, label, approval types
//   - types_budget.go  — Agent budget, cost event, audit types
//   - types_chat.go    — Chat session, bot config, user preference, marketplace types
//   - types_mcp.go     — MCP server, MCP set types
//   - types_feature.go — runtime feature toggles
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rakunlabs/query"
)

// ListMeta contains pagination metadata.
type ListMeta struct {
	Total  uint64 `json:"total"`
	Offset uint64 `json:"offset"`
	Limit  uint64 `json:"limit"`
}

// ListResult is a generic paginated response.
type ListResult[T any] struct {
	Data []T      `json:"data"`
	Meta ListMeta `json:"meta"`
}

// MarshalJSON keeps an empty collection iterable, including when a store returns
// a zero-value result. A missing individual resource still has its own 404 path.
func (r ListResult[T]) MarshalJSON() ([]byte, error) {
	type result ListResult[T]
	if r.Data == nil {
		r.Data = []T{}
	}
	return json.Marshal(result(r))
}

// Storer is the composite interface aggregating all domain store interfaces.
// Store backends (postgres) implement this interface.
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
	MCPServerStorer
	MCPSetStorer
	BotConfigStorer
	MarketplaceStorer
	MarketplaceSourceStorer
	UserPreferenceStorer
	WorkspaceChatPresetStorer
	OrganizationStorer
	GoalStorer
	TaskStorer
	AgentBudgetStorer
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
	PackSourceStorer
	GuideStorer
	ConnectionStorer
	ConnectorStorer
	RoutingProfileStorer
	FeatureSettingStorer
	AgentRuntimeSettingsStorer
	LLMCallStorer
}

// Marketplace groups Skills and MCP Servers into one Claude Code
// plugin marketplace export.
type Marketplace struct {
	ID               string                 `json:"id"`
	Name             string                 `json:"name"`
	Description      string                 `json:"description"`
	Skills           []string               `json:"skills"`
	MCPServers       []string               `json:"mcp_servers"`
	DirectMCPServers []MarketplaceMCPServer `json:"direct_mcp_servers"`
	CreatedAt        string                 `json:"created_at"`
	UpdatedAt        string                 `json:"updated_at"`
	CreatedBy        string                 `json:"created_by"`
	UpdatedBy        string                 `json:"updated_by"`
}

// MarketplaceMCPServer is an MCP server config embedded directly in a Claude
// marketplace plugin so the client runs/connects it without going through AT.
type MarketplaceMCPServer struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Type        string            `json:"type,omitempty"`
	URL         string            `json:"url,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
}

// MarketplaceStorer defines CRUD operations for Claude marketplace exports.
type MarketplaceStorer interface {
	ListMarketplaces(ctx context.Context, q *query.Query) (*ListResult[Marketplace], error)
	GetMarketplace(ctx context.Context, id string) (*Marketplace, error)
	GetMarketplaceByName(ctx context.Context, name string) (*Marketplace, error)
	CreateMarketplace(ctx context.Context, m Marketplace) (*Marketplace, error)
	UpdateMarketplace(ctx context.Context, id string, m Marketplace) (*Marketplace, error)
	DeleteMarketplace(ctx context.Context, id string) error
}

// ─── Skill Management ───

// Skill represents reusable Markdown instructions and bundled text resources.
// Executable capabilities belong to agents, MCP sets, workflows, and built-in
// tools; loading a skill never grants or registers a new tool.
type Skill struct {
	WorkspaceID  string          `json:"workspace_id"`
	OwnerUserID  string          `json:"owner_user_id,omitempty"`
	Scope        string          `json:"scope"`
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	Category     string          `json:"category,omitempty"`
	Tags         []string        `json:"tags,omitempty"`
	SystemPrompt string          `json:"system_prompt"`       // Prompt fragment appended to the agent's system prompt
	Tools        []Tool          `json:"tools,omitempty"`     // Deprecated import compatibility; normalized into Markdown before persistence.
	Resources    []SkillResource `json:"resources,omitempty"` // Text files bundled beside SKILL.md
	// Claude-compatible execution metadata. Context="fork" runs the skill in
	// the named agent's isolated context instead of injecting it into the caller.
	Context string `json:"context,omitempty"`
	Agent   string `json:"agent,omitempty"`

	// Sharing / provenance metadata. Round-trips through export/import so
	// other agent platforms (Claude Code plugins, agentskills consumers,
	// other AT instances) keep attribution and can detect updates.
	Version            string `json:"version,omitempty"`              // Semver-ish version string declared by the author
	Author             string `json:"author,omitempty"`               // Attribution carried through export/import
	License            string `json:"license,omitempty"`              // SPDX-style license identifier
	SourceURL          string `json:"source_url,omitempty"`           // Imported source or reserved built-in template identity (empty for local skills)
	SourceType         string `json:"source_type,omitempty"`          // "url" for one file, "git" for a repository package
	SourceRef          string `json:"source_ref,omitempty"`           // Git branch or tag used for repository imports
	SourcePath         string `json:"source_path,omitempty"`          // Skill directory inside a repository
	SourceCredentialID string `json:"source_credential_id,omitempty"` // Encrypted Git credential used for updates
	SourceChecksum     string `json:"source_checksum,omitempty"`      // SHA-256 of imported source or last applied template-managed payload

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	CreatedBy string `json:"created_by"`
	UpdatedBy string `json:"updated_by"`
}

func ValidateSkillExecution(skill Skill) error {
	switch skill.Context {
	case "":
		if skill.Agent != "" {
			return fmt.Errorf("skill agent requires context \"fork\"")
		}
	case "fork":
		if strings.TrimSpace(skill.Agent) == "" {
			return fmt.Errorf("forked skill requires an agent")
		}
	default:
		return fmt.Errorf("unsupported skill context %q", skill.Context)
	}
	return nil
}

// SkillResource is a UTF-8 text file bundled with a SKILL.md package. Paths
// are slash-separated and relative to the directory containing SKILL.md.
type SkillResource struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	MediaType string `json:"media_type,omitempty"`
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

type SkillPublisher interface {
	PublishSkillToWorkspace(ctx context.Context, id, by string) (*Skill, error)
}

// ─── Pack Source Management ───

// PackSource represents a Git repository registered as a source of integration packs.
type PackSource struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Branch    string `json:"branch"`
	Status    string `json:"status"` // "pending", "synced", "error"
	LastSync  string `json:"last_sync,omitempty"`
	Error     string `json:"error,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// PackSourceStorer defines CRUD operations for pack sources.
type PackSourceStorer interface {
	ListPackSources(ctx context.Context, q *query.Query) (*ListResult[PackSource], error)
	GetPackSource(ctx context.Context, id string) (*PackSource, error)
	CreatePackSource(ctx context.Context, ps PackSource) (*PackSource, error)
	UpdatePackSource(ctx context.Context, id string, ps PackSource) (*PackSource, error)
	DeletePackSource(ctx context.Context, id string) error
}

// ─── Variable Management ───

// Variable represents a key-value variable stored in the database.
// Variables can be secret (encrypted at rest, redacted in list responses)
// or non-secret (stored as plaintext, shown in list responses).
// Non-secret values may be exposed to workflow handlers. Secret values resolve
// only through approved typed references at controlled tool boundaries.
type Variable struct {
	WorkspaceID  string   `json:"workspace_id"`
	ID           string   `json:"id"`
	Key          string   `json:"key"`
	Value        string   `json:"value"`
	Description  string   `json:"description"`
	Secret       bool     `json:"secret"`
	AllowedTools []string `json:"allowed_tools,omitempty"`
	AllowedHosts []string `json:"allowed_hosts,omitempty"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
	CreatedBy    string   `json:"created_by"`
	UpdatedBy    string   `json:"updated_by"`
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

// ─── Guide Management ───

// Guide represents a user-authored documentation guide. Content is stored
// as raw markdown and rendered client-side. Built-in guides are hardcoded
// in the UI; user guides live in the database alongside them.
type Guide struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Icon        string `json:"icon"`    // lucide-svelte icon name (e.g. "BookOpen")
	Content     string `json:"content"` // raw markdown
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	CreatedBy   string `json:"created_by"`
	UpdatedBy   string `json:"updated_by"`
}

// GuideStorer defines CRUD operations for user-authored guides.
type GuideStorer interface {
	ListGuides(ctx context.Context, q *query.Query) (*ListResult[Guide], error)
	GetGuide(ctx context.Context, id string) (*Guide, error)
	CreateGuide(ctx context.Context, g Guide) (*Guide, error)
	UpdateGuide(ctx context.Context, id string, g Guide) (*Guide, error)
	DeleteGuide(ctx context.Context, id string) error
}
