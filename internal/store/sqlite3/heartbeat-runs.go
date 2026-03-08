package sqlite3

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

type heartbeatRunRow struct {
	ID               string         `db:"id"`
	AgentID          string         `db:"agent_id"`
	OrganizationID   string         `db:"organization_id"`
	InvocationSource string         `db:"invocation_source"`
	TriggerDetail    sql.NullString `db:"trigger_detail"`
	Status           string         `db:"status"`
	ContextSnapshot  sql.NullString `db:"context_snapshot"`
	UsageJSON        sql.NullString `db:"usage_json"`
	ResultJSON       sql.NullString `db:"result_json"`
	LogRef           sql.NullString `db:"log_ref"`
	LogBytes         int64          `db:"log_bytes"`
	LogSHA256        sql.NullString `db:"log_sha256"`
	StdoutExcerpt    sql.NullString `db:"stdout_excerpt"`
	StderrExcerpt    sql.NullString `db:"stderr_excerpt"`
	SessionIDBefore  sql.NullString `db:"session_id_before"`
	SessionIDAfter   sql.NullString `db:"session_id_after"`
	StartedAt        sql.NullString `db:"started_at"`
	FinishedAt       sql.NullString `db:"finished_at"`
	CreatedAt        string         `db:"created_at"`
}

var heartbeatRunColumns = []interface{}{
	"id", "agent_id", "organization_id", "invocation_source", "trigger_detail", "status",
	"context_snapshot", "usage_json", "result_json",
	"log_ref", "log_bytes", "log_sha256", "stdout_excerpt", "stderr_excerpt",
	"session_id_before", "session_id_after", "started_at", "finished_at", "created_at",
}

func scanHeartbeatRunRow(scanner interface{ Scan(dest ...any) error }) (heartbeatRunRow, error) {
	var row heartbeatRunRow
	err := scanner.Scan(
		&row.ID, &row.AgentID, &row.OrganizationID, &row.InvocationSource, &row.TriggerDetail, &row.Status,
		&row.ContextSnapshot, &row.UsageJSON, &row.ResultJSON,
		&row.LogRef, &row.LogBytes, &row.LogSHA256, &row.StdoutExcerpt, &row.StderrExcerpt,
		&row.SessionIDBefore, &row.SessionIDAfter, &row.StartedAt, &row.FinishedAt, &row.CreatedAt,
	)

	return row, err
}

func (s *SQLite) CreateHeartbeatRun(ctx context.Context, run service.HeartbeatRun) (*service.HeartbeatRun, error) {
	id := ulid.Make().String()
	now := time.Now().UTC()

	contextStr := marshalJSONField(run.ContextSnapshot)
	usageStr := marshalJSONField(run.UsageJSON)
	resultStr := marshalJSONField(run.ResultJSON)

	query, _, err := s.goqu.Insert(s.tableHeartbeatRuns).Rows(
		goqu.Record{
			"id":                id,
			"agent_id":          run.AgentID,
			"organization_id":   run.OrganizationID,
			"invocation_source": run.InvocationSource,
			"trigger_detail":    run.TriggerDetail,
			"status":            run.Status,
			"context_snapshot":  contextStr,
			"usage_json":        usageStr,
			"result_json":       resultStr,
			"log_ref":           run.LogRef,
			"log_bytes":         run.LogBytes,
			"log_sha256":        run.LogSHA256,
			"stdout_excerpt":    run.StdoutExcerpt,
			"stderr_excerpt":    run.StderrExcerpt,
			"session_id_before": run.SessionIDBefore,
			"session_id_after":  run.SessionIDAfter,
			"started_at":        run.StartedAt,
			"finished_at":       run.FinishedAt,
			"created_at":        now.Format(time.RFC3339),
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert heartbeat run query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create heartbeat run for agent %q: %w", run.AgentID, err)
	}

	run.ID = id
	run.CreatedAt = now.Format(time.RFC3339)

	return &run, nil
}

func (s *SQLite) GetHeartbeatRun(ctx context.Context, id string) (*service.HeartbeatRun, error) {
	query, _, err := s.goqu.From(s.tableHeartbeatRuns).
		Select(heartbeatRunColumns...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get heartbeat run query: %w", err)
	}

	row, err := scanHeartbeatRunRow(s.db.QueryRowContext(ctx, query))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get heartbeat run %q: %w", id, err)
	}

	rec, err := heartbeatRunRowToRecord(row)
	if err != nil {
		return nil, err
	}

	return rec, nil
}

