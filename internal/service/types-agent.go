package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rakunlabs/query"
)

// SkillRef references a skill attached to an agent. It behaves like a plain
// string (the skill's name or ID) for backward compatibility but can also
// carry a Connections map that overrides the agent's default connection
// bindings for the duration of this skill's tool handlers.
//
// JSON encoding:
//   - Simple reference:    "youtube_publish"
//   - With overrides:      {"id": "youtube_publish", "connections": {"youtube": "conn_01HV..."}}
//
// Both shapes round-trip cleanly: a bare string unmarshals into {ID, nil},
// and a SkillRef with no Connections marshals back into a bare string.
type SkillRef struct {
	ID          string            `json:"id"`
	Connections map[string]string `json:"connections,omitempty"` // provider -> connection_id
}

// MarshalJSON emits a bare string when no connection overrides are set, so
// existing agent configs and downstream consumers that expect a []string-ish
// shape keep seeing the old format.
func (r SkillRef) MarshalJSON() ([]byte, error) {
	if len(r.Connections) == 0 {
		return json.Marshal(r.ID)
	}
	type alias SkillRef
	return json.Marshal(alias(r))
}

// UnmarshalJSON accepts either a bare string or a {id, connections} object.
func (r *SkillRef) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*r = SkillRef{}
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*r = SkillRef{ID: s}
		return nil
	}
	type alias SkillRef
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return fmt.Errorf("skill ref: %w", err)
	}
	*r = SkillRef(a)
	return nil
}

// SkillRefsFromStrings wraps a list of skill names/IDs as SkillRefs with no
// connection overrides. Used by code paths that still deal with plain strings.
func SkillRefsFromStrings(ss []string) []SkillRef {
	if ss == nil {
		return nil
	}
	out := make([]SkillRef, len(ss))
	for i, s := range ss {
		out[i] = SkillRef{ID: s}
	}
	return out
}

// StringsFromSkillRefs returns the bare skill identifiers from a slice of
// SkillRefs, discarding any connection overrides. Used by export paths and
// by code that only needs to look up the skill definition.
func StringsFromSkillRefs(refs []SkillRef) []string {
	if refs == nil {
		return nil
	}
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.ID
	}
	return out
}

// ─── Agent Registry ───

// Agent status constants for lifecycle management.
const (
	AgentStatusActive     = "active"
	AgentStatusPaused     = "paused"
	AgentStatusTerminated = "terminated"
)

// AgentConfig holds the configuration fields for an agent, stored as JSON in the database.
type AgentConfig struct {
	Description  string     `json:"description,omitempty"`
	Provider     string     `json:"provider"`                // Provider key
	Model        string     `json:"model,omitempty"`         // Model identifier
	SystemPrompt string     `json:"system_prompt,omitempty"` // System prompt
	Skills       []SkillRef `json:"skills,omitempty"`        // Skill attachments (bare string or {id, connections})
	MCPs         []string   `json:"mcp_urls,omitempty"`      // List of MCP server URLs (legacy)
	MCPSets      []string   `json:"mcp_sets,omitempty"`      // List of MCP Set names (internal MCPs)
	// Workflows lists workflow NAMES exposed to the agent as callable tools.
	// Each workflow becomes one tool named `workflow_<name>` in the agentic
	// loop. Agents can also reach workflows indirectly via MCP sets; this
	// field is the explicit, portable attachment — export/import move agents
	// and their workflows together without relying on MCP-set indirection.
	Workflows                 []string `json:"workflows,omitempty"`
	BuiltinTools              []string `json:"builtin_tools,omitempty"`               // Enabled builtin tool names
	MaxIterations             int      `json:"max_iterations"`                        // Max iterations for the loop
	ToolTimeout               int      `json:"tool_timeout"`                          // Timeout in seconds
	ConfirmationRequiredTools []string `json:"confirmation_required_tools,omitempty"` // Tools that require human confirmation before execution
	AvatarSeed                string   `json:"avatar_seed,omitempty"`                 // Seed for deterministic avatar generation (defaults to agent name when empty)

	// Connections maps a provider name (e.g. "youtube", "google") to a
	// connection ID. Tool handlers that request variables bound to a provider
	// (e.g. getVar("youtube_refresh_token")) resolve through this map before
	// falling back to global variables. Per-skill overrides live on
	// SkillRef.Connections and take priority over this map.
	Connections map[string]string `json:"connections,omitempty"`

	// NOTE: Organizational fields (role, title, parent_agent_id, organization_id,
	// status, delegation_rules, heartbeat_schedule) live on the OrganizationAgent
	// join table so that agents can belong to multiple organizations with per-org metadata.
}

// Agent represents a reusable agent configuration that can be referenced
// by agent_call nodes in workflows.
type Agent struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Config    AgentConfig `json:"config"`
	CreatedAt string      `json:"created_at"`
	UpdatedAt string      `json:"updated_at"`
	CreatedBy string      `json:"created_by"`
	UpdatedBy string      `json:"updated_by"`
}

