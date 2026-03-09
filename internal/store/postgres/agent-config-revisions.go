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
	"github.com/worldline-go/types"
)

// ─── Agent Config Revisions ───

type agentConfigRevisionRow struct {
	ID           string         `db:"id"`
	AgentID      string         `db:"agent_id"`
	Version      int            `db:"version"`
	ConfigBefore types.RawJSON  `db:"config_before"`
	ConfigAfter  types.RawJSON  `db:"config_after"`
	ChangedBy    string         `db:"changed_by"`
	ChangeNote   sql.NullString `db:"change_note"`
	CreatedAt    time.Time      `db:"created_at"`
}

var agentConfigRevisionColumns = []interface{}{
	"id", "agent_id", "version", "config_before", "config_after",
	"changed_by", "change_note", "created_at",
}

func scanAgentConfigRevisionRow(scanner interface {
	Scan(dest ...interface{}) error
}, row *agentConfigRevisionRow) error {
	return scanner.Scan(
		&row.ID, &row.AgentID, &row.Version, &row.ConfigBefore, &row.ConfigAfter,
		&row.ChangedBy, &row.ChangeNote, &row.CreatedAt,
	)
}

func (p *Postgres) CreateRevision(ctx context.Context, rev service.AgentConfigRevision) (*service.AgentConfigRevision, error) {
	id := ulid.Make().String()
	now := time.Now().UTC()

	configBeforeJSON, err := json.Marshal(rev.ConfigBefore)
	if err != nil {
		return nil, fmt.Errorf("marshal config before: %w", err)
	}

	configAfterJSON, err := json.Marshal(rev.ConfigAfter)
	if err != nil {
		return nil, fmt.Errorf("marshal config after: %w", err)
	}

	query, _, err := p.goqu.Insert(p.tableAgentConfigRevisions).Rows(
		goqu.Record{
			"id":            id,
			"agent_id":      rev.AgentID,
			"version":       rev.Version,
			"config_before": types.RawJSON(configBeforeJSON),
			"config_after":  types.RawJSON(configAfterJSON),
			"changed_by":    rev.ChangedBy,
			"change_note":   nullString(rev.ChangeNote),
			"created_at":    now,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert config revision query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create config revision for agent %q: %w", rev.AgentID, err)
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

func (p *Postgres) ListRevisions(ctx context.Context, agentID string) ([]service.AgentConfigRevision, error) {
	query, _, err := p.goqu.From(p.tableAgentConfigRevisions).
		Select(agentConfigRevisionColumns...).
		Where(goqu.I("agent_id").Eq(agentID)).
		Order(goqu.I("version").Desc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list revisions query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list revisions for agent %q: %w", agentID, err)
	}
	defer rows.Close()

	var items []service.AgentConfigRevision
	for rows.Next() {
		var row agentConfigRevisionRow
		if err := scanAgentConfigRevisionRow(rows, &row); err != nil {
			return nil, fmt.Errorf("scan config revision row: %w", err)
		}

		rec, err := agentConfigRevisionRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *rec)
	}

	return items, rows.Err()
}

func (p *Postgres) GetRevision(ctx context.Context, id string) (*service.AgentConfigRevision, error) {
	query, _, err := p.goqu.From(p.tableAgentConfigRevisions).
		Select(agentConfigRevisionColumns...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get revision query: %w", err)
	}

	var row agentConfigRevisionRow
	err = scanAgentConfigRevisionRow(p.db.QueryRowContext(ctx, query), &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get revision %q: %w", id, err)
	}

	return agentConfigRevisionRowToRecord(row)
}

func (p *Postgres) GetLatestRevision(ctx context.Context, agentID string) (*service.AgentConfigRevision, error) {
	query, _, err := p.goqu.From(p.tableAgentConfigRevisions).
		Select(agentConfigRevisionColumns...).
		Where(goqu.I("agent_id").Eq(agentID)).
		Order(goqu.I("version").Desc()).
		Limit(1).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get latest revision query: %w", err)
	}

	var row agentConfigRevisionRow
	err = scanAgentConfigRevisionRow(p.db.QueryRowContext(ctx, query), &row)
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
	if len(row.ConfigBefore) > 0 {
		if err := json.Unmarshal(row.ConfigBefore, &configBefore); err != nil {
			return nil, fmt.Errorf("unmarshal config_before for revision %q: %w", row.ID, err)
		}
	}

	var configAfter service.AgentConfig
	if len(row.ConfigAfter) > 0 {
		if err := json.Unmarshal(row.ConfigAfter, &configAfter); err != nil {
			return nil, fmt.Errorf("unmarshal config_after for revision %q: %w", row.ID, err)
		}
	}

	return &service.AgentConfigRevision{
		ID:           row.ID,
		AgentID:      row.AgentID,
		Version:      row.Version,
		ConfigBefore: configBefore,
		ConfigAfter:  configAfter,
		ChangedBy:    row.ChangedBy,
		ChangeNote:   row.ChangeNote.String,
		CreatedAt:    row.CreatedAt.Format(time.RFC3339),
	}, nil
}
