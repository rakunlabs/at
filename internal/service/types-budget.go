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
	CreatedAt      string  `json:"created_at"`
}

// CostEventStorer defines operations for per-call cost tracking.
type CostEventStorer interface {
	RecordCostEvent(ctx context.Context, event CostEvent) error
	ListCostEvents(ctx context.Context, q *query.Query) (*ListResult[CostEvent], error)
	GetCostByAgent(ctx context.Context, agentID string) (float64, error)
	GetCostByProject(ctx context.Context, projectID string) (float64, error)
	GetCostByGoal(ctx context.Context, goalID string) (float64, error)
	GetCostByBillingCode(ctx context.Context, billingCode string) (float64, error)
}
