package sqlite3

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/rakunlabs/at/internal/service"
)

type agentRuntimeStateRow struct {
	AgentID           string         `db:"agent_id"`
	SessionID         sql.NullString `db:"session_id"`
	StateJSON         sql.NullString `db:"state_json"`
	TotalInputTokens  int64          `db:"total_input_tokens"`
	TotalOutputTokens int64          `db:"total_output_tokens"`
	TotalCostCents    int64          `db:"total_cost_cents"`
	LastRunID         sql.NullString `db:"last_run_id"`
	LastRunStatus     sql.NullString `db:"last_run_status"`
	LastError         sql.NullString `db:"last_error"`
	UpdatedAt         string         `db:"updated_at"`
}

var agentRuntimeStateColumns = []interface{}{"agent_id", "session_id", "state_json", "total_input_tokens", "total_output_tokens", "total_cost_cents", "last_run_id", "last_run_status", "last_error", "updated_at"}

func scanAgentRuntimeStateRow(scanner interface{ Scan(dest ...any) error }) (agentRuntimeStateRow, error) {
	var row agentRuntimeStateRow
	err := scanner.Scan(&row.AgentID, &row.SessionID, &row.StateJSON, &row.TotalInputTokens, &row.TotalOutputTokens, &row.TotalCostCents, &row.LastRunID, &row.LastRunStatus, &row.LastError, &row.UpdatedAt)

	return row, err
}

func (s *SQLite) GetAgentRuntimeState(ctx context.Context, agentID string) (*service.AgentRuntimeState, error) {
	query, _, err := s.goqu.From(s.tableAgentRuntimeState).
		Select(agentRuntimeStateColumns...).
		Where(goqu.I("agent_id").Eq(agentID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get agent runtime state query: %w", err)
	}

	row, err := scanAgentRuntimeStateRow(s.db.QueryRowContext(ctx, query))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get agent runtime state for %q: %w", agentID, err)
	}

	return agentRuntimeStateRowToRecord(row)
}

func (s *SQLite) UpsertAgentRuntimeState(ctx context.Context, state service.AgentRuntimeState) error {
	now := time.Now().UTC().Format(time.RFC3339)

	stateStr := marshalJSONField(state.StateJSON)

	query, _, err := s.goqu.Insert(s.tableAgentRuntimeState).Rows(
		goqu.Record{
			"agent_id":            state.AgentID,
			"session_id":          state.SessionID,
			"state_json":          stateStr,
			"total_input_tokens":  state.TotalInputTokens,
			"total_output_tokens": state.TotalOutputTokens,
			"total_cost_cents":    state.TotalCostCents,
			"last_run_id":         state.LastRunID,
			"last_run_status":     state.LastRunStatus,
			"last_error":          state.LastError,
			"updated_at":          now,
		},
	).OnConflict(goqu.DoUpdate("agent_id", goqu.Record{
		"session_id":          state.SessionID,
		"state_json":          stateStr,
		"total_input_tokens":  state.TotalInputTokens,
		"total_output_tokens": state.TotalOutputTokens,
		"total_cost_cents":    state.TotalCostCents,
		"last_run_id":         state.LastRunID,
		"last_run_status":     state.LastRunStatus,
		"last_error":          state.LastError,
		"updated_at":          now,
	})).ToSQL()
	if err != nil {
		return fmt.Errorf("build upsert agent runtime state query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("upsert agent runtime state for %q: %w", state.AgentID, err)
	}

	return nil
}

func (s *SQLite) AccumulateUsage(ctx context.Context, agentID string, inputTokens, outputTokens, costCents int64) error {
	now := time.Now().UTC().Format(time.RFC3339)

	rawSQL := fmt.Sprintf(
		`UPDATE %s SET total_input_tokens = total_input_tokens + ?, total_output_tokens = total_output_tokens + ?, total_cost_cents = total_cost_cents + ?, updated_at = ? WHERE agent_id = ?`,
		s.tableAgentRuntimeState.GetTable(),
	)

	res, err := s.db.ExecContext(ctx, rawSQL, inputTokens, outputTokens, costCents, now, agentID)
	if err != nil {
		return fmt.Errorf("accumulate usage for agent %q: %w", agentID, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	// If no row exists yet, insert a new one with the given values.
	if affected == 0 {
		query, _, err := s.goqu.Insert(s.tableAgentRuntimeState).Rows(
			goqu.Record{
				"agent_id":            agentID,
				"session_id":          "",
				"state_json":          "",
				"total_input_tokens":  inputTokens,
				"total_output_tokens": outputTokens,
				"total_cost_cents":    costCents,
				"last_run_id":         "",
				"last_run_status":     "",
				"last_error":          "",
				"updated_at":          now,
			},
		).ToSQL()
		if err != nil {
			return fmt.Errorf("build insert agent runtime state query: %w", err)
		}

		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("insert agent runtime state for %q: %w", agentID, err)
		}
	}

	return nil
}

func agentRuntimeStateRowToRecord(row agentRuntimeStateRow) (*service.AgentRuntimeState, error) {
	var stateJSON map[string]any
	if row.StateJSON.Valid && row.StateJSON.String != "" {
		if err := json.Unmarshal([]byte(row.StateJSON.String), &stateJSON); err != nil {
			return nil, fmt.Errorf("unmarshal state_json for agent %q: %w", row.AgentID, err)
		}
	}

	return &service.AgentRuntimeState{
		AgentID:           row.AgentID,
		SessionID:         row.SessionID.String,
		StateJSON:         stateJSON,
		TotalInputTokens:  row.TotalInputTokens,
		TotalOutputTokens: row.TotalOutputTokens,
		TotalCostCents:    row.TotalCostCents,
		LastRunID:         row.LastRunID.String,
		LastRunStatus:     row.LastRunStatus.String,
		LastError:         row.LastError.String,
		UpdatedAt:         row.UpdatedAt,
	}, nil
}
