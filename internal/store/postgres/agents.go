package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/doug-martin/goqu/v9/exp"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
	"github.com/rakunlabs/query/adapter/adaptergoqu"
	"github.com/worldline-go/types"
)

// ─── Agent CRUD ───

type agentRow struct {
	ID          string         `db:"id"`
	WorkspaceID string         `db:"workspace_id"`
	OwnerUserID string         `db:"owner_user_id"`
	Name        string         `db:"name"`
	Config      types.RawJSON  `db:"config"`
	CreatedAt   time.Time      `db:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at"`
	CreatedBy   sql.NullString `db:"created_by"`
	UpdatedBy   sql.NullString `db:"updated_by"`
}

// agentGlobalPredicate matches Default-workspace agents explicitly shared
// with every workspace (the provider `shared_with_all_workspaces` pattern).
// A personal agent can never be global, which the create/update guards
// enforce; the predicate re-states it so a bad row cannot widen visibility.
func agentGlobalPredicate() exp.Expression {
	return goqu.And(
		goqu.C("workspace_id").Eq(service.DefaultWorkspaceID),
		goqu.C("owner_user_id").Eq(""),
		goqu.L("config->>'shared_with_all_workspaces'").Eq("true"),
	)
}

// agentVisibilityScope is the read predicate for the three agent tiers:
// workspace agents (owner_user_id = ”) plus the caller's own personal
// agents inside the capability-scoped workspace, unioned with globally
// shared Default-workspace agents. Platform administrators additionally see
// every personal agent in the selected workspace.
func (p *Postgres) agentVisibilityScope(ctx context.Context) (exp.Expression, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	base, err := businessPredicate(a, "agents.read", "id")
	if err != nil {
		return nil, err
	}
	visible := base
	if !a.PlatformAdmin {
		visible = goqu.And(base, goqu.Or(
			goqu.C("owner_user_id").Eq(""),
			goqu.C("owner_user_id").Eq(a.UserID),
		))
	}
	return goqu.Or(visible, agentGlobalPredicate()), nil
}

func (p *Postgres) ListAgents(ctx context.Context, q *query.Query) (*service.ListResult[service.Agent], error) {
	scope, err := p.agentVisibilityScope(ctx)
	if err != nil {
		return nil, err
	}
	ds := p.goqu.From(p.tableAgents).Where(scope)

	countDs := ds
	if q != nil {
		if exprs := adaptergoqu.Expression(q); len(exprs) > 0 {
			countDs = countDs.Where(exprs...)
		}
	}
	countSQL, _, err := countDs.Select(goqu.COUNT("*")).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build count agents query: %w", err)
	}
	var total uint64
	if err := p.db.QueryRowContext(ctx, countSQL).Scan(&total); err != nil {
		return nil, fmt.Errorf("count agents: %w", err)
	}

	ds = adaptergoqu.Select(q, ds, adaptergoqu.WithParameterized(false))
	sql, _, err := ds.Select("id", "name", "config", "created_at", "updated_at", "created_by", "updated_by", "workspace_id", "owner_user_id").ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list agents query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()

	var items []service.Agent
	for rows.Next() {
		var row agentRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Config, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy, &row.WorkspaceID, &row.OwnerUserID); err != nil {
			return nil, fmt.Errorf("scan agent row: %w", err)
		}

		agent, err := agentRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *agent)
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.Agent]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (p *Postgres) GetAgent(ctx context.Context, id string) (*service.Agent, error) {
	scope, err := p.agentVisibilityScope(ctx)
	if err != nil {
		return nil, err
	}
	query, _, err := p.goqu.From(p.tableAgents).
		Select("id", "name", "config", "created_at", "updated_at", "created_by", "updated_by", "workspace_id", "owner_user_id").
		Where(scope, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get agent query: %w", err)
	}

	var row agentRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Config, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy, &row.WorkspaceID, &row.OwnerUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get agent %q: %w", id, err)
	}

	return agentRowToRecord(row)
}

func (p *Postgres) CreateAgent(ctx context.Context, agent service.Agent) (*service.Agent, error) {
	w, err := p.beginBusinessWrite(ctx, p.tableAgents, "agents.write", "")
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()
	if agent.WorkspaceID != "" && agent.WorkspaceID != w.actor.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	if err := agentOwnershipWriteGuard(w.actor, agent.OwnerUserID, agent.Config); err != nil {
		return nil, err
	}
	if err = p.agentReferences(ctx, w, agent.Config, agent.OwnerUserID != ""); err != nil {
		return nil, err
	}
	configJSON, err := json.Marshal(agent.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal agent config: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := w.tx.Insert(p.tableAgents).Rows(
		goqu.Record{
			"id":            id,
			"workspace_id":  w.actor.WorkspaceID,
			"owner_user_id": agent.OwnerUserID,
			"name":          agent.Name,
			"config":        types.RawJSON(configJSON),
			"created_at":    now,
			"updated_at":    now,
			"created_by":    agent.CreatedBy,
			"updated_by":    agent.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert agent query: %w", err)
	}

	if _, err := w.tx.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create agent %q: %w", agent.Name, err)
	}
	if err = w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit agent: %w", err)
	}

	return &service.Agent{
		ID:          id,
		WorkspaceID: w.actor.WorkspaceID,
		OwnerUserID: agent.OwnerUserID,
		Scope:       service.DeriveAgentScope(service.Agent{OwnerUserID: agent.OwnerUserID, Config: agent.Config}),
		Name:        agent.Name,
		Config:      agent.Config,
		CreatedAt:   now.Format(time.RFC3339),
		UpdatedAt:   now.Format(time.RFC3339),
		CreatedBy:   agent.CreatedBy,
		UpdatedBy:   agent.UpdatedBy,
	}, nil
}

