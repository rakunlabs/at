package sqlite3

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ─── Agent Budget ───

type agentBudgetRow struct {
	ID           string  `db:"id"`
	AgentID      string  `db:"agent_id"`
	MonthlyLimit float64 `db:"monthly_limit"`
	CurrentSpend float64 `db:"current_spend"`
	PeriodStart  string  `db:"period_start"`
	PeriodEnd    string  `db:"period_end"`
	CreatedAt    string  `db:"created_at"`
	UpdatedAt    string  `db:"updated_at"`
}

func (s *SQLite) GetAgentBudget(ctx context.Context, agentID string) (*service.AgentBudget, error) {
	query, _, err := s.goqu.From(s.tableAgentBudgets).
		Select("id", "agent_id", "monthly_limit", "current_spend", "period_start", "period_end", "created_at", "updated_at").
		Where(goqu.I("agent_id").Eq(agentID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get agent budget query: %w", err)
	}

	var row agentBudgetRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.AgentID, &row.MonthlyLimit, &row.CurrentSpend, &row.PeriodStart, &row.PeriodEnd, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get agent budget for %q: %w", agentID, err)
	}

	return &service.AgentBudget{
		ID:           row.ID,
		AgentID:      row.AgentID,
		MonthlyLimit: row.MonthlyLimit,
		CurrentSpend: row.CurrentSpend,
		PeriodStart:  row.PeriodStart,
		PeriodEnd:    row.PeriodEnd,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
	}, nil
}

// ListAgentBudgets returns every configured agent budget.
func (s *SQLite) ListAgentBudgets(ctx context.Context) ([]service.AgentBudget, error) {
	query, _, err := s.goqu.From(s.tableAgentBudgets).
		Select("id", "agent_id", "monthly_limit", "current_spend", "period_start", "period_end", "created_at", "updated_at").
		Order(goqu.I("agent_id").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list agent budgets query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list agent budgets: %w", err)
	}
	defer rows.Close()

	var budgets []service.AgentBudget
	for rows.Next() {
		var row agentBudgetRow
		if err := rows.Scan(&row.ID, &row.AgentID, &row.MonthlyLimit, &row.CurrentSpend, &row.PeriodStart, &row.PeriodEnd, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan agent budget row: %w", err)
		}
		budgets = append(budgets, service.AgentBudget{
			ID:           row.ID,
			AgentID:      row.AgentID,
			MonthlyLimit: row.MonthlyLimit,
			CurrentSpend: row.CurrentSpend,
			PeriodStart:  row.PeriodStart,
			PeriodEnd:    row.PeriodEnd,
			CreatedAt:    row.CreatedAt,
			UpdatedAt:    row.UpdatedAt,
		})
	}
	return budgets, rows.Err()
}

