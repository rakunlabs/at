package sqlite3

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

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
	LatencyMs      int64          `db:"latency_ms"`
	Status         string         `db:"status"`
	ErrorCode      sql.NullString `db:"error_code"`
	ErrorMessage   sql.NullString `db:"error_message"`
	CreatedAt      string         `db:"created_at"`
}

var costEventColumns = []interface{}{
	"id", "organization_id", "agent_id", "task_id", "project_id", "goal_id",
	"billing_code", "run_id", "provider", "model",
	"input_tokens", "output_tokens", "cost_cents",
	"latency_ms", "status", "error_code", "error_message",
	"created_at",
}

func scanCostEventRow(scanner interface{ Scan(dest ...any) error }) (costEventRow, error) {
	var row costEventRow
	err := scanner.Scan(
		&row.ID, &row.OrganizationID, &row.AgentID, &row.TaskID, &row.ProjectID, &row.GoalID,
		&row.BillingCode, &row.RunID, &row.Provider, &row.Model,
		&row.InputTokens, &row.OutputTokens, &row.CostCents,
		&row.LatencyMs, &row.Status, &row.ErrorCode, &row.ErrorMessage,
		&row.CreatedAt,
	)

	return row, err
}

func (s *SQLite) RecordCostEvent(ctx context.Context, event service.CostEvent) error {
	id := ulid.Make().String()
	now := time.Now().UTC()

	status := event.Status
	if status == "" {
		status = "ok"
	}

	query, _, err := s.goqu.Insert(s.tableCostEvents).Rows(
		goqu.Record{
			"id":              id,
			"organization_id": event.OrganizationID,
			"agent_id":        event.AgentID,
			"task_id":         event.TaskID,
			"project_id":      event.ProjectID,
			"goal_id":         event.GoalID,
			"billing_code":    event.BillingCode,
			"run_id":          event.RunID,
			"provider":        event.Provider,
			"model":           event.Model,
			"input_tokens":    event.InputTokens,
			"output_tokens":   event.OutputTokens,
			"cost_cents":      event.CostCents,
			"latency_ms":      event.LatencyMs,
			"status":          status,
			"error_code":      event.ErrorCode,
			"error_message":   truncateString(event.ErrorMessage, 500),
			"created_at":      now.Format(time.RFC3339),
		},
	).ToSQL()
	if err != nil {
		return fmt.Errorf("build insert cost event query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("record cost event for agent %q: %w", event.AgentID, err)
	}

	return nil
}

func (s *SQLite) ListCostEvents(ctx context.Context, q *query.Query) (*service.ListResult[service.CostEvent], error) {
	sql, total, err := s.buildListQuery(ctx, s.tableCostEvents, q, costEventColumns...)
	if err != nil {
		return nil, fmt.Errorf("build list cost events query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list cost events: %w", err)
	}
	defer rows.Close()

	var items []service.CostEvent
	for rows.Next() {
		row, err := scanCostEventRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan cost event row: %w", err)
		}

		items = append(items, costEventRowToRecord(row))
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

func (s *SQLite) GetCostByAgent(ctx context.Context, agentID string) (float64, error) {
	query, _, err := s.goqu.From(s.tableCostEvents).
		Select(goqu.COALESCE(goqu.SUM("cost_cents"), 0)).
		Where(goqu.I("agent_id").Eq(agentID)).
		ToSQL()
	if err != nil {
		return 0, fmt.Errorf("build get cost by agent query: %w", err)
	}

	var total float64
	if err := s.db.QueryRowContext(ctx, query).Scan(&total); err != nil {
		return 0, fmt.Errorf("get cost by agent %q: %w", agentID, err)
	}

	return total, nil
}

func (s *SQLite) GetCostByProject(ctx context.Context, projectID string) (float64, error) {
	query, _, err := s.goqu.From(s.tableCostEvents).
		Select(goqu.COALESCE(goqu.SUM("cost_cents"), 0)).
		Where(goqu.I("project_id").Eq(projectID)).
		ToSQL()
	if err != nil {
		return 0, fmt.Errorf("build get cost by project query: %w", err)
	}

	var total float64
	if err := s.db.QueryRowContext(ctx, query).Scan(&total); err != nil {
		return 0, fmt.Errorf("get cost by project %q: %w", projectID, err)
	}

	return total, nil
}

func (s *SQLite) GetCostByGoal(ctx context.Context, goalID string) (float64, error) {
	query, _, err := s.goqu.From(s.tableCostEvents).
		Select(goqu.COALESCE(goqu.SUM("cost_cents"), 0)).
		Where(goqu.I("goal_id").Eq(goalID)).
		ToSQL()
	if err != nil {
		return 0, fmt.Errorf("build get cost by goal query: %w", err)
	}

	var total float64
	if err := s.db.QueryRowContext(ctx, query).Scan(&total); err != nil {
		return 0, fmt.Errorf("get cost by goal %q: %w", goalID, err)
	}

	return total, nil
}

func (s *SQLite) GetCostByBillingCode(ctx context.Context, billingCode string) (float64, error) {
	query, _, err := s.goqu.From(s.tableCostEvents).
		Select(goqu.COALESCE(goqu.SUM("cost_cents"), 0)).
		Where(goqu.I("billing_code").Eq(billingCode)).
		ToSQL()
	if err != nil {
		return 0, fmt.Errorf("build get cost by billing code query: %w", err)
	}

	var total float64
	if err := s.db.QueryRowContext(ctx, query).Scan(&total); err != nil {
		return 0, fmt.Errorf("get cost by billing code %q: %w", billingCode, err)
	}

	return total, nil
}

func costEventRowToRecord(row costEventRow) service.CostEvent {
	return service.CostEvent{
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
		LatencyMs:      row.LatencyMs,
		Status:         row.Status,
		ErrorCode:      row.ErrorCode.String,
		ErrorMessage:   row.ErrorMessage.String,
		CreatedAt:      row.CreatedAt,
	}
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