func (s *SQLite) UpdateHeartbeatRun(ctx context.Context, id string, run service.HeartbeatRun) (*service.HeartbeatRun, error) {
	usageStr := marshalJSONField(run.UsageJSON)
	resultStr := marshalJSONField(run.ResultJSON)

	query, _, err := s.goqu.Update(s.tableHeartbeatRuns).Set(
		goqu.Record{
			"status":            run.Status,
			"usage_json":        usageStr,
			"result_json":       resultStr,
			"log_ref":           run.LogRef,
			"log_bytes":         run.LogBytes,
			"log_sha256":        run.LogSHA256,
			"stdout_excerpt":    run.StdoutExcerpt,
			"stderr_excerpt":    run.StderrExcerpt,
			"session_id_before": run.SessionIDBefore,
			"session_id_after":  run.SessionIDAfter,
			"started_at":        run.StartedAt,
			"finished_at":       run.FinishedAt,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update heartbeat run query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update heartbeat run %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return s.GetHeartbeatRun(ctx, id)
}

func (s *SQLite) ListHeartbeatRuns(ctx context.Context, agentID string, q *query.Query) (*service.ListResult[service.HeartbeatRun], error) {
	sql, total, err := s.buildListQuery(ctx, s.tableHeartbeatRuns, q, heartbeatRunColumns...)
	if err != nil {
		return nil, fmt.Errorf("build list heartbeat runs query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list heartbeat runs for agent %q: %w", agentID, err)
	}
	defer rows.Close()

	var items []service.HeartbeatRun
	for rows.Next() {
		row, err := scanHeartbeatRunRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan heartbeat run row: %w", err)
		}

		rec, err := heartbeatRunRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *rec)
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.HeartbeatRun]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (s *SQLite) GetActiveRun(ctx context.Context, agentID string) (*service.HeartbeatRun, error) {
	query, _, err := s.goqu.From(s.tableHeartbeatRuns).
		Select(heartbeatRunColumns...).
		Where(
			goqu.I("agent_id").Eq(agentID),
			goqu.I("status").Eq(service.RunStatusRunning),
		).
		Order(goqu.I("created_at").Desc()).
		Limit(1).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get active run query: %w", err)
	}

	row, err := scanHeartbeatRunRow(s.db.QueryRowContext(ctx, query))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get active run for agent %q: %w", agentID, err)
	}

	rec, err := heartbeatRunRowToRecord(row)
	if err != nil {
		return nil, err
	}

	return rec, nil
}

// ─── Helpers ───

func marshalJSONField(m map[string]any) string {
	if m == nil {
		return ""
	}
	b, _ := json.Marshal(m)

	return string(b)
}

func unmarshalJSONField(s sql.NullString) (map[string]any, error) {
	if !s.Valid || s.String == "" {
		return nil, nil
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(s.String), &m); err != nil {
		return nil, err
	}

	return m, nil
}

func heartbeatRunRowToRecord(row heartbeatRunRow) (*service.HeartbeatRun, error) {
	contextSnapshot, err := unmarshalJSONField(row.ContextSnapshot)
	if err != nil {
		return nil, fmt.Errorf("unmarshal context_snapshot for run %q: %w", row.ID, err)
	}

	usageJSON, err := unmarshalJSONField(row.UsageJSON)
	if err != nil {
		return nil, fmt.Errorf("unmarshal usage_json for run %q: %w", row.ID, err)
	}

	resultJSON, err := unmarshalJSONField(row.ResultJSON)
	if err != nil {
		return nil, fmt.Errorf("unmarshal result_json for run %q: %w", row.ID, err)
	}

	return &service.HeartbeatRun{
		ID:               row.ID,
		AgentID:          row.AgentID,
		OrganizationID:   row.OrganizationID,
		InvocationSource: row.InvocationSource,
		TriggerDetail:    row.TriggerDetail.String,
		Status:           row.Status,
		ContextSnapshot:  contextSnapshot,
		UsageJSON:        usageJSON,
		ResultJSON:       resultJSON,
		LogRef:           row.LogRef.String,
		LogBytes:         row.LogBytes,
		LogSHA256:        row.LogSHA256.String,
		StdoutExcerpt:    row.StdoutExcerpt.String,
		StderrExcerpt:    row.StderrExcerpt.String,
		SessionIDBefore:  row.SessionIDBefore.String,
		SessionIDAfter:   row.SessionIDAfter.String,
		StartedAt:        row.StartedAt.String,
		FinishedAt:       row.FinishedAt.String,
		CreatedAt:        row.CreatedAt,
	}, nil
}