// AgentStorer defines CRUD operations for agents.
type AgentStorer interface {
	ListAgents(ctx context.Context, q *query.Query) (*ListResult[Agent], error)
	GetAgent(ctx context.Context, id string) (*Agent, error)
	CreateAgent(ctx context.Context, agent Agent) (*Agent, error)
	UpdateAgent(ctx context.Context, id string, agent Agent) (*Agent, error)
	DeleteAgent(ctx context.Context, id string) error
}

// ─── Agent Heartbeats ───

// AgentHeartbeat tracks the last heartbeat for an agent.
type AgentHeartbeat struct {
	AgentID         string         `json:"agent_id"`
	Status          string         `json:"status"` // "healthy", "stale", "unresponsive"
	LastHeartbeatAt string         `json:"last_heartbeat_at"`
	Metadata        map[string]any `json:"metadata,omitempty"` // free-form agent state
	UpdatedAt       string         `json:"updated_at"`
}

// AgentHeartbeatStorer defines operations for agent heartbeat tracking.
type AgentHeartbeatStorer interface {
	RecordHeartbeat(ctx context.Context, agentID string, metadata map[string]any) error
	GetHeartbeat(ctx context.Context, agentID string) (*AgentHeartbeat, error)
	ListHeartbeats(ctx context.Context) ([]AgentHeartbeat, error)
	MarkStale(ctx context.Context, threshold time.Duration) (int, error)
}

// ─── Heartbeat Runs (Execution Tracking) ───

// HeartbeatRun invocation source constants.
const (
	InvocationTimer      = "timer"
	InvocationAssignment = "assignment"
	InvocationOnDemand   = "on_demand"
	InvocationAutomation = "automation"
)

// HeartbeatRun status constants.
const (
	RunStatusQueued    = "queued"
	RunStatusRunning   = "running"
	RunStatusSucceeded = "succeeded"
	RunStatusFailed    = "failed"
	RunStatusCancelled = "cancelled"
	RunStatusTimedOut  = "timed_out"
)

// HeartbeatRun represents a single execution run for an agent's heartbeat.
type HeartbeatRun struct {
	ID               string         `json:"id"`
	AgentID          string         `json:"agent_id"`
	OrganizationID   string         `json:"organization_id,omitempty"`
	InvocationSource string         `json:"invocation_source"`
	TriggerDetail    string         `json:"trigger_detail,omitempty"`
	Status           string         `json:"status"`
	ContextSnapshot  map[string]any `json:"context_snapshot,omitempty"`
	UsageJSON        map[string]any `json:"usage_json,omitempty"`
	ResultJSON       map[string]any `json:"result_json,omitempty"`
	LogRef           string         `json:"log_ref,omitempty"`
	LogBytes         int64          `json:"log_bytes,omitempty"`
	LogSHA256        string         `json:"log_sha256,omitempty"`
	StdoutExcerpt    string         `json:"stdout_excerpt,omitempty"`
	StderrExcerpt    string         `json:"stderr_excerpt,omitempty"`
	SessionIDBefore  string         `json:"session_id_before,omitempty"`
	SessionIDAfter   string         `json:"session_id_after,omitempty"`
	StartedAt        string         `json:"started_at,omitempty"`
	FinishedAt       string         `json:"finished_at,omitempty"`
	CreatedAt        string         `json:"created_at"`
}

// HeartbeatRunStorer defines operations for heartbeat run tracking.
type HeartbeatRunStorer interface {
	CreateHeartbeatRun(ctx context.Context, run HeartbeatRun) (*HeartbeatRun, error)
	GetHeartbeatRun(ctx context.Context, id string) (*HeartbeatRun, error)
	UpdateHeartbeatRun(ctx context.Context, id string, run HeartbeatRun) (*HeartbeatRun, error)
	ListHeartbeatRuns(ctx context.Context, agentID string, q *query.Query) (*ListResult[HeartbeatRun], error)
	GetActiveRun(ctx context.Context, agentID string) (*HeartbeatRun, error)
}

// ─── Wakeup Requests ───

// WakeupRequest status constants.
const (
	WakeupStatusPending                = "pending"
	WakeupStatusDispatched             = "dispatched"
	WakeupStatusDeferredIssueExecution = "deferred_issue_execution"
	WakeupStatusCancelled              = "cancelled"
)

