package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/rakunlabs/at/internal/service"
	"github.com/worldline-go/types"
)

// ─── Agent Runtime State ───

type agentRuntimeStateRow struct {
	AgentID           string         `db:"agent_id"`
	SessionID         sql.NullString `db:"session_id"`
	StateJSON         types.RawJSON  `db:"state_json"`
	TotalInputTokens  int64          `db:"total_input_tokens"`
	TotalOutputTokens int64          `db:"total_output_tokens"`
	TotalCostCents    int64          `db:"total_cost_cents"`
	LastRunID         sql.NullString `db:"last_run_id"`
	LastRunStatus     sql.NullString `db:"last_run_status"`
	LastError         sql.NullString `db:"last_error"`
	UpdatedAt         time.Time      `db:"updated_at"`
}

var agentRuntimeStateColumns = []interface{}{
	"agent_id", "session_id", "state_json",
	"total_input_tokens", "total_output_tokens", "total_cost_cents",
	"last_run_id", "last_run_status", "last_error", "updated_at",
}

func scanAgentRuntimeStateRow(scanner interface {
	Scan(dest ...interface{}) error
}, row *agentRuntimeStateRow) error {
	return scanner.Scan(
		&row.AgentID, &row.SessionID, &row.StateJSON,
		&row.TotalInputTokens, &row.TotalOutputTokens, &row.TotalCostCents,
		&row.LastRunID, &row.LastRunStatus, &row.LastError, &row.UpdatedAt,
	)
}

func (p *Postgres) GetAgentRuntimeState(ctx context.Context, agentID string) (*service.AgentRuntimeState, error) {
	query, _, err := p.goqu.From(p.tableAgentRuntimeState).
		Select(agentRuntimeStateColumns...).
		Where(goqu.I("agent_id").Eq(agentID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get agent runtime state query: %w", err)
	}

	var row agentRuntimeStateRow
	err = scanAgentRuntimeStateRow(p.db.QueryRowContext(ctx, query), &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get agent runtime state for %q: %w", agentID, err)
	}

	return agentRuntimeStateRowToRecord(row)
}

func (p *Postgres) UpsertAgentRuntimeState(ctx context.Context, state service.AgentRuntimeState) error {
	now := time.Now().UTC()

	stateJSON, err := json.Marshal(state.StateJSON)
	if err != nil {
		return fmt.Errorf("marshal state json: %w", err)
	}

	insertQuery, _, err := p.goqu.Insert(p.tableAgentRuntimeState).Rows(
		goqu.Record{
			"agent_id":            state.AgentID,
			"session_id":          nullString(state.SessionID),
			"state_json":          types.RawJSON(stateJSON),
			"total_input_tokens":  state.TotalInputTokens,
			"total_output_tokens": state.TotalOutputTokens,
			"total_cost_cents":    state.TotalCostCents,
			"last_run_id":         nullString(state.LastRunID),
			"last_run_status":     nullString(state.LastRunStatus),
			"last_error":          nullString(state.LastError),
			"updated_at":          now,
		},
	).OnConflict(goqu.DoUpdate("agent_id", goqu.Record{
		"session_id":          nullString(state.SessionID),
		"state_json":          types.RawJSON(stateJSON),
		"total_input_tokens":  state.TotalInputTokens,
		"total_output_tokens": state.TotalOutputTokens,
		"total_cost_cents":    state.TotalCostCents,
		"last_run_id":         nullString(state.LastRunID),
		"last_run_status":     nullString(state.LastRunStatus),
		"last_error":          nullString(state.LastError),
		"updated_at":          now,
	})).ToSQL()
	if err != nil {
		return fmt.Errorf("build upsert agent runtime state query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, insertQuery); err != nil {
		return fmt.Errorf("upsert agent runtime state for %q: %w", state.AgentID, err)
	}

	return nil
}

func (p *Postgres) AccumulateUsage(ctx context.Context, agentID string, inputTokens, outputTokens, costCents int64) error {
	now := time.Now().UTC()

	// Use INSERT ON CONFLICT to atomically create or increment counters.
	rawSQL := fmt.Sprintf(
		`INSERT INTO %s (agent_id, session_id, state_json, total_input_tokens, total_output_tokens, total_cost_cents, last_run_id, last_run_status, last_error, updated_at)
		VALUES ($1, NULL, '{}', $2, $3, $4, NULL, NULL, NULL, $5)
		ON CONFLICT (agent_id) DO UPDATE SET
			total_input_tokens = %s.total_input_tokens + $2,
			total_output_tokens = %s.total_output_tokens + $3,
			total_cost_cents = %s.total_cost_cents + $4,
			updated_at = $5`,
		p.tableAgentRuntimeState.GetTable(),
		p.tableAgentRuntimeState.GetTable(),
		p.tableAgentRuntimeState.GetTable(),
		p.tableAgentRuntimeState.GetTable(),
	)

	if _, err := p.db.ExecContext(ctx, rawSQL, agentID, inputTokens, outputTokens, costCents, now); err != nil {
		return fmt.Errorf("accumulate usage for agent %q: %w", agentID, err)
	}

	return nil
}

func agentRuntimeStateRowToRecord(row agentRuntimeStateRow) (*service.AgentRuntimeState, error) {
	var stateJSON map[string]any
	if len(row.StateJSON) > 0 {
		if err := json.Unmarshal(row.StateJSON, &stateJSON); err != nil {
			return nil, fmt.Errorf("unmarshal state json for agent %q: %w", row.AgentID, err)
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
		UpdatedAt:         row.UpdatedAt.Format(time.RFC3339),
	}, nil
}
