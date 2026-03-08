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
)

// ─── Agent Heartbeats ───

type heartbeatRow struct {
	AgentID         string          `db:"agent_id"`
	Status          string          `db:"status"`
	LastHeartbeatAt time.Time       `db:"last_heartbeat_at"`
	Metadata        json.RawMessage `db:"metadata"`
	UpdatedAt       time.Time       `db:"updated_at"`
}

func (p *Postgres) RecordHeartbeat(ctx context.Context, agentID string, metadata map[string]any) error {
	now := time.Now().UTC()

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal heartbeat metadata: %w", err)
	}

	insertQuery, _, err := p.goqu.Insert(p.tableAgentHeartbeats).Rows(
		goqu.Record{
			"agent_id":          agentID,
			"status":            "healthy",
			"last_heartbeat_at": now,
			"metadata":          metadataJSON,
			"updated_at":        now,
		},
	).OnConflict(goqu.DoUpdate("agent_id", goqu.Record{
		"status":            "healthy",
		"last_heartbeat_at": now,
		"metadata":          metadataJSON,
		"updated_at":        now,
	})).ToSQL()
	if err != nil {
		return fmt.Errorf("build upsert heartbeat query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, insertQuery); err != nil {
		return fmt.Errorf("record heartbeat for agent %q: %w", agentID, err)
	}

	return nil
}

func (p *Postgres) GetHeartbeat(ctx context.Context, agentID string) (*service.AgentHeartbeat, error) {
	query, _, err := p.goqu.From(p.tableAgentHeartbeats).
		Select("agent_id", "status", "last_heartbeat_at", "metadata", "updated_at").
		Where(goqu.I("agent_id").Eq(agentID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get heartbeat query: %w", err)
	}

	var row heartbeatRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.AgentID, &row.Status, &row.LastHeartbeatAt, &row.Metadata, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get heartbeat for agent %q: %w", agentID, err)
	}

	return heartbeatRowToRecord(row)
}

func (p *Postgres) ListHeartbeats(ctx context.Context) ([]service.AgentHeartbeat, error) {
	query, _, err := p.goqu.From(p.tableAgentHeartbeats).
		Select("agent_id", "status", "last_heartbeat_at", "metadata", "updated_at").
		Order(goqu.I("last_heartbeat_at").Desc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list heartbeats query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list heartbeats: %w", err)
	}
	defer rows.Close()

	var items []service.AgentHeartbeat
	for rows.Next() {
		var row heartbeatRow
		if err := rows.Scan(&row.AgentID, &row.Status, &row.LastHeartbeatAt, &row.Metadata, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan heartbeat row: %w", err)
		}

		rec, err := heartbeatRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *rec)
	}

	return items, rows.Err()
}

func (p *Postgres) MarkStale(ctx context.Context, threshold time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-threshold)

	query, _, err := p.goqu.Update(p.tableAgentHeartbeats).Set(
		goqu.Record{
			"status":     "stale",
			"updated_at": time.Now().UTC(),
		},
	).Where(
		goqu.I("status").Eq("healthy"),
		goqu.I("last_heartbeat_at").Lt(cutoff),
	).ToSQL()
	if err != nil {
		return 0, fmt.Errorf("build mark stale query: %w", err)
	}

	res, err := p.db.ExecContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("mark stale heartbeats: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}

	return int(affected), nil
}

func heartbeatRowToRecord(row heartbeatRow) (*service.AgentHeartbeat, error) {
	var metadata map[string]any
	if len(row.Metadata) > 0 {
		if err := json.Unmarshal(row.Metadata, &metadata); err != nil {
			return nil, fmt.Errorf("unmarshal heartbeat metadata for %q: %w", row.AgentID, err)
		}
	}

	return &service.AgentHeartbeat{
		AgentID:         row.AgentID,
		Status:          row.Status,
		LastHeartbeatAt: row.LastHeartbeatAt.Format(time.RFC3339),
		Metadata:        metadata,
		UpdatedAt:       row.UpdatedAt.Format(time.RFC3339),
	}, nil
}
