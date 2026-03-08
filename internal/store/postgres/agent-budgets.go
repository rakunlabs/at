package postgres

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

// ─── Agent Budget CRUD ───

type agentBudgetRow struct {
	ID           string    `db:"id"`
	AgentID      string    `db:"agent_id"`
	MonthlyLimit float64   `db:"monthly_limit"`
	CurrentSpend float64   `db:"current_spend"`
	PeriodStart  time.Time `db:"period_start"`
	PeriodEnd    time.Time `db:"period_end"`
	CreatedAt    time.Time `db:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"`
}

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
	CreatedAt        time.Time      `db:"created_at"`
}

type modelPricingRow struct {
	ID                   string    `db:"id"`
	ProviderKey          string    `db:"provider_key"`
	Model                string    `db:"model"`
	PromptPricePer1M     float64   `db:"prompt_price_per_1m"`
	CompletionPricePer1M float64   `db:"completion_price_per_1m"`
	CreatedAt            time.Time `db:"created_at"`
	UpdatedAt            time.Time `db:"updated_at"`
}

func (p *Postgres) GetAgentBudget(ctx context.Context, agentID string) (*service.AgentBudget, error) {
	query, _, err := p.goqu.From(p.tableAgentBudgets).
		Select("id", "agent_id", "monthly_limit", "current_spend", "period_start", "period_end", "created_at", "updated_at").
		Where(goqu.I("agent_id").Eq(agentID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get agent budget query: %w", err)
	}

	var row agentBudgetRow
	err = p.db.QueryRowContext(ctx, query).Scan(
		&row.ID, &row.AgentID, &row.MonthlyLimit, &row.CurrentSpend,
		&row.PeriodStart, &row.PeriodEnd, &row.CreatedAt, &row.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get agent budget for %q: %w", agentID, err)
	}

	return agentBudgetRowToRecord(row), nil
}

func (p *Postgres) SetAgentBudget(ctx context.Context, budget service.AgentBudget) error {
	id := ulid.Make().String()
	now := time.Now().UTC()

	// Use raw SQL for ON CONFLICT upsert.
	rawSQL := fmt.Sprintf(
		`INSERT INTO %s (id, agent_id, monthly_limit, current_spend, period_start, period_end, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (agent_id) DO UPDATE SET
			monthly_limit = EXCLUDED.monthly_limit,
			current_spend = EXCLUDED.current_spend,
			period_start = EXCLUDED.period_start,
			period_end = EXCLUDED.period_end,
			updated_at = EXCLUDED.updated_at`,
		p.tableAgentBudgets.GetTable(),
	)

	periodStart, _ := time.Parse(time.RFC3339, budget.PeriodStart)
	periodEnd, _ := time.Parse(time.RFC3339, budget.PeriodEnd)

	_, err := p.db.ExecContext(ctx, rawSQL,
		id, budget.AgentID, budget.MonthlyLimit, budget.CurrentSpend,
		periodStart, periodEnd, now, now,
	)
	if err != nil {
		return fmt.Errorf("set agent budget for %q: %w", budget.AgentID, err)
	}

	return nil
}

func (p *Postgres) RecordAgentUsage(ctx context.Context, usage service.AgentUsageRecord) error {
	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := p.goqu.Insert(p.tableAgentUsage).Rows(
		goqu.Record{
			"id":                id,
			"agent_id":          usage.AgentID,
			"task_id":           nullString(usage.TaskID),
			"workflow_run_id":   nullString(usage.WorkflowRunID),
			"session_id":        nullString(usage.SessionID),
			"model":             usage.Model,
			"prompt_tokens":     usage.PromptTokens,
			"completion_tokens": usage.CompletionTokens,
			"total_tokens":      usage.TotalTokens,
			"estimated_cost":    usage.EstimatedCost,
			"created_at":        now,
		},
	).ToSQL()
	if err != nil {
		return fmt.Errorf("build insert agent usage query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("record agent usage: %w", err)
	}

	return nil
}

func (p *Postgres) GetAgentUsage(ctx context.Context, agentID string, q *query.Query) (*service.ListResult[service.AgentUsageRecord], error) {
	// Build a base dataset filtered by agent_id, then apply list query pagination.
	cols := []interface{}{
		"id", "agent_id", "task_id", "workflow_run_id", "session_id",
		"model", "prompt_tokens", "completion_tokens", "total_tokens", "estimated_cost", "created_at",
	}

	// Count total for this agent.
	countQuery, _, err := p.goqu.From(p.tableAgentUsage).
		Select(goqu.COUNT("*")).
		Where(goqu.I("agent_id").Eq(agentID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build count agent usage query: %w", err)
	}

	var total uint64
	if err := p.db.QueryRowContext(ctx, countQuery).Scan(&total); err != nil {
		return nil, fmt.Errorf("count agent usage: %w", err)
	}

	// Build data query.
	ds := p.goqu.From(p.tableAgentUsage).
		Where(goqu.I("agent_id").Eq(agentID)).
		Order(goqu.I("created_at").Desc())

	offset, limit := getPagination(q)
	if limit > 0 {
		ds = ds.Limit(uint(limit)).Offset(uint(offset))
	}

	dataQuery, _, err := ds.Select(cols...).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list agent usage query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, dataQuery)
	if err != nil {
		return nil, fmt.Errorf("list agent usage: %w", err)
	}
	defer rows.Close()

	var items []service.AgentUsageRecord
	for rows.Next() {
		var row agentUsageRow
		if err := rows.Scan(
			&row.ID, &row.AgentID, &row.TaskID, &row.WorkflowRunID, &row.SessionID,
			&row.Model, &row.PromptTokens, &row.CompletionTokens, &row.TotalTokens,
			&row.EstimatedCost, &row.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan agent usage row: %w", err)
		}

		items = append(items, *agentUsageRowToRecord(row))
	}

	return &service.ListResult[service.AgentUsageRecord]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (p *Postgres) GetAgentTotalSpend(ctx context.Context, agentID string) (float64, error) {
	query, _, err := p.goqu.From(p.tableAgentUsage).
		Select(goqu.COALESCE(goqu.SUM("estimated_cost"), 0)).
		Where(goqu.I("agent_id").Eq(agentID)).
		ToSQL()
	if err != nil {
		return 0, fmt.Errorf("build get agent total spend query: %w", err)
	}

	var total float64
	if err := p.db.QueryRowContext(ctx, query).Scan(&total); err != nil {
		return 0, fmt.Errorf("get agent total spend for %q: %w", agentID, err)
	}

	return total, nil
}

func (p *Postgres) ListModelPricing(ctx context.Context) ([]service.ModelPricing, error) {
	query, _, err := p.goqu.From(p.tableModelPricing).
		Select("id", "provider_key", "model", "prompt_price_per_1m", "completion_price_per_1m", "created_at", "updated_at").
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list model pricing query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list model pricing: %w", err)
	}
	defer rows.Close()

	var items []service.ModelPricing
	for rows.Next() {
		var row modelPricingRow
		if err := rows.Scan(
			&row.ID, &row.ProviderKey, &row.Model, &row.PromptPricePer1M,
			&row.CompletionPricePer1M, &row.CreatedAt, &row.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan model pricing row: %w", err)
		}

		items = append(items, *modelPricingRowToRecord(row))
	}

	return items, rows.Err()
}

func (p *Postgres) SetModelPricing(ctx context.Context, pricing service.ModelPricing) error {
	id := ulid.Make().String()
	now := time.Now().UTC()

	// Use raw SQL for ON CONFLICT upsert by (provider_key, model).
	rawSQL := fmt.Sprintf(
		`INSERT INTO %s (id, provider_key, model, prompt_price_per_1m, completion_price_per_1m, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (provider_key, model) DO UPDATE SET
			prompt_price_per_1m = EXCLUDED.prompt_price_per_1m,
			completion_price_per_1m = EXCLUDED.completion_price_per_1m,
			updated_at = EXCLUDED.updated_at`,
		p.tableModelPricing.GetTable(),
	)

	_, err := p.db.ExecContext(ctx, rawSQL,
		id, pricing.ProviderKey, pricing.Model,
		pricing.PromptPricePer1M, pricing.CompletionPricePer1M,
		now, now,
	)
	if err != nil {
		return fmt.Errorf("set model pricing for %q/%q: %w", pricing.ProviderKey, pricing.Model, err)
	}

	return nil
}

// ─── Helpers ───

func agentBudgetRowToRecord(row agentBudgetRow) *service.AgentBudget {
	return &service.AgentBudget{
		ID:           row.ID,
		AgentID:      row.AgentID,
		MonthlyLimit: row.MonthlyLimit,
		CurrentSpend: row.CurrentSpend,
		PeriodStart:  row.PeriodStart.Format(time.RFC3339),
		PeriodEnd:    row.PeriodEnd.Format(time.RFC3339),
		CreatedAt:    row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:    row.UpdatedAt.Format(time.RFC3339),
	}
}

func agentUsageRowToRecord(row agentUsageRow) *service.AgentUsageRecord {
	return &service.AgentUsageRecord{
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
		CreatedAt:        row.CreatedAt.Format(time.RFC3339),
	}
}

func modelPricingRowToRecord(row modelPricingRow) *service.ModelPricing {
	return &service.ModelPricing{
		ID:                   row.ID,
		ProviderKey:          row.ProviderKey,
		Model:                row.Model,
		PromptPricePer1M:     row.PromptPricePer1M,
		CompletionPricePer1M: row.CompletionPricePer1M,
		CreatedAt:            row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:            row.UpdatedAt.Format(time.RFC3339),
	}
}