// WakeupRequest represents a request to wake up an agent.
type WakeupRequest struct {
	ID             string         `json:"id"`
	AgentID        string         `json:"agent_id"`
	OrganizationID string         `json:"organization_id,omitempty"`
	Status         string         `json:"status"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	Context        map[string]any `json:"context,omitempty"`
	CoalescedCount int            `json:"coalesced_count"`
	RunID          string         `json:"run_id,omitempty"`
	CreatedAt      string         `json:"created_at"`
	UpdatedAt      string         `json:"updated_at"`
}

// WakeupRequestStorer defines operations for wakeup requests with coalescing.
type WakeupRequestStorer interface {
	CreateOrCoalesce(ctx context.Context, req WakeupRequest) (*WakeupRequest, error)
	GetWakeupRequest(ctx context.Context, id string) (*WakeupRequest, error)
	ListPendingForAgent(ctx context.Context, agentID string) ([]WakeupRequest, error)
	MarkDispatched(ctx context.Context, id, runID string) error
	PromoteDeferred(ctx context.Context, agentID string) error
}

// ─── Agent Runtime State ───

// AgentRuntimeState represents persistent per-agent runtime state (1:1 with agent).
type AgentRuntimeState struct {
	AgentID           string         `json:"agent_id"`
	SessionID         string         `json:"session_id,omitempty"`
	StateJSON         map[string]any `json:"state_json,omitempty"`
	TotalInputTokens  int64          `json:"total_input_tokens"`
	TotalOutputTokens int64          `json:"total_output_tokens"`
	TotalCostCents    int64          `json:"total_cost_cents"`
	LastRunID         string         `json:"last_run_id,omitempty"`
	LastRunStatus     string         `json:"last_run_status,omitempty"`
	LastError         string         `json:"last_error,omitempty"`
	UpdatedAt         string         `json:"updated_at"`
}

// AgentRuntimeStateStorer defines operations for persistent agent runtime state.
type AgentRuntimeStateStorer interface {
	GetAgentRuntimeState(ctx context.Context, agentID string) (*AgentRuntimeState, error)
	UpsertAgentRuntimeState(ctx context.Context, state AgentRuntimeState) error
	AccumulateUsage(ctx context.Context, agentID string, inputTokens, outputTokens, costCents int64) error
}

// ─── Agent Task Sessions ───

// AgentTaskSession represents a per-agent, per-task session.
type AgentTaskSession struct {
	ID                string         `json:"id"`
	AgentID           string         `json:"agent_id"`
	TaskKey           string         `json:"task_key"`
	AdapterType       string         `json:"adapter_type,omitempty"`
	SessionParamsJSON map[string]any `json:"session_params_json,omitempty"`
	SessionDisplayID  string         `json:"session_display_id,omitempty"`
	CreatedAt         string         `json:"created_at"`
	UpdatedAt         string         `json:"updated_at"`
}

// AgentTaskSessionStorer defines operations for per-task agent sessions.
type AgentTaskSessionStorer interface {
	GetAgentTaskSession(ctx context.Context, agentID, taskKey string) (*AgentTaskSession, error)
	UpsertAgentTaskSession(ctx context.Context, session AgentTaskSession) error
	DeleteAgentTaskSession(ctx context.Context, agentID, taskKey string) error
	ListAgentTaskSessions(ctx context.Context, agentID string) ([]AgentTaskSession, error)
}

// ─── Agent Config Revisions ───

// AgentConfigRevision captures a full before/after snapshot of an agent config change.
type AgentConfigRevision struct {
	ID           string      `json:"id"`
	AgentID      string      `json:"agent_id"`
	Version      int         `json:"version"`
	ConfigBefore AgentConfig `json:"config_before"`
	ConfigAfter  AgentConfig `json:"config_after"`
	ChangedBy    string      `json:"changed_by"`
	ChangeNote   string      `json:"change_note,omitempty"`
	CreatedAt    string      `json:"created_at"`
}

// AgentConfigRevisionStorer defines operations for agent config revision tracking.
type AgentConfigRevisionStorer interface {
	CreateRevision(ctx context.Context, rev AgentConfigRevision) (*AgentConfigRevision, error)
	ListRevisions(ctx context.Context, agentID string) ([]AgentConfigRevision, error)
	GetRevision(ctx context.Context, id string) (*AgentConfigRevision, error)
	GetLatestRevision(ctx context.Context, agentID string) (*AgentConfigRevision, error)
}

// ─── Agent Memory ───

// AgentMemory represents a persistent memory entry for an agent within an organization.
type AgentMemory struct {
	ID             string   `json:"id"`
	AgentID        string   `json:"agent_id"`
	OrganizationID string   `json:"organization_id"`
	TaskID         string   `json:"task_id"`
	TaskIdentifier string   `json:"task_identifier,omitempty"`
	SummaryL0      string   `json:"summary_l0"`
	SummaryL1      string   `json:"summary_l1"`
	Tags           []string `json:"tags,omitempty"`
	CreatedAt      string   `json:"created_at"`
}

// AgentMemoryMessages stores the full L2 conversation for a memory entry.
type AgentMemoryMessages struct {
	MemoryID string    `json:"memory_id"`
	Messages []Message `json:"messages"`
}

// AgentMemoryStorer defines operations for persistent agent memory.
type AgentMemoryStorer interface {
	CreateAgentMemory(ctx context.Context, mem AgentMemory) (*AgentMemory, error)
	GetAgentMemory(ctx context.Context, id string) (*AgentMemory, error)
	ListAgentMemories(ctx context.Context, agentID, orgID string) ([]AgentMemory, error)
	ListOrgMemories(ctx context.Context, orgID string) ([]AgentMemory, error)
	SearchAgentMemories(ctx context.Context, agentID, orgID, query string) ([]AgentMemory, error)
	DeleteAgentMemory(ctx context.Context, id string) error
	GetAgentMemoryMessages(ctx context.Context, memoryID string) (*AgentMemoryMessages, error)
	CreateAgentMemoryMessages(ctx context.Context, msgs AgentMemoryMessages) error
}
