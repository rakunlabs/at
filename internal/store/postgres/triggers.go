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

// ─── Trigger CRUD ───

type triggerRow struct {
	WorkspaceID string         `db:"workspace_id"`
	ID          string         `db:"id"`
	WorkflowID  sql.NullString `db:"workflow_id"`
	TargetType  string         `db:"target_type"`
	TargetID    string         `db:"target_id"`
	EntryNodeID string         `db:"entry_node_id"`
	Type        string         `db:"type"`
	Config      types.RawJSON  `db:"config"`
	Alias       sql.NullString `db:"alias"`
	Public      bool           `db:"public"`
	Enabled     bool           `db:"enabled"`
	CreatedAt   time.Time      `db:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at"`
	CreatedBy   string         `db:"created_by"`
	UpdatedBy   string         `db:"updated_by"`
	HideMain    bool           `db:"hide_from_main"`
	Secret      string         `db:"webhook_secret"`
}

var triggerSelectColumns = []string{"id", "workflow_id", "target_type", "target_id", "entry_node_id", "type", "config", "alias", "public", "enabled", "created_at", "updated_at", "created_by", "updated_by", "workspace_id", "hide_from_main", "webhook_secret"}

func triggerSelectColumnsAny() []any {
	out := make([]any, len(triggerSelectColumns))
	for i, c := range triggerSelectColumns {
		out[i] = c
	}
	return out
}

func scanTriggerRow(scanner interface{ Scan(...any) error }) (*triggerRow, error) {
	var row triggerRow
	if err := scanner.Scan(&row.ID, &row.WorkflowID, &row.TargetType, &row.TargetID, &row.EntryNodeID, &row.Type, &row.Config, &row.Alias, &row.Public, &row.Enabled, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy, &row.WorkspaceID, &row.HideMain, &row.Secret); err != nil {
		return nil, err
	}
	return &row, nil
}

func (p *Postgres) ListAllTriggers(ctx context.Context) ([]service.Trigger, error) {
	scope, err := p.businessReadScope(ctx, p.tableTriggers)
	if err != nil {
		return nil, err
	}
	query, _, err := p.goqu.From(p.tableTriggers).
		Select(triggerSelectColumnsAny()...).Where(scope).
		Order(goqu.I("created_at").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list all triggers query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list all triggers: %w", err)
	}
	defer rows.Close()

	var result []service.Trigger
	for rows.Next() {
		row, err := scanTriggerRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan trigger row: %w", err)
		}

		t, err := p.triggerRowToRecord(*row)
		if err != nil {
			return nil, err
		}
		result = append(result, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, p.attachTriggerRoutes(ctx, result)
}

func (p *Postgres) ListTriggers(ctx context.Context, workflowID string) ([]service.Trigger, error) {
	scope, err := p.businessReadScope(ctx, p.tableTriggers)
	if err != nil {
		return nil, err
	}
	query, _, err := p.goqu.From(p.tableTriggers).
		Select(triggerSelectColumnsAny()...).
		Where(scope, goqu.I("workflow_id").Eq(workflowID)).
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
		row, err := scanTriggerRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan trigger row: %w", err)
		}

		t, err := p.triggerRowToRecord(*row)
		if err != nil {
			return nil, err
		}
		result = append(result, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, p.attachTriggerRoutes(ctx, result)
}

func (p *Postgres) GetTrigger(ctx context.Context, id string) (*service.Trigger, error) {
	scope, err := p.businessReadScope(ctx, p.tableTriggers)
	if err != nil {
		return nil, err
	}
	query, _, err := p.goqu.From(p.tableTriggers).
		Select(triggerSelectColumnsAny()...).
		Where(scope, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get trigger query: %w", err)
	}

	row, err := scanTriggerRow(p.db.QueryRowContext(ctx, query))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get trigger %q: %w", id, err)
	}

	return p.singleTriggerWithRoutes(ctx, *row)
}

func (p *Postgres) GetTriggerByAlias(ctx context.Context, alias string) (*service.Trigger, error) {
	scope, err := p.businessReadScope(ctx, p.tableTriggers)
	if err != nil {
		return nil, err
	}
	query, _, err := p.goqu.From(p.tableTriggers).
		Select(triggerSelectColumnsAny()...).
		Where(scope, goqu.I("alias").Eq(alias)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get trigger by alias query: %w", err)
	}

	row, err := scanTriggerRow(p.db.QueryRowContext(ctx, query))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get trigger by alias %q: %w", alias, err)
	}

	return p.singleTriggerWithRoutes(ctx, *row)
}

