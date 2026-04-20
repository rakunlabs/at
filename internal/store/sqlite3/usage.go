package sqlite3

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Usage Dashboard Aggregations ───

// applyUsageFilter appends WHERE clauses + args for the common UsageFilter.
// Returns the SQL fragment starting with " WHERE ..." (or empty string) and the positional args.
func applyUsageFilter(filter service.UsageFilter) (string, []interface{}) {
	var conds []string
	var args []interface{}

	if filter.From != "" {
		conds = append(conds, "created_at >= ?")
		args = append(args, filter.From)
	}
	if filter.To != "" {
		conds = append(conds, "created_at < ?")
		args = append(args, filter.To)
	}
	if filter.Status != "" {
		conds = append(conds, "status = ?")
		args = append(args, filter.Status)
	}

	addIn := func(column string, values []string) {
		if len(values) == 0 {
			return
		}
		placeholders := strings.Repeat("?,", len(values))
		placeholders = placeholders[:len(placeholders)-1]
		conds = append(conds, fmt.Sprintf("%s IN (%s)", column, placeholders))
		for _, v := range values {
			args = append(args, v)
		}
	}

	addIn("provider", filter.Providers)
	addIn("model", filter.Models)
	addIn("agent_id", filter.AgentIDs)
	addIn("organization_id", filter.OrgIDs)
	addIn("project_id", filter.ProjectIDs)
	addIn("goal_id", filter.GoalIDs)
	addIn("billing_code", filter.BillingCodes)

	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// usageAggregateSelect returns the SELECT-list fragment for usage aggregations.
// Keeping it in one place ensures summary + grouped + time-series share shape.
const usageAggregateSelect = `
    COALESCE(SUM(input_tokens), 0)  AS input_tokens,
    COALESCE(SUM(output_tokens), 0) AS output_tokens,
    COALESCE(SUM(input_tokens), 0) + COALESCE(SUM(output_tokens), 0) AS total_tokens,
    COUNT(*)                         AS request_count,
    COALESCE(SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END), 0) AS error_count,
    COALESCE(SUM(cost_cents), 0)    AS cost_cents,
    COALESCE(AVG(latency_ms), 0)    AS avg_latency_ms,
    COALESCE(MAX(latency_ms), 0)    AS max_latency_ms,
    COALESCE(SUM(latency_ms), 0)    AS total_latency_ms,
    COALESCE(MIN(created_at), '')   AS first_event_at,
    COALESCE(MAX(created_at), '')   AS last_event_at
`

func (s *SQLite) GetUsageSummary(ctx context.Context, filter service.UsageFilter) (service.UsageSummary, error) {
	where, args := applyUsageFilter(filter)
	q := fmt.Sprintf(`SELECT %s FROM %s%s`, usageAggregateSelect, s.tableCostEvents.GetTable(), where)

	var sum service.UsageSummary
	var first, last sql.NullString
	err := s.db.QueryRowContext(ctx, q, args...).Scan(
		&sum.InputTokens, &sum.OutputTokens, &sum.TotalTokens,
		&sum.RequestCount, &sum.ErrorCount, &sum.CostCents,
		&sum.AvgLatencyMs, &sum.MaxLatencyMs, &sum.TotalLatencyMs,
		&first, &last,
	)
	if err != nil {
		return service.UsageSummary{}, fmt.Errorf("usage summary: %w", err)
	}
	sum.FirstEventAt = first.String
	sum.LastEventAt = last.String
	return sum, nil
}

// usageGroupColumn maps a public groupBy name to its column in cost_events.
// Keeping this allowlisted prevents SQL injection via the group_by query param.
func usageGroupColumn(groupBy string) (string, error) {
	switch groupBy {
	case "provider":
		return "provider", nil
	case "model":
		return "model", nil
	case "agent", "agent_id":
		return "agent_id", nil
	case "organization", "organization_id", "org":
		return "organization_id", nil
	case "project", "project_id":
		return "project_id", nil
	case "goal", "goal_id":
		return "goal_id", nil
	case "billing_code", "billing":
		return "billing_code", nil
	case "status":
		return "status", nil
	default:
		return "", fmt.Errorf("invalid group_by: %q", groupBy)
	}
}

func (s *SQLite) GetUsageGrouped(ctx context.Context, filter service.UsageFilter, groupBy string, limit int) ([]service.UsageSummary, error) {
	col, err := usageGroupColumn(groupBy)
	if err != nil {
		return nil, err
	}

	where, args := applyUsageFilter(filter)
	limitClause := ""
	if limit > 0 {
		limitClause = fmt.Sprintf(" LIMIT %d", limit)
	}

	q := fmt.Sprintf(
		`SELECT %s AS _key, %s FROM %s%s GROUP BY %s ORDER BY cost_cents DESC, request_count DESC%s`,
		col, usageAggregateSelect, s.tableCostEvents.GetTable(), where, col, limitClause,
	)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("usage grouped: %w", err)
	}
	defer rows.Close()

	var out []service.UsageSummary
	for rows.Next() {
		var row service.UsageSummary
		var key sql.NullString
		var first, last sql.NullString
		if err := rows.Scan(
			&key,
			&row.InputTokens, &row.OutputTokens, &row.TotalTokens,
			&row.RequestCount, &row.ErrorCount, &row.CostCents,
			&row.AvgLatencyMs, &row.MaxLatencyMs, &row.TotalLatencyMs,
			&first, &last,
		); err != nil {
			return nil, fmt.Errorf("scan usage group row: %w", err)
		}
		row.Key = key.String
		row.FirstEventAt = first.String
		row.LastEventAt = last.String
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *SQLite) GetUsageTimeSeries(ctx context.Context, filter service.UsageFilter, bucket string) ([]service.UsageTimeSeriesPoint, error) {
	var bucketExpr string
	switch bucket {
	case "hour":
		bucketExpr = `strftime('%Y-%m-%dT%H:00:00Z', created_at)`
	case "day", "":
		bucketExpr = `strftime('%Y-%m-%dT00:00:00Z', created_at)`
	default:
		return nil, fmt.Errorf("invalid bucket: %q (expected hour or day)", bucket)
	}

	where, args := applyUsageFilter(filter)
	q := fmt.Sprintf(
		`SELECT %s AS bucket,
            COALESCE(SUM(input_tokens), 0),
            COALESCE(SUM(output_tokens), 0),
            COALESCE(SUM(input_tokens), 0) + COALESCE(SUM(output_tokens), 0),
            COUNT(*),
            COALESCE(SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END), 0),
            COALESCE(SUM(cost_cents), 0),
            COALESCE(AVG(latency_ms), 0)
         FROM %s%s
         GROUP BY bucket
         ORDER BY bucket ASC`,
		bucketExpr, s.tableCostEvents.GetTable(), where,
	)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("usage timeseries: %w", err)
	}
	defer rows.Close()

	var out []service.UsageTimeSeriesPoint
	for rows.Next() {
		var p service.UsageTimeSeriesPoint
		if err := rows.Scan(
			&p.Bucket,
			&p.InputTokens, &p.OutputTokens, &p.TotalTokens,
			&p.RequestCount, &p.ErrorCount, &p.CostCents, &p.AvgLatencyMs,
		); err != nil {
			return nil, fmt.Errorf("scan timeseries row: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
