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
)

// ─── Agent Task Sessions ───

type agentTaskSessionRow struct {
	ID                string          `db:"id"`
	AgentID           string          `db:"agent_id"`
	TaskKey           string          `db:"task_key"`
	AdapterType       sql.NullString  `db:"adapter_type"`
	SessionParamsJSON json.RawMessage `db:"session_params_json"`
	SessionDisplayID  sql.NullString  `db:"session_display_id"`
	CreatedAt         time.Time       `db:"created_at"`
	UpdatedAt         time.Time       `db:"updated_at"`
}

var agentTaskSessionColumns = []interface{}{
	"id", "agent_id", "task_key", "adapter_type", "session_params_json",
	"session_display_id", "created_at", "updated_at",
}

func scanAgentTaskSessionRow(scanner interface {
	Scan(dest ...interface{}) error
}, row *agentTaskSessionRow) error {
	return scanner.Scan(
		&row.ID, &row.AgentID, &row.TaskKey, &row.AdapterType,
		&row.SessionParamsJSON, &row.SessionDisplayID, &row.CreatedAt, &row.UpdatedAt,
	)
}

func (p *Postgres) GetAgentTaskSession(ctx context.Context, agentID, taskKey string) (*service.AgentTaskSession, error) {
	query, _, err := p.goqu.From(p.tableAgentTaskSessions).
		Select(agentTaskSessionColumns...).
		Where(
			goqu.I("agent_id").Eq(agentID),
			goqu.I("task_key").Eq(taskKey),
		).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get agent task session query: %w", err)
	}

	var row agentTaskSessionRow
	err = scanAgentTaskSessionRow(p.db.QueryRowContext(ctx, query), &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get agent task session (%q, %q): %w", agentID, taskKey, err)
	}

	return agentTaskSessionRowToRecord(row)
}

func (p *Postgres) UpsertAgentTaskSession(ctx context.Context, session service.AgentTaskSession) error {
	now := time.Now().UTC()
	id := ulid.Make().String()

	paramsJSON, err := json.Marshal(session.SessionParamsJSON)
	if err != nil {
		return fmt.Errorf("marshal session params json: %w", err)
	}

	insertQuery, _, err := p.goqu.Insert(p.tableAgentTaskSessions).Rows(
		goqu.Record{
			"id":                  id,
			"agent_id":            session.AgentID,
			"task_key":            session.TaskKey,
			"adapter_type":        nullString(session.AdapterType),
			"session_params_json": paramsJSON,
			"session_display_id":  nullString(session.SessionDisplayID),
			"created_at":          now,
			"updated_at":          now,
		},
	).OnConflict(goqu.DoUpdate("agent_id, task_key", goqu.Record{
		"adapter_type":        nullString(session.AdapterType),
		"session_params_json": paramsJSON,
		"session_display_id":  nullString(session.SessionDisplayID),
		"updated_at":          now,
	})).ToSQL()
	if err != nil {
		return fmt.Errorf("build upsert agent task session query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, insertQuery); err != nil {
		return fmt.Errorf("upsert agent task session (%q, %q): %w", session.AgentID, session.TaskKey, err)
	}

	return nil
}

func (p *Postgres) DeleteAgentTaskSession(ctx context.Context, agentID, taskKey string) error {
	query, _, err := p.goqu.Delete(p.tableAgentTaskSessions).
		Where(
			goqu.I("agent_id").Eq(agentID),
			goqu.I("task_key").Eq(taskKey),
		).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete agent task session query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete agent task session (%q, %q): %w", agentID, taskKey, err)
	}

	return nil
}

func (p *Postgres) ListAgentTaskSessions(ctx context.Context, agentID string) ([]service.AgentTaskSession, error) {
	query, _, err := p.goqu.From(p.tableAgentTaskSessions).
		Select(agentTaskSessionColumns...).
		Where(goqu.I("agent_id").Eq(agentID)).
		Order(goqu.I("updated_at").Desc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list agent task sessions query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list agent task sessions for %q: %w", agentID, err)
	}
	defer rows.Close()

	var items []service.AgentTaskSession
	for rows.Next() {
		var row agentTaskSessionRow
		if err := scanAgentTaskSessionRow(rows, &row); err != nil {
			return nil, fmt.Errorf("scan agent task session row: %w", err)
		}

		rec, err := agentTaskSessionRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *rec)
	}

	return items, rows.Err()
}

func agentTaskSessionRowToRecord(row agentTaskSessionRow) (*service.AgentTaskSession, error) {
	var paramsJSON map[string]any
	if len(row.SessionParamsJSON) > 0 {
		if err := json.Unmarshal(row.SessionParamsJSON, &paramsJSON); err != nil {
			return nil, fmt.Errorf("unmarshal session params for (%q, %q): %w", row.AgentID, row.TaskKey, err)
		}
	}

	return &service.AgentTaskSession{
		ID:                row.ID,
		AgentID:           row.AgentID,
		TaskKey:           row.TaskKey,
		AdapterType:       row.AdapterType.String,
		SessionParamsJSON: paramsJSON,
		SessionDisplayID:  row.SessionDisplayID.String,
		CreatedAt:         row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:         row.UpdatedAt.Format(time.RFC3339),
	}, nil
}
