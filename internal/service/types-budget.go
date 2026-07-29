package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rakunlabs/query"
)

// ─── Agent Budgets & Cost Tracking ───

const (
	BudgetPeriodDaily   = "daily"
	BudgetPeriodWeekly  = "weekly"
	BudgetPeriodMonthly = "monthly"
)

// BudgetSchedule defines recurring budget reset boundaries in a local timezone.
type BudgetSchedule struct {
	BudgetPeriod    string `json:"budget_period"`
	BudgetResetDay  int    `json:"budget_reset_day"`
	BudgetResetTime string `json:"budget_reset_time"`
	BudgetTimezone  string `json:"budget_timezone"`
}

// NormalizeBudgetSchedule validates a schedule and fills its defaults.
func NormalizeBudgetSchedule(schedule BudgetSchedule) (BudgetSchedule, error) {
	if schedule.BudgetPeriod == "" {
		schedule.BudgetPeriod = BudgetPeriodMonthly
	}
	if schedule.BudgetResetTime == "" {
		schedule.BudgetResetTime = "00:00"
	}
	if schedule.BudgetTimezone == "" {
		schedule.BudgetTimezone = "UTC"
	}

	switch schedule.BudgetPeriod {
	case BudgetPeriodDaily:
		if schedule.BudgetResetDay != 0 {
			return BudgetSchedule{}, fmt.Errorf("budget_reset_day is not used for daily budgets")
		}
	case BudgetPeriodWeekly:
		if schedule.BudgetResetDay == 0 {
			schedule.BudgetResetDay = 1
		}
		if schedule.BudgetResetDay < 1 || schedule.BudgetResetDay > 7 {
			return BudgetSchedule{}, fmt.Errorf("budget_reset_day must be between 1 and 7 for weekly budgets")
		}
	case BudgetPeriodMonthly:
		if schedule.BudgetResetDay == 0 {
			schedule.BudgetResetDay = 1
		}
		if schedule.BudgetResetDay < 1 || schedule.BudgetResetDay > 31 {
			return BudgetSchedule{}, fmt.Errorf("budget_reset_day must be between 1 and 31 for monthly budgets")
		}
	default:
		return BudgetSchedule{}, fmt.Errorf("budget_period must be daily, weekly, or monthly")
	}

	parts := strings.Split(schedule.BudgetResetTime, ":")
	if len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return BudgetSchedule{}, fmt.Errorf("budget_reset_time must use HH:MM format")
	}
	hour, hourErr := strconv.Atoi(parts[0])
	minute, minuteErr := strconv.Atoi(parts[1])
	if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return BudgetSchedule{}, fmt.Errorf("budget_reset_time must use HH:MM format")
	}
	if _, err := time.LoadLocation(schedule.BudgetTimezone); err != nil {
		return BudgetSchedule{}, fmt.Errorf("invalid budget_timezone %q: %w", schedule.BudgetTimezone, err)
	}

	return schedule, nil
}

// BudgetPeriodBounds returns the current schedule interval [start, end).
func BudgetPeriodBounds(now time.Time, schedule BudgetSchedule) (time.Time, time.Time, error) {
	schedule, err := NormalizeBudgetSchedule(schedule)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	loc, _ := time.LoadLocation(schedule.BudgetTimezone)
	localNow := now.In(loc)
	timeParts := strings.Split(schedule.BudgetResetTime, ":")
	hour, _ := strconv.Atoi(timeParts[0])
	minute, _ := strconv.Atoi(timeParts[1])

	boundary := func(year int, month time.Month, day int) time.Time {
		lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, loc).Day()
		if day > lastDay {
			day = lastDay
		}
		return time.Date(year, month, day, hour, minute, 0, 0, loc)
	}

	var start, end time.Time
	switch schedule.BudgetPeriod {
	case BudgetPeriodDaily:
		start = boundary(localNow.Year(), localNow.Month(), localNow.Day())
		if localNow.Before(start) {
			previous := localNow.AddDate(0, 0, -1)
			start = boundary(previous.Year(), previous.Month(), previous.Day())
		}
		next := start.AddDate(0, 0, 1)
		end = boundary(next.Year(), next.Month(), next.Day())
	case BudgetPeriodWeekly:
		daysSinceReset := (int(localNow.Weekday()) - schedule.BudgetResetDay + 7) % 7
		candidate := localNow.AddDate(0, 0, -daysSinceReset)
		start = boundary(candidate.Year(), candidate.Month(), candidate.Day())
		if localNow.Before(start) {
			candidate = candidate.AddDate(0, 0, -7)
			start = boundary(candidate.Year(), candidate.Month(), candidate.Day())
		}
		next := start.AddDate(0, 0, 7)
		end = boundary(next.Year(), next.Month(), next.Day())
	case BudgetPeriodMonthly:
		start = boundary(localNow.Year(), localNow.Month(), schedule.BudgetResetDay)
		if localNow.Before(start) {
			previous := time.Date(localNow.Year(), localNow.Month()-1, 1, 0, 0, 0, 0, loc)
			start = boundary(previous.Year(), previous.Month(), schedule.BudgetResetDay)
		}
		nextMonth := time.Date(start.Year(), start.Month()+1, 1, 0, 0, 0, 0, loc)
		end = boundary(nextMonth.Year(), nextMonth.Month(), schedule.BudgetResetDay)
	}

	return start, end, nil
}

