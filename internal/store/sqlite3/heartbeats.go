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

// ─── Agent Heartbeats ───

type heartbeatRow struct {
	AgentID         string         `db:"agent_id"`
	Status          string         `db:"status"`
	LastHeartbeatAt string         `db:"last_heartbeat_at"`
	Metadata        sql.NullString `db:"metadata"`
	UpdatedAt       string         `db:"updated_at"`
}

func (s *SQLite) RecordHeartbeat(ctx context.Context, agentID string, metadata map[string]any) error {
	now := time.Now().UTC().Format(time.RFC3339)

	metadataStr := "{}"
	if metadata != nil {
		metadataJSON, err := json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("marshal heartbeat metadata: %w", err)
		}
		metadataStr = string(metadataJSON)
	}

	// SQLite uses INSERT OR REPLACE for upsert on PRIMARY KEY.
	query, _, err := s.goqu.Insert(s.tableAgentHeartbeats).Rows(
		goqu.Record{
			"agent_id":          agentID,
			"status":            "healthy",
			"last_heartbeat_at": now,
			"metadata":          metadataStr,
			"updated_at":        now,
		},
	).OnConflict(goqu.DoUpdate("agent_id", goqu.Record{
		"status":            "healthy",
		"last_heartbeat_at": now,
		"metadata":          metadataStr,
		"updated_at":        now,
	})).ToSQL()
	if err != nil {
		return fmt.Errorf("build upsert heartbeat query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("record heartbeat for agent %q: %w", agentID, err)
	}

	return nil
}

func (s *SQLite) GetHeartbeat(ctx context.Context, agentID string) (*service.AgentHeartbeat, error) {
	query, _, err := s.goqu.From(s.tableAgentHeartbeats).
		Select("agent_id", "status", "last_heartbeat_at", "metadata", "updated_at").
		Where(goqu.I("agent_id").Eq(agentID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get heartbeat query: %w", err)
	}

	var row heartbeatRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.AgentID, &row.Status, &row.LastHeartbeatAt, &row.Metadata, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get heartbeat for agent %q: %w", agentID, err)
	}

	return heartbeatRowToRecord(row)
}

func (s *SQLite) ListHeartbeats(ctx context.Context) ([]service.AgentHeartbeat, error) {
	query, _, err := s.goqu.From(s.tableAgentHeartbeats).
		Select("agent_id", "status", "last_heartbeat_at", "metadata", "updated_at").
		Order(goqu.I("last_heartbeat_at").Desc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list heartbeats query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
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

func (s *SQLite) MarkStale(ctx context.Context, threshold time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-threshold).Format(time.RFC3339)
	now := time.Now().UTC().Format(time.RFC3339)

	query, _, err := s.goqu.Update(s.tableAgentHeartbeats).Set(
		goqu.Record{
			"status":     "stale",
			"updated_at": now,
		},
	).Where(
		goqu.I("status").Eq("healthy"),
		goqu.I("last_heartbeat_at").Lt(cutoff),
	).ToSQL()
	if err != nil {
		return 0, fmt.Errorf("build mark stale query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
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
	if row.Metadata.Valid && row.Metadata.String != "" {
		if err := json.Unmarshal([]byte(row.Metadata.String), &metadata); err != nil {
			return nil, fmt.Errorf("unmarshal heartbeat metadata for %q: %w", row.AgentID, err)
		}
	}

	return &service.AgentHeartbeat{
		AgentID:         row.AgentID,
		Status:          row.Status,
		LastHeartbeatAt: row.LastHeartbeatAt,
		Metadata:        metadata,
		UpdatedAt:       row.UpdatedAt,
	}, nil
}