func (p *Postgres) CreateTrigger(ctx context.Context, t service.Trigger) (*service.Trigger, error) {
	t, err := normalizeScopedTrigger(t)
	if err != nil {
		return nil, err
	}
	w, err := p.beginBusinessWrite(ctx, p.tableTriggers, "workflows.write", t.WorkflowID)
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()
	if err = p.triggerReferences(ctx, w, t); err != nil {
		return nil, err
	}
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

	// Ensure backward compat: if TargetType is empty, default to workflow.
	targetType := t.TargetType
	if targetType == "" {
		targetType = service.TriggerTargetWorkflow
	}
	targetID := t.TargetID
	if targetID == "" {
		targetID = t.WorkflowID
	}

	var workflowID interface{}
	if t.WorkflowID != "" {
		workflowID = t.WorkflowID
	}

	routes, err := service.NormalizeWebhookRoutes(t.WebhookRoutes)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrWorkspaceConflict, err)
	}
	if t.Type != "http" && (len(routes) > 0 || t.HideFromMain || (t.Signature != nil && t.Signature.Scheme != "")) {
		return nil, fmt.Errorf("%w: webhook routing applies only to http triggers", service.ErrWorkspaceConflict)
	}
	if t.HideFromMain && len(routes) == 0 {
		return nil, fmt.Errorf("%w: hiding a webhook from the main route requires at least one webhook server", service.ErrWorkspaceConflict)
	}
	secret, signature, err := p.resolveTriggerSignature(t.Signature, "")
	if err != nil {
		return nil, err
	}

	query, _, err := p.goqu.Insert(p.tableTriggers).Rows(
		goqu.Record{
			"workspace_id":  w.actor.WorkspaceID,
			"id":            id,
			"workflow_id":   workflowID,
			"target_type":   targetType,
			"target_id":     targetID,
			"entry_node_id": t.EntryNodeID,
			"type":          t.Type,
			"config":        types.RawJSON(configJSON),
			"alias":         alias,
			"public":        t.Public,
			"enabled":       t.Enabled,
			"created_at":    now,
			"updated_at":    now,
			"created_by":     t.CreatedBy,
			"updated_by":     t.UpdatedBy,
			"hide_from_main": t.HideFromMain,
			"webhook_secret": secret,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert trigger query: %w", err)
	}

	if _, err := w.tx.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create trigger: %w", err)
	}
	if err = p.replaceTriggerRoutes(ctx, w, id, routes); err != nil {
		return nil, err
	}
	if err = w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit trigger: %w", err)
	}

	return &service.Trigger{
		WorkspaceID: w.actor.WorkspaceID,
		ID:          id,
		WorkflowID:  t.WorkflowID,
		TargetType:  targetType,
		TargetID:    targetID,
		EntryNodeID: t.EntryNodeID,
		Type:        t.Type,
		Config:      t.Config,
		Alias:       t.Alias,
		Public:      t.Public,
		Enabled:     t.Enabled,
		CreatedAt:   now.Format(time.RFC3339),
		UpdatedAt:   now.Format(time.RFC3339),
		CreatedBy:   t.CreatedBy,
		UpdatedBy:   t.UpdatedBy,

		WebhookRoutes: routes,
		HideFromMain:  t.HideFromMain,
		Signature:     signature.Redacted(),
	}, nil
}

