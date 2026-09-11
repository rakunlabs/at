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
	"github.com/rakunlabs/query"
	"github.com/worldline-go/types"
)

// ─── Agent CRUD ───

type agentRow struct {
	ID          string         `db:"id"`
	WorkspaceID string         `db:"workspace_id"`
	Name        string         `db:"name"`
	Config      types.RawJSON  `db:"config"`
	CreatedAt   time.Time      `db:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at"`
	CreatedBy   sql.NullString `db:"created_by"`
	UpdatedBy   sql.NullString `db:"updated_by"`
}

func (p *Postgres) ListAgents(ctx context.Context, q *query.Query) (*service.ListResult[service.Agent], error) {
	sql, total, err := p.buildListQuery(ctx, p.tableAgents, q, "id", "name", "config", "created_at", "updated_at", "created_by", "updated_by", "workspace_id")
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
		if err := rows.Scan(&row.ID, &row.Name, &row.Config, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy, &row.WorkspaceID); err != nil {
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
	scope, err := p.businessReadScope(ctx, p.tableAgents)
	if err != nil {
		return nil, err
	}
	query, _, err := p.goqu.From(p.tableAgents).
		Select("id", "name", "config", "created_at", "updated_at", "created_by", "updated_by", "workspace_id").
		Where(scope, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get agent query: %w", err)
	}

	var row agentRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Config, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy, &row.WorkspaceID)
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
	if err = p.agentReferences(ctx, w, agent.Config); err != nil {
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
			"id":           id,
			"workspace_id": w.actor.WorkspaceID,
			"name":         agent.Name,
			"config":       types.RawJSON(configJSON),
			"created_at":   now,
			"updated_at":   now,
			"created_by":   agent.CreatedBy,
			"updated_by":   agent.UpdatedBy,
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
		Name:        agent.Name,
		Config:      agent.Config,
		CreatedAt:   now.Format(time.RFC3339),
		UpdatedAt:   now.Format(time.RFC3339),
		CreatedBy:   agent.CreatedBy,
		UpdatedBy:   agent.UpdatedBy,
	}, nil
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
	if err = p.agentReferences(ctx, w, agent.Config); err != nil {
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

	return &service.Agent{
		ID:          row.ID,
		WorkspaceID: row.WorkspaceID,
		Name:        row.Name,
		Config:      cfg,
		CreatedAt:   row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   row.UpdatedAt.Format(time.RFC3339),
		CreatedBy:   row.CreatedBy.String,
		UpdatedBy:   row.UpdatedBy.String,
	}, nil
}
