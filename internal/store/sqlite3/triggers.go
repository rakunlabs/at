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

// ─── Trigger CRUD ───

type triggerRow struct {
	ID         string `db:"id"`
	WorkflowID string `db:"workflow_id"`
	Type       string `db:"type"`
	Config     string `db:"config"`
	Enabled    int    `db:"enabled"`
	CreatedAt  string `db:"created_at"`
	UpdatedAt  string `db:"updated_at"`
}

func (s *SQLite) ListTriggers(ctx context.Context, workflowID string) ([]service.Trigger, error) {
	query, _, err := s.goqu.From(s.tableTriggers).
		Select("id", "workflow_id", "type", "config", "enabled", "created_at", "updated_at").
		Where(goqu.I("workflow_id").Eq(workflowID)).
		Order(goqu.I("created_at").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list triggers query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list triggers: %w", err)
	}
	defer rows.Close()

	var result []service.Trigger
	for rows.Next() {
		var row triggerRow
		if err := rows.Scan(&row.ID, &row.WorkflowID, &row.Type, &row.Config, &row.Enabled, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan trigger row: %w", err)
		}

		t, err := triggerRowToRecord(row)
		if err != nil {
			return nil, err
		}
		result = append(result, *t)
	}

	return result, rows.Err()
}

func (s *SQLite) GetTrigger(ctx context.Context, id string) (*service.Trigger, error) {
	query, _, err := s.goqu.From(s.tableTriggers).
		Select("id", "workflow_id", "type", "config", "enabled", "created_at", "updated_at").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get trigger query: %w", err)
	}

	var row triggerRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.WorkflowID, &row.Type, &row.Config, &row.Enabled, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get trigger %q: %w", id, err)
	}

	return triggerRowToRecord(row)
}

func (s *SQLite) CreateTrigger(ctx context.Context, t service.Trigger) (*service.Trigger, error) {
	configJSON, err := json.Marshal(t.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal trigger config: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	enabled := 0
	if t.Enabled {
		enabled = 1
	}

	query, _, err := s.goqu.Insert(s.tableTriggers).Rows(
		goqu.Record{
			"id":          id,
			"workflow_id": t.WorkflowID,
			"type":        t.Type,
			"config":      string(configJSON),
			"enabled":     enabled,
			"created_at":  now.Format(time.RFC3339),
			"updated_at":  now.Format(time.RFC3339),
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert trigger query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create trigger: %w", err)
	}

	return &service.Trigger{
		ID:         id,
		WorkflowID: t.WorkflowID,
		Type:       t.Type,
		Config:     t.Config,
		Enabled:    t.Enabled,
		CreatedAt:  now.Format(time.RFC3339),
		UpdatedAt:  now.Format(time.RFC3339),
	}, nil
}

func (s *SQLite) UpdateTrigger(ctx context.Context, id string, t service.Trigger) (*service.Trigger, error) {
	configJSON, err := json.Marshal(t.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal trigger config: %w", err)
	}

	enabled := 0
	if t.Enabled {
		enabled = 1
	}

	now := time.Now().UTC()

	query, _, err := s.goqu.Update(s.tableTriggers).Set(
		goqu.Record{
			"type":       t.Type,
			"config":     string(configJSON),
			"enabled":    enabled,
			"updated_at": now.Format(time.RFC3339),
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update trigger query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update trigger %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return s.GetTrigger(ctx, id)
}

func (s *SQLite) DeleteTrigger(ctx context.Context, id string) error {
	query, _, err := s.goqu.Delete(s.tableTriggers).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete trigger query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete trigger %q: %w", id, err)
	}

	return nil
}

func (s *SQLite) ListEnabledCronTriggers(ctx context.Context) ([]service.Trigger, error) {
	query, _, err := s.goqu.From(s.tableTriggers).
		Select("id", "workflow_id", "type", "config", "enabled", "created_at", "updated_at").
		Where(
			goqu.I("type").Eq("cron"),
			goqu.I("enabled").Eq(1),
		).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list enabled cron triggers query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list enabled cron triggers: %w", err)
	}
	defer rows.Close()

	var result []service.Trigger
	for rows.Next() {
		var row triggerRow
		if err := rows.Scan(&row.ID, &row.WorkflowID, &row.Type, &row.Config, &row.Enabled, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan trigger row: %w", err)
		}

		t, err := triggerRowToRecord(row)
		if err != nil {
			return nil, err
		}
		result = append(result, *t)
	}

	return result, rows.Err()
}

// triggerRowToRecord converts a database row to a Trigger.
func triggerRowToRecord(row triggerRow) (*service.Trigger, error) {
	var cfg map[string]any
	if err := json.Unmarshal([]byte(row.Config), &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal trigger config for %q: %w", row.ID, err)
	}

	return &service.Trigger{
		ID:         row.ID,
		WorkflowID: row.WorkflowID,
		Type:       row.Type,
		Config:     cfg,
		Enabled:    row.Enabled != 0,
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
	}, nil
}
