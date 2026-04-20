package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Usage Dashboard Aggregations ───

// applyUsageFilter builds a WHERE clause and args ($1..) matching UsageFilter.
// Uses $N placeholders (postgres) counted from startIdx.
func applyUsageFilter(filter service.UsageFilter, startIdx int) (string, []interface{}) {
	var conds []string
	var args []interface{}
	idx := startIdx

	next := func() string {
		p := fmt.Sprintf("$%d", idx)
		idx++
		return p
	}

	if filter.From != "" {
		if t, err := time.Parse(time.RFC3339, filter.From); err == nil {
			conds = append(conds, "created_at >= "+next())
			args = append(args, t)
		}
	}
	if filter.To != "" {
		if t, err := time.Parse(time.RFC3339, filter.To); err == nil {
			conds = append(conds, "created_at < "+next())
			args = append(args, t)
		}
	}
	if filter.Status != "" {
		conds = append(conds, "status = "+next())
		args = append(args, filter.Status)
	}

	addIn := func(column string, values []string) {
		if len(values) == 0 {
			return
		}
		placeholders := make([]string, 0, len(values))
		for _, v := range values {
			placeholders = append(placeholders, next())
			args = append(args, v)
		}
		conds = append(conds, fmt.Sprintf("%s IN (%s)", column, strings.Join(placeholders, ", ")))
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
    MIN(created_at)                  AS first_event_at,
    MAX(created_at)                  AS last_event_at
`

func (p *Postgres) GetUsageSummary(ctx context.Context, filter service.UsageFilter) (service.UsageSummary, error) {
	where, args := applyUsageFilter(filter, 1)
	q := fmt.Sprintf(`SELECT %s FROM %s%s`, usageAggregateSelect, p.tableCostEvents.GetTable(), where)

	var sum service.UsageSummary
	var first, last sql.NullTime
	err := p.db.QueryRowContext(ctx, q, args...).Scan(
		&sum.InputTokens, &sum.OutputTokens, &sum.TotalTokens,
		&sum.RequestCount, &sum.ErrorCount, &sum.CostCents,
		&sum.AvgLatencyMs, &sum.MaxLatencyMs, &sum.TotalLatencyMs,
		&first, &last,
	)
	if err != nil {
		return service.UsageSummary{}, fmt.Errorf("usage summary: %w", err)
	}
	if first.Valid {
		sum.FirstEventAt = first.Time.Format(time.RFC3339)
	}
	if last.Valid {
		sum.LastEventAt = last.Time.Format(time.RFC3339)
	}
	return sum, nil
}

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

func (p *Postgres) GetUsageGrouped(ctx context.Context, filter service.UsageFilter, groupBy string, limit int) ([]service.UsageSummary, error) {
	col, err := usageGroupColumn(groupBy)
	if err != nil {
		return nil, err
	}

	where, args := applyUsageFilter(filter, 1)
	limitClause := ""
	if limit > 0 {
		limitClause = fmt.Sprintf(" LIMIT %d", limit)
	}

	q := fmt.Sprintf(
		`SELECT %s AS _key, %s FROM %s%s GROUP BY %s ORDER BY cost_cents DESC, request_count DESC%s`,
		col, usageAggregateSelect, p.tableCostEvents.GetTable(), where, col, limitClause,
	)

	rows, err := p.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("usage grouped: %w", err)
	}
	defer rows.Close()

	var out []service.UsageSummary
	for rows.Next() {
		var row service.UsageSummary
		var key sql.NullString
		var first, last sql.NullTime
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
		if first.Valid {
			row.FirstEventAt = first.Time.Format(time.RFC3339)
		}
		if last.Valid {
			row.LastEventAt = last.Time.Format(time.RFC3339)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (p *Postgres) GetUsageTimeSeries(ctx context.Context, filter service.UsageFilter, bucket string) ([]service.UsageTimeSeriesPoint, error) {
	var truncUnit string
	switch bucket {
	case "hour":
		truncUnit = "hour"
	case "day", "":
		truncUnit = "day"
	default:
		return nil, fmt.Errorf("invalid bucket: %q (expected hour or day)", bucket)
	}

	where, args := applyUsageFilter(filter, 1)
	q := fmt.Sprintf(
		`SELECT date_trunc('%s', created_at) AS bucket,
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
		truncUnit, p.tableCostEvents.GetTable(), where,
	)

	rows, err := p.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("usage timeseries: %w", err)
	}
	defer rows.Close()

	var out []service.UsageTimeSeriesPoint
	for rows.Next() {
		var point service.UsageTimeSeriesPoint
		var bucketT time.Time
		if err := rows.Scan(
			&bucketT,
			&point.InputTokens, &point.OutputTokens, &point.TotalTokens,
			&point.RequestCount, &point.ErrorCount, &point.CostCents, &point.AvgLatencyMs,
		); err != nil {
			return nil, fmt.Errorf("scan timeseries row: %w", err)
		}
		point.Bucket = bucketT.UTC().Format(time.RFC3339)
		out = append(out, point)
	}
	return out, rows.Err()
}
