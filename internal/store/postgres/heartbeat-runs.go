package postgres

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

// ─── Heartbeat Runs ───

type heartbeatRunRow struct {
	ID               string          `db:"id"`
	AgentID          string          `db:"agent_id"`
	InvocationSource string          `db:"invocation_source"`
	TriggerDetail    sql.NullString  `db:"trigger_detail"`
	Status           string          `db:"status"`
	ContextSnapshot  json.RawMessage `db:"context_snapshot"`
	UsageJSON        json.RawMessage `db:"usage_json"`
	ResultJSON       json.RawMessage `db:"result_json"`
	LogRef           sql.NullString  `db:"log_ref"`
	LogBytes         int64           `db:"log_bytes"`
	LogSHA256        sql.NullString  `db:"log_sha256"`
	StdoutExcerpt    sql.NullString  `db:"stdout_excerpt"`
	StderrExcerpt    sql.NullString  `db:"stderr_excerpt"`
	SessionIDBefore  sql.NullString  `db:"session_id_before"`
	SessionIDAfter   sql.NullString  `db:"session_id_after"`
	StartedAt        sql.NullTime    `db:"started_at"`
	FinishedAt       sql.NullTime    `db:"finished_at"`
	CreatedAt        time.Time       `db:"created_at"`
}

var heartbeatRunColumns = []interface{}{
	"id", "agent_id", "invocation_source", "trigger_detail", "status",
	"context_snapshot", "usage_json", "result_json",
	"log_ref", "log_bytes", "log_sha256", "stdout_excerpt", "stderr_excerpt",
	"session_id_before", "session_id_after", "started_at", "finished_at", "created_at",
}

func scanHeartbeatRunRow(scanner interface {
	Scan(dest ...interface{}) error
}, row *heartbeatRunRow) error {
	return scanner.Scan(
		&row.ID, &row.AgentID, &row.InvocationSource, &row.TriggerDetail, &row.Status,
		&row.ContextSnapshot, &row.UsageJSON, &row.ResultJSON,
		&row.LogRef, &row.LogBytes, &row.LogSHA256, &row.StdoutExcerpt, &row.StderrExcerpt,
		&row.SessionIDBefore, &row.SessionIDAfter, &row.StartedAt, &row.FinishedAt, &row.CreatedAt,
	)
}

