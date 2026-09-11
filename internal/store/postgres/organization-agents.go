package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
)

type orgAgentRow struct {
	WorkspaceID       string         `db:"workspace_id"`
	ID                string         `db:"id"`
	OrganizationID    string         `db:"organization_id"`
	AgentID           string         `db:"agent_id"`
	Role              sql.NullString `db:"role"`
	Title             sql.NullString `db:"title"`
	ParentAgentID     sql.NullString `db:"parent_agent_id"`
	Status            string         `db:"status"`
	HeartbeatSchedule string         `db:"heartbeat_schedule"`
	CreatedAt         string         `db:"created_at"`
	UpdatedAt         string         `db:"updated_at"`
}

func orgAgentRowToRecord(row orgAgentRow) service.OrganizationAgent {
	return service.OrganizationAgent{
		WorkspaceID:       row.WorkspaceID,
		ID:                row.ID,
		OrganizationID:    row.OrganizationID,
		AgentID:           row.AgentID,
		Role:              row.Role.String,
		Title:             row.Title.String,
		ParentAgentID:     row.ParentAgentID.String,
		Status:            row.Status,
		HeartbeatSchedule: row.HeartbeatSchedule,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}
}

func (p *Postgres) scanOrgAgentRow(scanner interface{ Scan(...any) error }) (orgAgentRow, error) {
	var row orgAgentRow
	err := scanner.Scan(
		&row.ID, &row.OrganizationID, &row.AgentID,
		&row.Role, &row.Title, &row.ParentAgentID,
		&row.Status, &row.HeartbeatSchedule,
		&row.CreatedAt, &row.UpdatedAt,
		&row.WorkspaceID,
	)

	return row, err
}

var orgAgentCols = []any{"id", "organization_id", "agent_id", "role", "title", "parent_agent_id", "status", "heartbeat_schedule", "created_at", "updated_at", "workspace_id"}

func (p *Postgres) ListOrganizationAgents(ctx context.Context, orgID string) ([]service.OrganizationAgent, error) {
	scope, err := p.orgAgentReadScope(ctx)
	if err != nil {
		return nil, err
	}
	q, _, err := p.goqu.From(p.tableOrganizationAgents).
		Select(orgAgentCols...).
		Where(scope, goqu.I("organization_id").Eq(orgID)).
		Order(goqu.I("created_at").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list organization agents query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list organization agents: %w", err)
	}
	defer rows.Close()

	var items []service.OrganizationAgent
	for rows.Next() {
		row, err := p.scanOrgAgentRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan organization agent row: %w", err)
		}

		items = append(items, orgAgentRowToRecord(row))
	}

	return items, rows.Err()
}

func (p *Postgres) ListAgentOrganizations(ctx context.Context, agentID string) ([]service.OrganizationAgent, error) {
	scope, err := p.orgAgentReadScope(ctx)
	if err != nil {
		return nil, err
	}
	q, _, err := p.goqu.From(p.tableOrganizationAgents).
		Select(orgAgentCols...).
		Where(scope, goqu.I("agent_id").Eq(agentID)).
		Order(goqu.I("created_at").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list agent organizations query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list agent organizations: %w", err)
	}
	defer rows.Close()

	var items []service.OrganizationAgent
	for rows.Next() {
		row, err := p.scanOrgAgentRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan organization agent row: %w", err)
		}

		items = append(items, orgAgentRowToRecord(row))
	}

	return items, rows.Err()
}

func (p *Postgres) GetOrganizationAgent(ctx context.Context, id string) (*service.OrganizationAgent, error) {
	scope, err := p.orgAgentReadScope(ctx)
	if err != nil {
		return nil, err
	}
	q, _, err := p.goqu.From(p.tableOrganizationAgents).
		Select(orgAgentCols...).
		Where(scope, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get organization agent query: %w", err)
	}

	row, err := p.scanOrgAgentRow(p.db.QueryRowContext(ctx, q))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get organization agent %q: %w", id, err)
	}

	rec := orgAgentRowToRecord(row)

	return &rec, nil
}

func (p *Postgres) GetOrganizationAgentByPair(ctx context.Context, orgID, agentID string) (*service.OrganizationAgent, error) {
	scope, err := p.orgAgentReadScope(ctx)
	if err != nil {
		return nil, err
	}
	q, _, err := p.goqu.From(p.tableOrganizationAgents).
		Select(orgAgentCols...).
		Where(scope, goqu.I("organization_id").Eq(orgID), goqu.I("agent_id").Eq(agentID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get organization agent by pair query: %w", err)
	}

	row, err := p.scanOrgAgentRow(p.db.QueryRowContext(ctx, q))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get organization agent pair (%q, %q): %w", orgID, agentID, err)
	}

	rec := orgAgentRowToRecord(row)

	return &rec, nil
}