// agentOwnershipWriteGuard bounds who may write which agent tier. Personal
// agents belong to their owner (platform administrators may act on any); a
// globally shared agent is Default-workspace platform administration, the
// same rule providers use; and the two tiers are mutually exclusive.
func agentOwnershipWriteGuard(actor service.AccessPrincipal, ownerUserID string, cfg service.AgentConfig) error {
	if ownerUserID != "" && ownerUserID != actor.UserID && !actor.PlatformAdmin {
		return service.ErrAccessDenied
	}
	if ownerUserID != "" && cfg.SharedWithAllWorkspaces {
		return fmt.Errorf("a personal agent cannot be shared with all workspaces: %w", service.ErrAccessDenied)
	}
	if cfg.SharedWithAllWorkspaces && (!actor.PlatformAdmin || actor.WorkspaceID != service.DefaultWorkspaceID) {
		return fmt.Errorf("only a platform administrator can share agents from the Default workspace: %w", service.ErrAccessDenied)
	}
	return nil
}

// lockAgentForWrite loads and row-locks the agent inside the write
// transaction and re-checks tier authority against the *stored* row, so a
// member can neither edit another account's personal agent nor touch a
// globally shared one.
func (p *Postgres) lockAgentForWrite(ctx context.Context, w *businessWrite, table interface{}, id string) (string, bool, error) {
	var current struct {
		OwnerUserID string        `db:"owner_user_id"`
		Config      types.RawJSON `db:"config"`
	}
	found, err := w.tx.From(table).
		Select("owner_user_id", "config").
		Where(w.predicate, goqu.I("id").Eq(id)).
		ForUpdate(goqu.Wait).
		ScanStructContext(ctx, &current)
	if err != nil {
		return "", false, fmt.Errorf("lock agent %q: %w", id, err)
	}
	if !found {
		return "", false, nil
	}
	var cfg service.AgentConfig
	if len(current.Config) > 0 {
		if err := json.Unmarshal(current.Config, &cfg); err != nil {
			return "", false, fmt.Errorf("unmarshal agent config for %q: %w", id, err)
		}
	}
	if err := agentOwnershipWriteGuard(w.actor, current.OwnerUserID, cfg); err != nil {
		return "", false, err
	}
	return current.OwnerUserID, true, nil
}

func (p *Postgres) UpdateAgent(ctx context.Context, id string, agent service.Agent) (*service.Agent, error) {
	w, err := p.beginBusinessWrite(ctx, p.tableAgents, "agents.write", id)
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()
	if agent.WorkspaceID != "" && agent.WorkspaceID != w.actor.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	currentOwner, found, err := p.lockAgentForWrite(ctx, w, p.tableAgents, id)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	// The incoming config decides the *new* shared flag; owner_user_id is
	// never rewritten (there is no tier conversion), so the incoming config
	// is guarded against the stored owner.
	if err := agentOwnershipWriteGuard(w.actor, currentOwner, agent.Config); err != nil {
		return nil, err
	}
	if err = p.agentReferences(ctx, w, agent.Config, currentOwner != ""); err != nil {
		return nil, err
	}
	configJSON, err := json.Marshal(agent.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal agent config: %w", err)
	}

	now := time.Now().UTC()

	query, _, err := w.tx.Update(p.tableAgents).Set(
		goqu.Record{
			"name":       agent.Name,
			"config":     types.RawJSON(configJSON),
			"updated_at": now,
			"updated_by": agent.UpdatedBy,
		},
	).Where(w.predicate, goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update agent query: %w", err)
	}

	res, err := w.tx.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update agent %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}
	if err = w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit agent update: %w", err)
	}

	return p.GetAgent(ctx, id)
}

func (p *Postgres) DeleteAgent(ctx context.Context, id string) error {
	w, err := p.beginBusinessWrite(ctx, p.tableAgents, "agents.write", id)
	if err != nil {
		return err
	}
	defer w.tx.Rollback()
	_, found, err := p.lockAgentForWrite(ctx, w, p.tableAgents, id)
	if err != nil {
		return err
	}
	if !found {
		return w.tx.Commit()
	}
	query, _, err := w.tx.Delete(p.tableAgents).
		Where(w.predicate, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete agent query: %w", err)
	}

	_, err = w.tx.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete agent %q: %w", id, err)
	}

	return w.tx.Commit()
}

func agentRowToRecord(row agentRow) (*service.Agent, error) {
	var cfg service.AgentConfig
	if len(row.Config) > 0 {
		if err := json.Unmarshal(row.Config, &cfg); err != nil {
			return nil, fmt.Errorf("unmarshal agent config for %q: %w", row.ID, err)
		}
	}

	agent := service.Agent{
		ID:          row.ID,
		WorkspaceID: row.WorkspaceID,
		OwnerUserID: row.OwnerUserID,
		Name:        row.Name,
		Config:      cfg,
		CreatedAt:   row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   row.UpdatedAt.Format(time.RFC3339),
		CreatedBy:   row.CreatedBy.String,
		UpdatedBy:   row.UpdatedBy.String,
	}
	agent.Scope = service.DeriveAgentScope(agent)
	return &agent, nil
}
