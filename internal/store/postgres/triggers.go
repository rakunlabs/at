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

// ─── Trigger CRUD ───

type triggerRow struct {
	ID         string          `db:"id"`
	WorkflowID string          `db:"workflow_id"`
	Type       string          `db:"type"`
	Config     json.RawMessage `db:"config"`
	Alias      sql.NullString  `db:"alias"`
	Public     bool            `db:"public"`
	Enabled    bool            `db:"enabled"`
	CreatedAt  time.Time       `db:"created_at"`
	UpdatedAt  time.Time       `db:"updated_at"`
	CreatedBy  string          `db:"created_by"`
	UpdatedBy  string          `db:"updated_by"`
}

func (p *Postgres) ListTriggers(ctx context.Context, workflowID string) ([]service.Trigger, error) {
	query, _, err := p.goqu.From(p.tableTriggers).
		Select("id", "workflow_id", "type", "config", "alias", "public", "enabled", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("workflow_id").Eq(workflowID)).
		Order(goqu.I("created_at").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list triggers query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list triggers: %w", err)
	}
	defer rows.Close()

	var result []service.Trigger
	for rows.Next() {
		var row triggerRow
		if err := rows.Scan(&row.ID, &row.WorkflowID, &row.Type, &row.Config, &row.Alias, &row.Public, &row.Enabled, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy); err != nil {
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

func (p *Postgres) GetTrigger(ctx context.Context, id string) (*service.Trigger, error) {
	query, _, err := p.goqu.From(p.tableTriggers).
		Select("id", "workflow_id", "type", "config", "alias", "public", "enabled", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get trigger query: %w", err)
	}

	var row triggerRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.WorkflowID, &row.Type, &row.Config, &row.Alias, &row.Public, &row.Enabled, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get trigger %q: %w", id, err)
	}

	return triggerRowToRecord(row)
}

func (p *Postgres) GetTriggerByAlias(ctx context.Context, alias string) (*service.Trigger, error) {
	query, _, err := p.goqu.From(p.tableTriggers).
		Select("id", "workflow_id", "type", "config", "alias", "public", "enabled", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("alias").Eq(alias)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get trigger by alias query: %w", err)
	}

	var row triggerRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.WorkflowID, &row.Type, &row.Config, &row.Alias, &row.Public, &row.Enabled, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get trigger by alias %q: %w", alias, err)
	}

	return triggerRowToRecord(row)
}

func (p *Postgres) CreateTrigger(ctx context.Context, t service.Trigger) (*service.Trigger, error) {
	configJSON, err := json.Marshal(t.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal trigger config: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	var alias interface{}
	if t.Alias != "" {
		alias = t.Alias
	}

	query, _, err := p.goqu.Insert(p.tableTriggers).Rows(
		goqu.Record{
			"id":          id,
			"workflow_id": t.WorkflowID,
			"type":        t.Type,
			"config":      configJSON,
			"alias":       alias,
			"public":      t.Public,
			"enabled":     t.Enabled,
			"created_at":  now,
			"updated_at":  now,
			"created_by":  t.CreatedBy,
			"updated_by":  t.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert trigger query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create trigger: %w", err)
	}

	return &service.Trigger{
		ID:         id,
		WorkflowID: t.WorkflowID,
		Type:       t.Type,
		Config:     t.Config,
		Alias:      t.Alias,
		Public:     t.Public,
		Enabled:    t.Enabled,
		CreatedAt:  now.Format(time.RFC3339),
		UpdatedAt:  now.Format(time.RFC3339),
		CreatedBy:  t.CreatedBy,
		UpdatedBy:  t.UpdatedBy,
	}, nil
}

func (p *Postgres) UpdateTrigger(ctx context.Context, id string, t service.Trigger) (*service.Trigger, error) {
	configJSON, err := json.Marshal(t.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal trigger config: %w", err)
	}

	var alias interface{}
	if t.Alias != "" {
		alias = t.Alias
	}

	now := time.Now().UTC()

	query, _, err := p.goqu.Update(p.tableTriggers).Set(
		goqu.Record{
			"type":       t.Type,
			"config":     configJSON,
			"alias":      alias,
			"public":     t.Public,
			"enabled":    t.Enabled,
			"updated_at": now,
			"updated_by": t.UpdatedBy,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update trigger query: %w", err)
	}

	res, err := p.db.ExecContext(ctx, query)
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

	return p.GetTrigger(ctx, id)
}

func (p *Postgres) DeleteTrigger(ctx context.Context, id string) error {
	query, _, err := p.goqu.Delete(p.tableTriggers).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete trigger query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete trigger %q: %w", id, err)
	}

	return nil
}

func (p *Postgres) ListEnabledCronTriggers(ctx context.Context) ([]service.Trigger, error) {
	query, _, err := p.goqu.From(p.tableTriggers).
		Select("id", "workflow_id", "type", "config", "alias", "public", "enabled", "created_at", "updated_at", "created_by", "updated_by").
		Where(
			goqu.I("type").Eq("cron"),
			goqu.I("enabled").Eq(true),
		).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list enabled cron triggers query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list enabled cron triggers: %w", err)
	}
	defer rows.Close()

	var result []service.Trigger
	for rows.Next() {
		var row triggerRow
		if err := rows.Scan(&row.ID, &row.WorkflowID, &row.Type, &row.Config, &row.Alias, &row.Public, &row.Enabled, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy); err != nil {
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
	if err := json.Unmarshal(row.Config, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal trigger config for %q: %w", row.ID, err)
	}

	alias := ""
	if row.Alias.Valid {
		alias = row.Alias.String
	}

	return &service.Trigger{
		ID:         row.ID,
		WorkflowID: row.WorkflowID,
		Type:       row.Type,
		Config:     cfg,
		Alias:      alias,
		Public:     row.Public,
		Enabled:    row.Enabled,
		CreatedAt:  row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:  row.UpdatedAt.Format(time.RFC3339),
		CreatedBy:  row.CreatedBy,
		UpdatedBy:  row.UpdatedBy,
	}, nil
}
