package service

import (
	"context"

	"github.com/rakunlabs/query"
)

// ─── Agent Budgets & Cost Tracking ───

// AgentBudget represents a spending limit for an agent within a time period.
type AgentBudget struct {
	ID           string  `json:"id"`
	AgentID      string  `json:"agent_id"`
	MonthlyLimit float64 `json:"monthly_limit"`
	CurrentSpend float64 `json:"current_spend"`
	PeriodStart  string  `json:"period_start"`
	PeriodEnd    string  `json:"period_end"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

// AgentUsageRecord represents a single cost event from an agent's LLM call.
type AgentUsageRecord struct {
	ID               string  `json:"id"`
	AgentID          string  `json:"agent_id"`
	TaskID           string  `json:"task_id,omitempty"`
	WorkflowRunID    string  `json:"workflow_run_id,omitempty"`
	SessionID        string  `json:"session_id,omitempty"`
	Model            string  `json:"model"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	EstimatedCost    float64 `json:"estimated_cost"`
	CreatedAt        string  `json:"created_at"`
}

// ModelPricing defines the cost per token for a specific provider/model combination.
type ModelPricing struct {
	ID                   string  `json:"id"`
	ProviderKey          string  `json:"provider_key"`
	Model                string  `json:"model"`
	PromptPricePer1M     float64 `json:"prompt_price_per_1m"`
	CompletionPricePer1M float64 `json:"completion_price_per_1m"`
	CreatedAt            string  `json:"created_at"`
	UpdatedAt            string  `json:"updated_at"`
}

// AgentBudgetStorer defines operations for agent budgets and cost tracking.
type AgentBudgetStorer interface {
	GetAgentBudget(ctx context.Context, agentID string) (*AgentBudget, error)
	SetAgentBudget(ctx context.Context, budget AgentBudget) error
	ListAgentBudgets(ctx context.Context) ([]AgentBudget, error)
	RecordAgentUsage(ctx context.Context, usage AgentUsageRecord) error
	GetAgentUsage(ctx context.Context, agentID string, q *query.Query) (*ListResult[AgentUsageRecord], error)
	GetAgentTotalSpend(ctx context.Context, agentID string) (float64, error)
	ListModelPricing(ctx context.Context) ([]ModelPricing, error)
	SetModelPricing(ctx context.Context, pricing ModelPricing) error
}

// ─── Audit Trail ───

// AuditEntry represents an immutable log entry for agent/system actions.
type AuditEntry struct {
	ID             string         `json:"id"`
	OrganizationID string         `json:"organization_id,omitempty"`
	ActorType      string         `json:"actor_type"`
	ActorID        string         `json:"actor_id"`
	Action         string         `json:"action"`
	ResourceType   string         `json:"resource_type"`
	ResourceID     string         `json:"resource_id"`
	Details        map[string]any `json:"details,omitempty"`
	CreatedAt      string         `json:"created_at"`
}

// AuditStorer defines operations for the immutable audit log.
type AuditStorer interface {
	RecordAudit(ctx context.Context, entry AuditEntry) error
	ListAuditEntries(ctx context.Context, q *query.Query) (*ListResult[AuditEntry], error)
	GetAuditTrail(ctx context.Context, resourceType, resourceID string) ([]AuditEntry, error)
}

// AuditPayloadMaxBytes is the per-side cap (input or output) we apply when
// folding I/O into an audit entry's Details JSON. Audit rows are stored in
// SQLite/Postgres TEXT columns and read back wholesale on the Audit page —
// a runaway tool result would otherwise turn one row into a megabyte-class
// blob. This cap is independent of the loop governor's tool-result cap;
// the governor controls what the LLM sees, this controls what we persist
// for human review.
const AuditPayloadMaxBytes = 4096

// TruncateForAudit returns s clamped to AuditPayloadMaxBytes with a clear
// "...[truncated]" suffix when it had to cut. Intended for tool inputs and
// outputs being attached to AuditEntry.Details; not for general logging.
func TruncateForAudit(s string) string {
	if len(s) <= AuditPayloadMaxBytes {
		return s
	}
	return s[:AuditPayloadMaxBytes] + "...[truncated]"
}

// ─── Cost Events ───

// CostEvent records a single LLM call cost with full attribution.
type CostEvent struct {
	ID             string  `json:"id"`
	OrganizationID string  `json:"organization_id,omitempty"`
	AgentID        string  `json:"agent_id"`
	TaskID         string  `json:"task_id,omitempty"`
	ProjectID      string  `json:"project_id,omitempty"`
	GoalID         string  `json:"goal_id,omitempty"`
	BillingCode    string  `json:"billing_code,omitempty"`
	RunID          string  `json:"run_id,omitempty"`
	Provider       string  `json:"provider"`
	Model          string  `json:"model"`
	InputTokens    int64   `json:"input_tokens"`
	OutputTokens   int64   `json:"output_tokens"`
	CostCents      float64 `json:"cost_cents"`
	// LatencyMs is the wall-clock duration of the LLM call, in milliseconds.
	// Zero for externally-ingested events that don't report latency.
	LatencyMs int64 `json:"latency_ms"`
	// Status is "ok" for successful calls, "error" for failed ones.
	Status string `json:"status"`
	// ErrorCode is a stable short tag (e.g. "rate_limit", "timeout") when Status="error".
	ErrorCode string `json:"error_code,omitempty"`
	// ErrorMessage is a truncated human-readable error description.
	ErrorMessage string `json:"error_message,omitempty"`
	CreatedAt    string `json:"created_at"`
}