func (p *Postgres) CreateHeartbeatRun(ctx context.Context, run service.HeartbeatRun) (*service.HeartbeatRun, error) {
	id := ulid.Make().String()
	now := time.Now().UTC()

	contextJSON, err := json.Marshal(run.ContextSnapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal context snapshot: %w", err)
	}

	usageJSON, err := json.Marshal(run.UsageJSON)
	if err != nil {
		return nil, fmt.Errorf("marshal usage json: %w", err)
	}

	resultJSON, err := json.Marshal(run.ResultJSON)
	if err != nil {
		return nil, fmt.Errorf("marshal result json: %w", err)
	}

	query, _, err := p.goqu.Insert(p.tableHeartbeatRuns).Rows(
		goqu.Record{
			"id":                id,
			"agent_id":          run.AgentID,
			"invocation_source": run.InvocationSource,
			"trigger_detail":    nullString(run.TriggerDetail),
			"status":            run.Status,
			"context_snapshot":  contextJSON,
			"usage_json":        usageJSON,
			"result_json":       resultJSON,
			"log_ref":           nullString(run.LogRef),
			"log_bytes":         run.LogBytes,
			"log_sha256":        nullString(run.LogSHA256),
			"stdout_excerpt":    nullString(run.StdoutExcerpt),
			"stderr_excerpt":    nullString(run.StderrExcerpt),
			"session_id_before": nullString(run.SessionIDBefore),
			"session_id_after":  nullString(run.SessionIDAfter),
			"started_at":        nullTimeString(run.StartedAt),
			"finished_at":       nullTimeString(run.FinishedAt),
			"created_at":        now,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert heartbeat run query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create heartbeat run: %w", err)
	}

	run.ID = id
	run.CreatedAt = now.Format(time.RFC3339)

	return &run, nil
}

func (p *Postgres) GetHeartbeatRun(ctx context.Context, id string) (*service.HeartbeatRun, error) {
	query, _, err := p.goqu.From(p.tableHeartbeatRuns).
		Select(heartbeatRunColumns...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get heartbeat run query: %w", err)
	}

	var row heartbeatRunRow
	err = scanHeartbeatRunRow(p.db.QueryRowContext(ctx, query), &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get heartbeat run %q: %w", id, err)
	}

	return heartbeatRunRowToRecord(row)
}

func (p *Postgres) UpdateHeartbeatRun(ctx context.Context, id string, run service.HeartbeatRun) (*service.HeartbeatRun, error) {
	contextJSON, err := json.Marshal(run.ContextSnapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal context snapshot: %w", err)
	}

	usageJSON, err := json.Marshal(run.UsageJSON)
	if err != nil {
		return nil, fmt.Errorf("marshal usage json: %w", err)
	}

	resultJSON, err := json.Marshal(run.ResultJSON)
	if err != nil {
		return nil, fmt.Errorf("marshal result json: %w", err)
	}

	query, _, err := p.goqu.Update(p.tableHeartbeatRuns).Set(
		goqu.Record{
			"status":            run.Status,
			"context_snapshot":  contextJSON,
			"usage_json":        usageJSON,
			"result_json":       resultJSON,
			"log_ref":           nullString(run.LogRef),
			"log_bytes":         run.LogBytes,
			"log_sha256":        nullString(run.LogSHA256),
			"stdout_excerpt":    nullString(run.StdoutExcerpt),
			"stderr_excerpt":    nullString(run.StderrExcerpt),
			"session_id_before": nullString(run.SessionIDBefore),
			"session_id_after":  nullString(run.SessionIDAfter),
			"started_at":        nullTimeString(run.StartedAt),
			"finished_at":       nullTimeString(run.FinishedAt),
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update heartbeat run query: %w", err)
	}

	res, err := p.db.ExecContext(ctx, query)
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

	return p.GetHeartbeatRun(ctx, id)
}

func (p *Postgres) ListHeartbeatRuns(ctx context.Context, agentID string, q *query.Query) (*service.ListResult[service.HeartbeatRun], error) {
	// Build a base dataset filtered by agent_id, then apply pagination.
	baseDs := p.goqu.From(p.tableHeartbeatRuns).Where(goqu.I("agent_id").Eq(agentID))

	// Count total matching rows.
	countSQL, _, err := baseDs.Select(goqu.COUNT("*")).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build count heartbeat runs query: %w", err)
	}

	var total uint64
	if err := p.db.QueryRowContext(ctx, countSQL).Scan(&total); err != nil {
		return nil, fmt.Errorf("count heartbeat runs: %w", err)
	}

	// Build data query with pagination.
	dataDs := baseDs.Select(heartbeatRunColumns...).Order(goqu.I("created_at").Desc())

	offset, limit := getPagination(q)
	if limit > 0 {
		dataDs = dataDs.Limit(uint(limit))
	}
	if offset > 0 {
		dataDs = dataDs.Offset(uint(offset))
	}

	dataSQL, _, err := dataDs.ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list heartbeat runs query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, dataSQL)
	if err != nil {
		return nil, fmt.Errorf("list heartbeat runs for agent %q: %w", agentID, err)
	}
	defer rows.Close()

	var items []service.HeartbeatRun
	for rows.Next() {
		var row heartbeatRunRow
		if err := scanHeartbeatRunRow(rows, &row); err != nil {
			return nil, fmt.Errorf("scan heartbeat run row: %w", err)
		}

		rec, err := heartbeatRunRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *rec)
	}

	return &service.ListResult[service.HeartbeatRun]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (p *Postgres) GetActiveRun(ctx context.Context, agentID string) (*service.HeartbeatRun, error) {
	query, _, err := p.goqu.From(p.tableHeartbeatRuns).
		Select(heartbeatRunColumns...).
		Where(
			goqu.I("agent_id").Eq(agentID),
			goqu.I("status").Eq("running"),
		).
		Order(goqu.I("created_at").Desc()).
		Limit(1).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get active run query: %w", err)
	}

	var row heartbeatRunRow
	err = scanHeartbeatRunRow(p.db.QueryRowContext(ctx, query), &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get active run for agent %q: %w", agentID, err)
	}

	return heartbeatRunRowToRecord(row)
}

// nullTimeString converts an RFC3339 string to *time.Time for nullable DB columns.
// Returns nil if the string is empty.
func nullTimeString(s string) interface{} {
	if s == "" {
		return nil
	}

	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}

	return t
}

func heartbeatRunRowToRecord(row heartbeatRunRow) (*service.HeartbeatRun, error) {
	var contextSnapshot map[string]any
	if len(row.ContextSnapshot) > 0 {
		if err := json.Unmarshal(row.ContextSnapshot, &contextSnapshot); err != nil {
			return nil, fmt.Errorf("unmarshal context snapshot for run %q: %w", row.ID, err)
		}
	}

	var usageJSON map[string]any
	if len(row.UsageJSON) > 0 {
		if err := json.Unmarshal(row.UsageJSON, &usageJSON); err != nil {
			return nil, fmt.Errorf("unmarshal usage json for run %q: %w", row.ID, err)
		}
	}

	var resultJSON map[string]any
	if len(row.ResultJSON) > 0 {
		if err := json.Unmarshal(row.ResultJSON, &resultJSON); err != nil {
			return nil, fmt.Errorf("unmarshal result json for run %q: %w", row.ID, err)
		}
	}

	var startedAt, finishedAt string
	if row.StartedAt.Valid {
		startedAt = row.StartedAt.Time.Format(time.RFC3339)
	}
	if row.FinishedAt.Valid {
		finishedAt = row.FinishedAt.Time.Format(time.RFC3339)
	}

	return &service.HeartbeatRun{
		ID:               row.ID,
		AgentID:          row.AgentID,
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
		StartedAt:        startedAt,
		FinishedAt:       finishedAt,
		CreatedAt:        row.CreatedAt.Format(time.RFC3339),
	}, nil
}