func (s *SQLite) SetAgentBudget(ctx context.Context, budget service.AgentBudget) error {
	now := time.Now().UTC()
	id := ulid.Make().String()

	rawSQL := fmt.Sprintf(
		`INSERT OR REPLACE INTO %s (id, agent_id, monthly_limit, current_spend, period_start, period_end, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.tableAgentBudgets.GetTable(),
	)

	_, err := s.db.ExecContext(ctx, rawSQL,
		id,
		budget.AgentID,
		budget.MonthlyLimit,
		budget.CurrentSpend,
		budget.PeriodStart,
		budget.PeriodEnd,
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("set agent budget for %q: %w", budget.AgentID, err)
	}

	return nil
}

// ─── Agent Usage ───

type agentUsageRow struct {
	ID               string         `db:"id"`
	AgentID          string         `db:"agent_id"`
	TaskID           sql.NullString `db:"task_id"`
	WorkflowRunID    sql.NullString `db:"workflow_run_id"`
	SessionID        sql.NullString `db:"session_id"`
	Model            string         `db:"model"`
	PromptTokens     int64          `db:"prompt_tokens"`
	CompletionTokens int64          `db:"completion_tokens"`
	TotalTokens      int64          `db:"total_tokens"`
	EstimatedCost    float64        `db:"estimated_cost"`
	CreatedAt        string         `db:"created_at"`
}

var agentUsageColumns = []interface{}{"id", "agent_id", "task_id", "workflow_run_id", "session_id", "model", "prompt_tokens", "completion_tokens", "total_tokens", "estimated_cost", "created_at"}

func scanAgentUsageRow(scanner interface{ Scan(dest ...any) error }) (agentUsageRow, error) {
	var row agentUsageRow
	err := scanner.Scan(&row.ID, &row.AgentID, &row.TaskID, &row.WorkflowRunID, &row.SessionID, &row.Model, &row.PromptTokens, &row.CompletionTokens, &row.TotalTokens, &row.EstimatedCost, &row.CreatedAt)

	return row, err
}

func (s *SQLite) RecordAgentUsage(ctx context.Context, usage service.AgentUsageRecord) error {
	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := s.goqu.Insert(s.tableAgentUsage).Rows(
		goqu.Record{
			"id":                id,
			"agent_id":          usage.AgentID,
			"task_id":           usage.TaskID,
			"workflow_run_id":   usage.WorkflowRunID,
			"session_id":        usage.SessionID,
			"model":             usage.Model,
			"prompt_tokens":     usage.PromptTokens,
			"completion_tokens": usage.CompletionTokens,
			"total_tokens":      usage.TotalTokens,
			"estimated_cost":    usage.EstimatedCost,
			"created_at":        now.Format(time.RFC3339),
		},
	).ToSQL()
	if err != nil {
		return fmt.Errorf("build insert agent usage query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("record agent usage for %q: %w", usage.AgentID, err)
	}

	return nil
}

func (s *SQLite) GetAgentUsage(ctx context.Context, agentID string, q *query.Query) (*service.ListResult[service.AgentUsageRecord], error) {
	sql, total, err := s.buildListQuery(ctx, s.tableAgentUsage, q, agentUsageColumns...)
	if err != nil {
		return nil, fmt.Errorf("build list agent usage query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list agent usage for %q: %w", agentID, err)
	}
	defer rows.Close()

	var items []service.AgentUsageRecord
	for rows.Next() {
		row, err := scanAgentUsageRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent usage row: %w", err)
		}

		items = append(items, agentUsageRowToRecord(row))
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.AgentUsageRecord]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (s *SQLite) GetAgentTotalSpend(ctx context.Context, agentID string) (float64, error) {
	query, _, err := s.goqu.From(s.tableAgentUsage).
		Select(goqu.COALESCE(goqu.SUM("estimated_cost"), 0)).
		Where(goqu.I("agent_id").Eq(agentID)).
		ToSQL()
	if err != nil {
		return 0, fmt.Errorf("build get agent total spend query: %w", err)
	}

	var total float64
	if err := s.db.QueryRowContext(ctx, query).Scan(&total); err != nil {
		return 0, fmt.Errorf("get agent total spend for %q: %w", agentID, err)
	}

	return total, nil
}

// ─── Model Pricing ───

type modelPricingRow struct {
	ID                   string  `db:"id"`
	ProviderKey          string  `db:"provider_key"`
	Model                string  `db:"model"`
	PromptPricePer1M     float64 `db:"prompt_price_per_1m"`
	CompletionPricePer1M float64 `db:"completion_price_per_1m"`
	CreatedAt            string  `db:"created_at"`
	UpdatedAt            string  `db:"updated_at"`
}

func (s *SQLite) ListModelPricing(ctx context.Context) ([]service.ModelPricing, error) {
	query, _, err := s.goqu.From(s.tableModelPricing).
		Select("id", "provider_key", "model", "prompt_price_per_1m", "completion_price_per_1m", "created_at", "updated_at").
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list model pricing query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list model pricing: %w", err)
	}
	defer rows.Close()

	var items []service.ModelPricing
	for rows.Next() {
		var row modelPricingRow
		if err := rows.Scan(&row.ID, &row.ProviderKey, &row.Model, &row.PromptPricePer1M, &row.CompletionPricePer1M, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan model pricing row: %w", err)
		}

		items = append(items, service.ModelPricing{
			ID:                   row.ID,
			ProviderKey:          row.ProviderKey,
			Model:                row.Model,
			PromptPricePer1M:     row.PromptPricePer1M,
			CompletionPricePer1M: row.CompletionPricePer1M,
			CreatedAt:            row.CreatedAt,
			UpdatedAt:            row.UpdatedAt,
		})
	}

	return items, rows.Err()
}

func (s *SQLite) SetModelPricing(ctx context.Context, pricing service.ModelPricing) error {
	now := time.Now().UTC()
	id := ulid.Make().String()

	rawSQL := fmt.Sprintf(
		`INSERT OR REPLACE INTO %s (id, provider_key, model, prompt_price_per_1m, completion_price_per_1m, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.tableModelPricing.GetTable(),
	)

	_, err := s.db.ExecContext(ctx, rawSQL,
		id,
		pricing.ProviderKey,
		pricing.Model,
		pricing.PromptPricePer1M,
		pricing.CompletionPricePer1M,
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("set model pricing for %q/%q: %w", pricing.ProviderKey, pricing.Model, err)
	}

	return nil
}

func agentUsageRowToRecord(row agentUsageRow) service.AgentUsageRecord {
	return service.AgentUsageRecord{
		ID:               row.ID,
		AgentID:          row.AgentID,
		TaskID:           row.TaskID.String,
		WorkflowRunID:    row.WorkflowRunID.String,
		SessionID:        row.SessionID.String,
		Model:            row.Model,
		PromptTokens:     row.PromptTokens,
		CompletionTokens: row.CompletionTokens,
		TotalTokens:      row.TotalTokens,
		EstimatedCost:    row.EstimatedCost,
		CreatedAt:        row.CreatedAt,
	}
}