// AgentBudget represents a spending limit for an agent within a time period.
type AgentBudget struct {
	BudgetSchedule
	ID           string  `json:"id"`
	AgentID      string  `json:"agent_id"`
	MonthlyLimit float64 `json:"monthly_limit"`
	CurrentSpend float64 `json:"current_spend"`
	PeriodStart  string  `json:"period_start"`
	PeriodEnd    string  `json:"period_end"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

// BudgetStatus reports an organization's effective budget window and spend.
type BudgetStatus struct {
	BudgetSchedule
	LimitCents     int64   `json:"limit_cents"`
	SpendCents     float64 `json:"spend_cents"`
	RemainingCents float64 `json:"remaining_cents"`
	UsagePercent   float64 `json:"usage_percent"`
	PeriodStart    string  `json:"period_start"`
	PeriodEnd      string  `json:"period_end"`
	NextResetAt    string  `json:"next_reset_at"`
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
	ID                         string  `json:"id"`
	ProviderKey                string  `json:"provider_key"`
	Model                      string  `json:"model"`
	PromptPricePer1M           float64 `json:"prompt_price_per_1m"`
	CompletionPricePer1M       float64 `json:"completion_price_per_1m"`
	CacheReadPricePer1M        float64 `json:"cache_read_price_per_1m"`
	CacheWritePricePer1M       float64 `json:"cache_write_price_per_1m"`
	Source                     string  `json:"source,omitempty"`
	SourceProvider             string  `json:"source_provider,omitempty"`
	SourceModel                string  `json:"source_model,omitempty"`
	SourceURL                  string  `json:"source_url,omitempty"`
	SourcePromptPricePer1M     float64 `json:"source_prompt_price_per_1m"`
	SourceCompletionPricePer1M float64 `json:"source_completion_price_per_1m"`
	SourceCacheReadPricePer1M  float64 `json:"source_cache_read_price_per_1m"`
	SourceCacheWritePricePer1M float64 `json:"source_cache_write_price_per_1m"`
	ManualOverride             bool    `json:"manual_override"`
	LastSyncedAt               string  `json:"last_synced_at,omitempty"`
	CreatedAt                  string  `json:"created_at"`
	UpdatedAt                  string  `json:"updated_at"`
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
	DeleteModelPricing(ctx context.Context, id string) error
	ResetModelPricingOverride(ctx context.Context, id string) error
}

// ─── Cost Events ───

// CostEvent records a single LLM call cost with full attribution.
type CostEvent struct {
	ID               string  `json:"id"`
	OrganizationID   string  `json:"organization_id,omitempty"`
	AgentID          string  `json:"agent_id"`
	TaskID           string  `json:"task_id,omitempty"`
	ProjectID        string  `json:"project_id,omitempty"`
	GoalID           string  `json:"goal_id,omitempty"`
	BillingCode      string  `json:"billing_code,omitempty"`
	RunID            string  `json:"run_id,omitempty"`
	Provider         string  `json:"provider"`
	Model            string  `json:"model"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	CostCents        float64 `json:"cost_cents"`
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
	Key              string  `json:"key,omitempty"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	RequestCount     int64   `json:"request_count"`
	ErrorCount       int64   `json:"error_count"`
	CostCents        float64 `json:"cost_cents"`
	AvgLatencyMs     float64 `json:"avg_latency_ms"`
	MaxLatencyMs     int64   `json:"max_latency_ms"`
	TotalLatencyMs   int64   `json:"total_latency_ms"`
	FirstEventAt     string  `json:"first_event_at,omitempty"`
	LastEventAt      string  `json:"last_event_at,omitempty"`
}

// UsageTimeSeriesPoint is one bucket in a time series.
type UsageTimeSeriesPoint struct {
	Bucket           string  `json:"bucket"` // RFC3339 timestamp at bucket start
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	RequestCount     int64   `json:"request_count"`
	ErrorCount       int64   `json:"error_count"`
	CostCents        float64 `json:"cost_cents"`
	AvgLatencyMs     float64 `json:"avg_latency_ms"`
}

// BudgetUtilization combines an agent's budget with its current spend.
type BudgetUtilization struct {
	BudgetSchedule
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
	CostCents        float64 `json:"cost_cents"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	EventCount       int64   `json:"event_count"`
}

// CostEventStorer defines operations for per-call cost tracking.
type CostEventStorer interface {
	RecordCostEvent(ctx context.Context, event CostEvent) error
	ListCostEvents(ctx context.Context, q *query.Query) (*ListResult[CostEvent], error)
	GetCostByAgent(ctx context.Context, agentID string) (float64, error)
	GetCostByAgentSince(ctx context.Context, agentID, since string) (float64, error)
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
