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
)

type agentTaskSessionRow struct {
	ID                string         `db:"id"`
	AgentID           string         `db:"agent_id"`
	TaskKey           string         `db:"task_key"`
	AdapterType       sql.NullString `db:"adapter_type"`
	SessionParamsJSON sql.NullString `db:"session_params_json"`
	SessionDisplayID  sql.NullString `db:"session_display_id"`
	CreatedAt         string         `db:"created_at"`
	UpdatedAt         string         `db:"updated_at"`
}

var agentTaskSessionColumns = []interface{}{"id", "agent_id", "task_key", "adapter_type", "session_params_json", "session_display_id", "created_at", "updated_at"}

func scanAgentTaskSessionRow(scanner interface{ Scan(dest ...any) error }) (agentTaskSessionRow, error) {
	var row agentTaskSessionRow
	err := scanner.Scan(&row.ID, &row.AgentID, &row.TaskKey, &row.AdapterType, &row.SessionParamsJSON, &row.SessionDisplayID, &row.CreatedAt, &row.UpdatedAt)

	return row, err
}

func (s *SQLite) GetAgentTaskSession(ctx context.Context, agentID, taskKey string) (*service.AgentTaskSession, error) {
	query, _, err := s.goqu.From(s.tableAgentTaskSessions).
		Select(agentTaskSessionColumns...).
		Where(
			goqu.I("agent_id").Eq(agentID),
			goqu.I("task_key").Eq(taskKey),
		).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get agent task session query: %w", err)
	}

	row, err := scanAgentTaskSessionRow(s.db.QueryRowContext(ctx, query))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get agent task session for %q/%q: %w", agentID, taskKey, err)
	}

	return agentTaskSessionRowToRecord(row)
}

func (s *SQLite) UpsertAgentTaskSession(ctx context.Context, session service.AgentTaskSession) error {
	now := time.Now().UTC().Format(time.RFC3339)
	id := ulid.Make().String()

	paramsStr := marshalJSONField(session.SessionParamsJSON)

	query, _, err := s.goqu.Insert(s.tableAgentTaskSessions).Rows(
		goqu.Record{
			"id":                  id,
			"agent_id":            session.AgentID,
			"task_key":            session.TaskKey,
			"adapter_type":        session.AdapterType,
			"session_params_json": paramsStr,
			"session_display_id":  session.SessionDisplayID,
			"created_at":          now,
			"updated_at":          now,
		},
	).OnConflict(goqu.DoUpdate("agent_id, task_key", goqu.Record{
		"adapter_type":        session.AdapterType,
		"session_params_json": paramsStr,
		"session_display_id":  session.SessionDisplayID,
		"updated_at":          now,
	})).ToSQL()
	if err != nil {
		return fmt.Errorf("build upsert agent task session query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("upsert agent task session for %q/%q: %w", session.AgentID, session.TaskKey, err)
	}

	return nil
}

func (s *SQLite) DeleteAgentTaskSession(ctx context.Context, agentID, taskKey string) error {
	query, _, err := s.goqu.Delete(s.tableAgentTaskSessions).
		Where(
			goqu.I("agent_id").Eq(agentID),
			goqu.I("task_key").Eq(taskKey),
		).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete agent task session query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete agent task session for %q/%q: %w", agentID, taskKey, err)
	}

	return nil
}

func (s *SQLite) ListAgentTaskSessions(ctx context.Context, agentID string) ([]service.AgentTaskSession, error) {
	query, _, err := s.goqu.From(s.tableAgentTaskSessions).
		Select(agentTaskSessionColumns...).
		Where(goqu.I("agent_id").Eq(agentID)).
		Order(goqu.I("updated_at").Desc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list agent task sessions query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list agent task sessions for %q: %w", agentID, err)
	}
	defer rows.Close()

	var items []service.AgentTaskSession
	for rows.Next() {
		row, err := scanAgentTaskSessionRow(rows)
		if err != nil {
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
	if row.SessionParamsJSON.Valid && row.SessionParamsJSON.String != "" {
		if err := json.Unmarshal([]byte(row.SessionParamsJSON.String), &paramsJSON); err != nil {
			return nil, fmt.Errorf("unmarshal session_params_json for session %q: %w", row.ID, err)
		}
	}

	return &service.AgentTaskSession{
		ID:                row.ID,
		AgentID:           row.AgentID,
		TaskKey:           row.TaskKey,
		AdapterType:       row.AdapterType.String,
		SessionParamsJSON: paramsJSON,
		SessionDisplayID:  row.SessionDisplayID.String,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}, nil
}
