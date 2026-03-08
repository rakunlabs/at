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

// ─── Cost Events ───

type costEventRow struct {
	ID             string         `db:"id"`
	OrganizationID sql.NullString `db:"organization_id"`
	AgentID        string         `db:"agent_id"`
	TaskID         sql.NullString `db:"task_id"`
	ProjectID      sql.NullString `db:"project_id"`
	GoalID         sql.NullString `db:"goal_id"`
	BillingCode    sql.NullString `db:"billing_code"`
	RunID          sql.NullString `db:"run_id"`
	Provider       string         `db:"provider"`
	Model          string         `db:"model"`
	InputTokens    int64          `db:"input_tokens"`
	OutputTokens   int64          `db:"output_tokens"`
	CostCents      float64        `db:"cost_cents"`
	CreatedAt      time.Time      `db:"created_at"`
}

var costEventColumns = []interface{}{
	"id", "organization_id", "agent_id", "task_id", "project_id", "goal_id",
	"billing_code", "run_id", "provider", "model",
	"input_tokens", "output_tokens", "cost_cents", "created_at",
}

func scanCostEventRow(scanner interface {
	Scan(dest ...interface{}) error
}, row *costEventRow) error {
	return scanner.Scan(
		&row.ID, &row.OrganizationID, &row.AgentID, &row.TaskID,
		&row.ProjectID, &row.GoalID, &row.BillingCode, &row.RunID,
		&row.Provider, &row.Model, &row.InputTokens, &row.OutputTokens,
		&row.CostCents, &row.CreatedAt,
	)
}

func (p *Postgres) RecordCostEvent(ctx context.Context, event service.CostEvent) error {
	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := p.goqu.Insert(p.tableCostEvents).Rows(
		goqu.Record{
			"id":              id,
			"organization_id": nullString(event.OrganizationID),
			"agent_id":        event.AgentID,
			"task_id":         nullString(event.TaskID),
			"project_id":      nullString(event.ProjectID),
			"goal_id":         nullString(event.GoalID),
			"billing_code":    nullString(event.BillingCode),
			"run_id":          nullString(event.RunID),
			"provider":        event.Provider,
			"model":           event.Model,
			"input_tokens":    event.InputTokens,
			"output_tokens":   event.OutputTokens,
			"cost_cents":      event.CostCents,
			"created_at":      now,
		},
	).ToSQL()
	if err != nil {
		return fmt.Errorf("build insert cost event query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("record cost event: %w", err)
	}

	return nil
}

func (p *Postgres) ListCostEvents(ctx context.Context, q *query.Query) (*service.ListResult[service.CostEvent], error) {
	sql, total, err := p.buildListQuery(ctx, p.tableCostEvents, q, costEventColumns...)
	if err != nil {
		return nil, fmt.Errorf("build list cost events query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list cost events: %w", err)
	}
	defer rows.Close()

	var items []service.CostEvent
	for rows.Next() {
		var row costEventRow
		if err := scanCostEventRow(rows, &row); err != nil {
			return nil, fmt.Errorf("scan cost event row: %w", err)
		}

		items = append(items, *costEventRowToRecord(row))
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.CostEvent]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (p *Postgres) GetCostByAgent(ctx context.Context, agentID string) (float64, error) {
	return p.sumCostCents(ctx, "agent_id", agentID)
}

func (p *Postgres) GetCostByProject(ctx context.Context, projectID string) (float64, error) {
	return p.sumCostCents(ctx, "project_id", projectID)
}

func (p *Postgres) GetCostByGoal(ctx context.Context, goalID string) (float64, error) {
	return p.sumCostCents(ctx, "goal_id", goalID)
}

func (p *Postgres) GetCostByBillingCode(ctx context.Context, billingCode string) (float64, error) {
	return p.sumCostCents(ctx, "billing_code", billingCode)
}

func (p *Postgres) sumCostCents(ctx context.Context, column, value string) (float64, error) {
	query, _, err := p.goqu.From(p.tableCostEvents).
		Select(goqu.COALESCE(goqu.SUM("cost_cents"), 0)).
		Where(goqu.I(column).Eq(value)).
		ToSQL()
	if err != nil {
		return 0, fmt.Errorf("build sum cost query for %s=%q: %w", column, value, err)
	}

	var total float64
	err = p.db.QueryRowContext(ctx, query).Scan(&total)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("sum cost for %s=%q: %w", column, value, err)
	}

	return total, nil
}

func costEventRowToRecord(row costEventRow) *service.CostEvent {
	return &service.CostEvent{
		ID:             row.ID,
		OrganizationID: row.OrganizationID.String,
		AgentID:        row.AgentID,
		TaskID:         row.TaskID.String,
		ProjectID:      row.ProjectID.String,
		GoalID:         row.GoalID.String,
		BillingCode:    row.BillingCode.String,
		RunID:          row.RunID.String,
		Provider:       row.Provider,
		Model:          row.Model,
		InputTokens:    row.InputTokens,
		OutputTokens:   row.OutputTokens,
		CostCents:      row.CostCents,
		CreatedAt:      row.CreatedAt.Format(time.RFC3339),
	}
}
