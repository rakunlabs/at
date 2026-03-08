package sqlite3

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

func (s *SQLite) scanOrgAgentRow(scanner interface{ Scan(...any) error }) (orgAgentRow, error) {
	var row orgAgentRow
	err := scanner.Scan(
		&row.ID, &row.OrganizationID, &row.AgentID,
		&row.Role, &row.Title, &row.ParentAgentID,
		&row.Status, &row.HeartbeatSchedule, &row.CreatedAt, &row.UpdatedAt,
	)

	return row, err
}

var orgAgentCols = []any{"id", "organization_id", "agent_id", "role", "title", "parent_agent_id", "status", "heartbeat_schedule", "created_at", "updated_at"}

func (s *SQLite) ListOrganizationAgents(ctx context.Context, orgID string) ([]service.OrganizationAgent, error) {
	q, _, err := s.goqu.From(s.tableOrganizationAgents).
		Select(orgAgentCols...).
		Where(goqu.I("organization_id").Eq(orgID)).
		Order(goqu.I("created_at").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list organization agents query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list organization agents: %w", err)
	}
	defer rows.Close()

	var items []service.OrganizationAgent
	for rows.Next() {
		row, err := s.scanOrgAgentRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan organization agent row: %w", err)
		}

		items = append(items, orgAgentRowToRecord(row))
	}

	return items, rows.Err()
}

func (s *SQLite) ListAgentOrganizations(ctx context.Context, agentID string) ([]service.OrganizationAgent, error) {
	q, _, err := s.goqu.From(s.tableOrganizationAgents).
		Select(orgAgentCols...).
		Where(goqu.I("agent_id").Eq(agentID)).
		Order(goqu.I("created_at").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list agent organizations query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list agent organizations: %w", err)
	}
	defer rows.Close()

	var items []service.OrganizationAgent
	for rows.Next() {
		row, err := s.scanOrgAgentRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan organization agent row: %w", err)
		}

		items = append(items, orgAgentRowToRecord(row))
	}

	return items, rows.Err()
}

func (s *SQLite) GetOrganizationAgent(ctx context.Context, id string) (*service.OrganizationAgent, error) {
	q, _, err := s.goqu.From(s.tableOrganizationAgents).
		Select(orgAgentCols...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get organization agent query: %w", err)
	}

	row, err := s.scanOrgAgentRow(s.db.QueryRowContext(ctx, q))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get organization agent %q: %w", id, err)
	}

	rec := orgAgentRowToRecord(row)

	return &rec, nil
}

func (s *SQLite) GetOrganizationAgentByPair(ctx context.Context, orgID, agentID string) (*service.OrganizationAgent, error) {
	q, _, err := s.goqu.From(s.tableOrganizationAgents).
		Select(orgAgentCols...).
		Where(goqu.I("organization_id").Eq(orgID), goqu.I("agent_id").Eq(agentID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get organization agent by pair query: %w", err)
	}

	row, err := s.scanOrgAgentRow(s.db.QueryRowContext(ctx, q))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get organization agent pair (%q, %q): %w", orgID, agentID, err)
	}

	rec := orgAgentRowToRecord(row)

	return &rec, nil
}

func (s *SQLite) CreateOrganizationAgent(ctx context.Context, oa service.OrganizationAgent) (*service.OrganizationAgent, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	status := oa.Status
	if status == "" {
		status = "active"
	}

	q, _, err := s.goqu.Insert(s.tableOrganizationAgents).Rows(
		goqu.Record{
			"id":                 id,
			"organization_id":    oa.OrganizationID,
			"agent_id":           oa.AgentID,
			"role":               oa.Role,
			"title":              oa.Title,
			"parent_agent_id":    oa.ParentAgentID,
			"status":             status,
			"heartbeat_schedule": oa.HeartbeatSchedule,
			"created_at":         now,
			"updated_at":         now,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert organization agent query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, q); err != nil {
		return nil, fmt.Errorf("create organization agent (%q, %q): %w", oa.OrganizationID, oa.AgentID, err)
	}

	return &service.OrganizationAgent{
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

func (s *SQLite) UpdateOrganizationAgent(ctx context.Context, id string, oa service.OrganizationAgent) (*service.OrganizationAgent, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	q, _, err := s.goqu.Update(s.tableOrganizationAgents).Set(
		goqu.Record{
			"role":               oa.Role,
			"title":              oa.Title,
			"parent_agent_id":    oa.ParentAgentID,
			"status":             oa.Status,
			"heartbeat_schedule": oa.HeartbeatSchedule,
			"updated_at":         now,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update organization agent query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, q)
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

	return s.GetOrganizationAgent(ctx, id)
}

func (s *SQLite) DeleteOrganizationAgent(ctx context.Context, id string) error {
	q, _, err := s.goqu.Delete(s.tableOrganizationAgents).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete organization agent query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, q)
	if err != nil {
		return fmt.Errorf("delete organization agent %q: %w", id, err)
	}

	return nil
}

func (s *SQLite) DeleteOrganizationAgentByPair(ctx context.Context, orgID, agentID string) error {
	q, _, err := s.goqu.Delete(s.tableOrganizationAgents).
		Where(goqu.I("organization_id").Eq(orgID), goqu.I("agent_id").Eq(agentID)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete organization agent by pair query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, q)
	if err != nil {
		return fmt.Errorf("delete organization agent pair (%q, %q): %w", orgID, agentID, err)
	}

	return nil
}
