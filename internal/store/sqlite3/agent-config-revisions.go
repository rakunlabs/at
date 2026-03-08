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

type agentConfigRevisionRow struct {
	ID           string         `db:"id"`
	AgentID      string         `db:"agent_id"`
	Version      int            `db:"version"`
	ConfigBefore string         `db:"config_before"`
	ConfigAfter  string         `db:"config_after"`
	ChangedBy    string         `db:"changed_by"`
	ChangeNote   sql.NullString `db:"change_note"`
	CreatedAt    string         `db:"created_at"`
}

var agentConfigRevisionColumns = []interface{}{"id", "agent_id", "version", "config_before", "config_after", "changed_by", "change_note", "created_at"}

func scanAgentConfigRevisionRow(scanner interface{ Scan(dest ...any) error }) (agentConfigRevisionRow, error) {
	var row agentConfigRevisionRow
	err := scanner.Scan(&row.ID, &row.AgentID, &row.Version, &row.ConfigBefore, &row.ConfigAfter, &row.ChangedBy, &row.ChangeNote, &row.CreatedAt)

	return row, err
}

func (s *SQLite) CreateRevision(ctx context.Context, rev service.AgentConfigRevision) (*service.AgentConfigRevision, error) {
	id := ulid.Make().String()
	now := time.Now().UTC()

	configBeforeJSON, err := json.Marshal(rev.ConfigBefore)
	if err != nil {
		return nil, fmt.Errorf("marshal config_before: %w", err)
	}

	configAfterJSON, err := json.Marshal(rev.ConfigAfter)
	if err != nil {
		return nil, fmt.Errorf("marshal config_after: %w", err)
	}

	query, _, err := s.goqu.Insert(s.tableAgentConfigRevisions).Rows(
		goqu.Record{
			"id":            id,
			"agent_id":      rev.AgentID,
			"version":       rev.Version,
			"config_before": string(configBeforeJSON),
			"config_after":  string(configAfterJSON),
			"changed_by":    rev.ChangedBy,
			"change_note":   rev.ChangeNote,
			"created_at":    now.Format(time.RFC3339),
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert agent config revision query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create agent config revision for %q: %w", rev.AgentID, err)
	}

	return &service.AgentConfigRevision{
		ID:           id,
		AgentID:      rev.AgentID,
		Version:      rev.Version,
		ConfigBefore: rev.ConfigBefore,
		ConfigAfter:  rev.ConfigAfter,
		ChangedBy:    rev.ChangedBy,
		ChangeNote:   rev.ChangeNote,
		CreatedAt:    now.Format(time.RFC3339),
	}, nil
}

func (s *SQLite) ListRevisions(ctx context.Context, agentID string) ([]service.AgentConfigRevision, error) {
	query, _, err := s.goqu.From(s.tableAgentConfigRevisions).
		Select(agentConfigRevisionColumns...).
		Where(goqu.I("agent_id").Eq(agentID)).
		Order(goqu.I("version").Desc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list revisions query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list revisions for agent %q: %w", agentID, err)
	}
	defer rows.Close()

	var items []service.AgentConfigRevision
	for rows.Next() {
		row, err := scanAgentConfigRevisionRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent config revision row: %w", err)
		}

		rec, err := agentConfigRevisionRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *rec)
	}

	return items, rows.Err()
}

func (s *SQLite) GetRevision(ctx context.Context, id string) (*service.AgentConfigRevision, error) {
	query, _, err := s.goqu.From(s.tableAgentConfigRevisions).
		Select(agentConfigRevisionColumns...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get revision query: %w", err)
	}

	row, err := scanAgentConfigRevisionRow(s.db.QueryRowContext(ctx, query))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get revision %q: %w", id, err)
	}

	return agentConfigRevisionRowToRecord(row)
}

func (s *SQLite) GetLatestRevision(ctx context.Context, agentID string) (*service.AgentConfigRevision, error) {
	query, _, err := s.goqu.From(s.tableAgentConfigRevisions).
		Select(agentConfigRevisionColumns...).
		Where(goqu.I("agent_id").Eq(agentID)).
		Order(goqu.I("version").Desc()).
		Limit(1).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get latest revision query: %w", err)
	}

	row, err := scanAgentConfigRevisionRow(s.db.QueryRowContext(ctx, query))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get latest revision for agent %q: %w", agentID, err)
	}

	return agentConfigRevisionRowToRecord(row)
}

func agentConfigRevisionRowToRecord(row agentConfigRevisionRow) (*service.AgentConfigRevision, error) {
	var configBefore service.AgentConfig
	if err := json.Unmarshal([]byte(row.ConfigBefore), &configBefore); err != nil {
		return nil, fmt.Errorf("unmarshal config_before for revision %q: %w", row.ID, err)
	}

	var configAfter service.AgentConfig
	if err := json.Unmarshal([]byte(row.ConfigAfter), &configAfter); err != nil {
		return nil, fmt.Errorf("unmarshal config_after for revision %q: %w", row.ID, err)
	}

	return &service.AgentConfigRevision{
		ID:           row.ID,
		AgentID:      row.AgentID,
		Version:      row.Version,
		ConfigBefore: configBefore,
		ConfigAfter:  configAfter,
		ChangedBy:    row.ChangedBy,
		ChangeNote:   row.ChangeNote.String,
		CreatedAt:    row.CreatedAt,
	}, nil
}