// ─── Usage Dashboard Aggregations ───

// UsageFilter narrows the set of cost_events considered by usage aggregations.
// All slice fields are OR-matched within themselves and AND-matched across fields.
// Zero-value From/To means "no lower/upper bound".
type UsageFilter struct {
	From         string   // RFC3339, inclusive
	To           string   // RFC3339, exclusive
	Providers    []string // match any
	Models       []string
	AgentIDs     []string
	OrgIDs       []string
	ProjectIDs   []string
	GoalIDs      []string
	BillingCodes []string
	// Status, when non-empty, restricts to rows with this status (e.g. "ok" or "error").
	Status string
}

// UsageSummary holds a single aggregated usage row across an arbitrary filter.
// Used both for the /usage/summary endpoint (single row) and as the row shape
// returned by /usage/grouped (keyed by the requested GroupBy dimension).
type UsageSummary struct {
	Key            string  `json:"key,omitempty"`
	InputTokens    int64   `json:"input_tokens"`
	OutputTokens   int64   `json:"output_tokens"`
	TotalTokens    int64   `json:"total_tokens"`
	RequestCount   int64   `json:"request_count"`
	ErrorCount     int64   `json:"error_count"`
	CostCents      float64 `json:"cost_cents"`
	AvgLatencyMs   float64 `json:"avg_latency_ms"`
	MaxLatencyMs   int64   `json:"max_latency_ms"`
	TotalLatencyMs int64   `json:"total_latency_ms"`
	FirstEventAt   string  `json:"first_event_at,omitempty"`
	LastEventAt    string  `json:"last_event_at,omitempty"`
}

// UsageTimeSeriesPoint is one bucket in a time series.
type UsageTimeSeriesPoint struct {
	Bucket       string  `json:"bucket"` // RFC3339 timestamp at bucket start
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	RequestCount int64   `json:"request_count"`
	ErrorCount   int64   `json:"error_count"`
	CostCents    float64 `json:"cost_cents"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
}

// BudgetUtilization combines an agent's budget with its current spend.
type BudgetUtilization struct {
	AgentID      string  `json:"agent_id"`
	AgentName    string  `json:"agent_name,omitempty"`
	MonthlyLimit float64 `json:"monthly_limit"`
	CurrentSpend float64 `json:"current_spend"`
	PeriodStart  string  `json:"period_start,omitempty"`
	PeriodEnd    string  `json:"period_end,omitempty"`
	// UsagePercent is (CurrentSpend / MonthlyLimit) * 100, capped by clients for display.
	UsagePercent float64 `json:"usage_percent"`
}

// CostByTasksResult aggregates the cost rollup for a set of tasks. Used by
// the TaskDetail page to show "this pipeline cost X" without forcing the
// caller to fetch every cost_event individually.
type CostByTasksResult struct {
	CostCents    float64 `json:"cost_cents"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	EventCount   int64   `json:"event_count"`
}

// CostEventStorer defines operations for per-call cost tracking.
type CostEventStorer interface {
	RecordCostEvent(ctx context.Context, event CostEvent) error
	ListCostEvents(ctx context.Context, q *query.Query) (*ListResult[CostEvent], error)
	GetCostByAgent(ctx context.Context, agentID string) (float64, error)
	GetCostByProject(ctx context.Context, projectID string) (float64, error)
	GetCostByGoal(ctx context.Context, goalID string) (float64, error)
	GetCostByBillingCode(ctx context.Context, billingCode string) (float64, error)
	// GetCostByTasks returns the summed cost_cents across every cost_event whose
	// task_id is in the supplied set. The caller is responsible for assembling
	// the descendant set (typically root + all transitive sub-tasks) since
	// task descendant traversal lives in the task store, not the cost-event store.
	// Empty taskIDs returns 0 with no error. Also returns an aggregate event
	// count and total token usage so the UI can show "X events, Y tokens" in
	// addition to the cost.
	GetCostByTasks(ctx context.Context, taskIDs []string) (CostByTasksResult, error)

	// GetUsageSummary aggregates all matching events into a single row.
	GetUsageSummary(ctx context.Context, filter UsageFilter) (UsageSummary, error)
	// GetUsageGrouped returns one aggregated row per distinct value of groupBy.
	// Allowed groupBy values: "provider", "model", "agent", "organization",
	// "project", "goal", "billing_code", "status".
	GetUsageGrouped(ctx context.Context, filter UsageFilter, groupBy string, limit int) ([]UsageSummary, error)
	// GetUsageTimeSeries returns aggregated buckets.
	// Allowed bucket values: "hour", "day".
	GetUsageTimeSeries(ctx context.Context, filter UsageFilter, bucket string) ([]UsageTimeSeriesPoint, error)
}