func (p *Postgres) CreateOrganizationAgent(ctx context.Context, oa service.OrganizationAgent) (*service.OrganizationAgent, error) {
	w, err := p.beginBusinessWrite(ctx, p.tableOrganizationAgents, "organizations.write", oa.OrganizationID)
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()
	if oa.WorkspaceID != "" && oa.WorkspaceID != w.actor.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	if err = p.orgAgentReferences(ctx, w, oa); err != nil {
		return nil, err
	}
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	status := oa.Status
	if status == "" {
		status = "active"
	}

	q, _, err := p.goqu.Insert(p.tableOrganizationAgents).Rows(
		goqu.Record{
			"workspace_id":       w.actor.WorkspaceID,
			"id":                 id,
			"organization_id":    oa.OrganizationID,
			"agent_id":           oa.AgentID,
			"role":               nullString(oa.Role),
			"title":              nullString(oa.Title),
			"parent_agent_id":    nullString(oa.ParentAgentID),
			"status":             status,
			"heartbeat_schedule": oa.HeartbeatSchedule,
			"created_at":         now,
			"updated_at":         now,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert organization agent query: %w", err)
	}

	if _, err := w.tx.ExecContext(ctx, q); err != nil {
		return nil, fmt.Errorf("create organization agent (%q, %q): %w", oa.OrganizationID, oa.AgentID, err)
	}
	if err = w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit organization agent: %w", err)
	}

	return &service.OrganizationAgent{
		WorkspaceID:       w.actor.WorkspaceID,
		ID:                id,
		OrganizationID:    oa.OrganizationID,
		AgentID:           oa.AgentID,
		Role:              oa.Role,
		Title:             oa.Title,
		ParentAgentID:     oa.ParentAgentID,
		Status:            status,
		HeartbeatSchedule: oa.HeartbeatSchedule,
		CreatedAt:         now,
		UpdatedAt:         now,
	}, nil
}

func (p *Postgres) UpdateOrganizationAgent(ctx context.Context, id string, oa service.OrganizationAgent) (*service.OrganizationAgent, error) {
	w, err := p.beginOrgAgentWrite(ctx, id)
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()
	if oa.WorkspaceID != "" && oa.WorkspaceID != w.actor.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	if err = p.businessReference(ctx, w, p.tableAgents, "id", oa.ParentAgentID); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)

	q, _, err := p.goqu.Update(p.tableOrganizationAgents).Set(
		goqu.Record{
			"role":               nullString(oa.Role),
			"title":              nullString(oa.Title),
			"parent_agent_id":    nullString(oa.ParentAgentID),
			"status":             oa.Status,
			"heartbeat_schedule": oa.HeartbeatSchedule,
			"updated_at":         now,
		},
	).Where(w.predicate, goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update organization agent query: %w", err)
	}

	res, err := w.tx.ExecContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("update organization agent %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}
	if err = w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit organization agent update: %w", err)
	}

	return p.GetOrganizationAgent(ctx, id)
}

func (p *Postgres) DeleteOrganizationAgent(ctx context.Context, id string) error {
	w, err := p.beginOrgAgentWrite(ctx, id)
	if err != nil {
		return err
	}
	defer w.tx.Rollback()
	q, _, err := p.goqu.Delete(p.tableOrganizationAgents).
		Where(w.predicate, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete organization agent query: %w", err)
	}

	_, err = w.tx.ExecContext(ctx, q)
	if err != nil {
		return fmt.Errorf("delete organization agent %q: %w", id, err)
	}

	return w.tx.Commit()
}

func (p *Postgres) DeleteOrganizationAgentByPair(ctx context.Context, orgID, agentID string) error {
	w, err := p.beginBusinessWrite(ctx, p.tableOrganizationAgents, "organizations.write", orgID)
	if err != nil {
		return err
	}
	defer w.tx.Rollback()
	q, _, err := p.goqu.Delete(p.tableOrganizationAgents).
		Where(w.predicate, goqu.I("organization_id").Eq(orgID), goqu.I("agent_id").Eq(agentID)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete organization agent by pair query: %w", err)
	}

	_, err = w.tx.ExecContext(ctx, q)
	if err != nil {
		return fmt.Errorf("delete organization agent pair (%q, %q): %w", orgID, agentID, err)
	}

	return w.tx.Commit()
}