func (p *Postgres) UpdateTrigger(ctx context.Context, id string, t service.Trigger) (*service.Trigger, error) {
	t, err := normalizeScopedTrigger(t)
	if err != nil {
		return nil, err
	}
	w, err := p.beginTriggerWrite(ctx, id)
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()
	if err = p.triggerReferences(ctx, w, t); err != nil {
		return nil, err
	}
	configJSON, err := json.Marshal(t.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal trigger config: %w", err)
	}

	var alias interface{}
	if t.Alias != "" {
		alias = t.Alias
	}

	now := time.Now().UTC()

	var stored struct {
		Secret string `db:"webhook_secret"`
	}
	if _, err := w.tx.From(p.tableTriggers).Select("webhook_secret").Where(w.predicate, goqu.C("id").Eq(id)).ScanStructContext(ctx, &stored); err != nil {
		return nil, fmt.Errorf("read trigger webhook settings: %w", err)
	}
	record := goqu.Record{}
	if t.WebhookRoutes != nil {
		record["hide_from_main"] = t.HideFromMain
	}
	if t.Signature != nil {
		secret, _, err := p.resolveTriggerSignature(t.Signature, stored.Secret)
		if err != nil {
			return nil, err
		}
		record["webhook_secret"] = secret
	}
	var routes []service.WebhookRoute
	if t.WebhookRoutes != nil {
		if routes, err = service.NormalizeWebhookRoutes(t.WebhookRoutes); err != nil {
			return nil, fmt.Errorf("%w: %w", service.ErrWorkspaceConflict, err)
		}
		if t.HideFromMain && len(routes) == 0 {
			return nil, fmt.Errorf("%w: hiding a webhook from the main route requires at least one webhook server", service.ErrWorkspaceConflict)
		}
	}
	if t.Type == "cron" {
		if len(routes) > 0 || (t.WebhookRoutes != nil && t.HideFromMain) || (t.Signature != nil && t.Signature.Scheme != "") {
			return nil, fmt.Errorf("%w: webhook routing applies only to http triggers", service.ErrWorkspaceConflict)
		}
		// A trigger converted away from http keeps no webhook exposure.
		record["hide_from_main"], record["webhook_secret"] = false, ""
		routes, t.WebhookRoutes = nil, []service.WebhookRoute{}
	}

	record["workflow_id"] = t.WorkflowID
	record["target_type"] = t.TargetType
	record["target_id"] = t.TargetID
	record["entry_node_id"] = t.EntryNodeID
	record["type"] = t.Type
	record["config"] = types.RawJSON(configJSON)
	record["alias"] = alias
	record["public"] = t.Public
	record["enabled"] = t.Enabled
	record["updated_at"] = now
	record["updated_by"] = t.UpdatedBy
	query, _, err := p.goqu.Update(p.tableTriggers).Set(record).Where(w.predicate, goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update trigger query: %w", err)
	}

	res, err := w.tx.ExecContext(ctx, query)
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
	if t.WebhookRoutes != nil {
		if err = p.replaceTriggerRoutes(ctx, w, id, routes); err != nil {
			return nil, err
		}
	}
	if err = w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit trigger update: %w", err)
	}

	return p.GetTrigger(ctx, id)
}

func (p *Postgres) DeleteTrigger(ctx context.Context, id string) error {
	w, err := p.beginTriggerWrite(ctx, id)
	if err != nil {
		return err
	}
	defer w.tx.Rollback()
	query, _, err := p.goqu.Delete(p.tableTriggers).
		Where(w.predicate, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete trigger query: %w", err)
	}

	_, err = w.tx.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete trigger %q: %w", id, err)
	}
	if _, err := w.tx.Delete(p.workspaceTable("webhook_deliveries")).Where(goqu.Ex{"workspace_id": w.actor.WorkspaceID, "trigger_id": id}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("delete trigger deliveries: %w", err)
	}

	return w.tx.Commit()
}

func (p *Postgres) ListEnabledCronTriggers(ctx context.Context) ([]service.Trigger, error) {
	scope, err := p.businessReadScope(ctx, p.tableTriggers)
	if err != nil {
		return nil, err
	}
	query, _, err := p.goqu.From(p.tableTriggers).
		Select(triggerSelectColumnsAny()...).
		Where(
			scope,
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
		row, err := scanTriggerRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan trigger row: %w", err)
		}

		t, err := p.triggerRowToRecord(*row)
		if err != nil {
			return nil, err
		}
		result = append(result, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, p.attachTriggerRoutes(ctx, result)
}

// triggerRowToRecord converts a database row to a Trigger.
func (p *Postgres) triggerRowToRecord(row triggerRow) (*service.Trigger, error) {
	var cfg map[string]any
	if err := json.Unmarshal(row.Config, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal trigger config for %q: %w", row.ID, err)
	}

	alias := ""
	if row.Alias.Valid {
		alias = row.Alias.String
	}

	workflowID := ""
	if row.WorkflowID.Valid {
		workflowID = row.WorkflowID.String
	}

	targetType := row.TargetType
	if targetType == "" {
		targetType = service.TriggerTargetWorkflow
	}

	targetID := row.TargetID
	if targetID == "" {
		targetID = workflowID
	}

	return &service.Trigger{
		WorkspaceID: row.WorkspaceID,
		ID:          row.ID,
		WorkflowID:  workflowID,
		TargetType:  targetType,
		TargetID:    targetID,
		EntryNodeID: row.EntryNodeID,
		Type:        row.Type,
		Config:      cfg,
		Alias:       alias,
		Public:      row.Public,
		Enabled:     row.Enabled,
		CreatedAt:   row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   row.UpdatedAt.Format(time.RFC3339),
		CreatedBy:   row.CreatedBy,
		UpdatedBy:   row.UpdatedBy,

		HideFromMain:  row.HideMain,
		Signature:     p.storedTriggerSignature(row.Secret).Redacted(),
		WebhookRoutes: []service.WebhookRoute{},
	}, nil
}
