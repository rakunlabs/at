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
	"github.com/rakunlabs/query"
)

type projectRow struct {
	ID             string         `db:"id"`
	OrganizationID sql.NullString `db:"organization_id"`
	GoalID         sql.NullString `db:"goal_id"`
	LeadAgentID    sql.NullString `db:"lead_agent_id"`
	Name           string         `db:"name"`
	Description    sql.NullString `db:"description"`
	Status         string         `db:"status"`
	Color          sql.NullString `db:"color"`
	TargetDate     sql.NullString `db:"target_date"`
	ArchivedAt     sql.NullString `db:"archived_at"`
	CreatedAt      string         `db:"created_at"`
	UpdatedAt      string         `db:"updated_at"`
	CreatedBy      sql.NullString `db:"created_by"`
	UpdatedBy      sql.NullString `db:"updated_by"`
}

var projectColumns = []interface{}{"id", "organization_id", "goal_id", "lead_agent_id", "name", "description", "status", "color", "target_date", "archived_at", "created_at", "updated_at", "created_by", "updated_by"}

func scanProjectRow(scanner interface{ Scan(dest ...any) error }) (projectRow, error) {
	var row projectRow
	err := scanner.Scan(&row.ID, &row.OrganizationID, &row.GoalID, &row.LeadAgentID, &row.Name, &row.Description, &row.Status, &row.Color, &row.TargetDate, &row.ArchivedAt, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)

	return row, err
}

func (s *SQLite) ListProjects(ctx context.Context, q *query.Query) (*service.ListResult[service.Project], error) {
	sql, total, err := s.buildListQuery(ctx, s.tableProjects, q, projectColumns...)
	if err != nil {
		return nil, fmt.Errorf("build list projects query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var items []service.Project
	for rows.Next() {
		row, err := scanProjectRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan project row: %w", err)
		}

		items = append(items, projectRowToRecord(row))
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.Project]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (s *SQLite) GetProject(ctx context.Context, id string) (*service.Project, error) {
	query, _, err := s.goqu.From(s.tableProjects).
		Select(projectColumns...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get project query: %w", err)
	}

	row, err := scanProjectRow(s.db.QueryRowContext(ctx, query))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get project %q: %w", id, err)
	}

	project := projectRowToRecord(row)

	return &project, nil
}

func (s *SQLite) CreateProject(ctx context.Context, project service.Project) (*service.Project, error) {
	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := s.goqu.Insert(s.tableProjects).Rows(
		goqu.Record{
			"id":              id,
			"organization_id": project.OrganizationID,
			"goal_id":         project.GoalID,
			"lead_agent_id":   project.LeadAgentID,
			"name":            project.Name,
			"description":     project.Description,
			"status":          project.Status,
			"color":           project.Color,
			"target_date":     project.TargetDate,
			"archived_at":     project.ArchivedAt,
			"created_at":      now.Format(time.RFC3339),
			"updated_at":      now.Format(time.RFC3339),
			"created_by":      project.CreatedBy,
			"updated_by":      project.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert project query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create project %q: %w", project.Name, err)
	}

	return &service.Project{
		ID:             id,
		OrganizationID: project.OrganizationID,
		GoalID:         project.GoalID,
		LeadAgentID:    project.LeadAgentID,
		Name:           project.Name,
		Description:    project.Description,
		Status:         project.Status,
		Color:          project.Color,
		TargetDate:     project.TargetDate,
		ArchivedAt:     project.ArchivedAt,
		CreatedAt:      now.Format(time.RFC3339),
		UpdatedAt:      now.Format(time.RFC3339),
		CreatedBy:      project.CreatedBy,
		UpdatedBy:      project.UpdatedBy,
	}, nil
}

func (s *SQLite) UpdateProject(ctx context.Context, id string, project service.Project) (*service.Project, error) {
	now := time.Now().UTC()

	query, _, err := s.goqu.Update(s.tableProjects).Set(
		goqu.Record{
			"organization_id": project.OrganizationID,
			"goal_id":         project.GoalID,
			"lead_agent_id":   project.LeadAgentID,
			"name":            project.Name,
			"description":     project.Description,
			"status":          project.Status,
			"color":           project.Color,
			"target_date":     project.TargetDate,
			"archived_at":     project.ArchivedAt,
			"updated_at":      now.Format(time.RFC3339),
			"updated_by":      project.UpdatedBy,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update project query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update project %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return s.GetProject(ctx, id)
}

func (s *SQLite) DeleteProject(ctx context.Context, id string) error {
	query, _, err := s.goqu.Delete(s.tableProjects).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete project query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete project %q: %w", id, err)
	}

	return nil
}

func (s *SQLite) ListProjectsByGoal(ctx context.Context, goalID string) ([]service.Project, error) {
	query, _, err := s.goqu.From(s.tableProjects).
		Select(projectColumns...).
		Where(goqu.I("goal_id").Eq(goalID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list projects by goal query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list projects by goal %q: %w", goalID, err)
	}
	defer rows.Close()

	var items []service.Project
	for rows.Next() {
		row, err := scanProjectRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan project row: %w", err)
		}

		items = append(items, projectRowToRecord(row))
	}

	return items, rows.Err()
}

func (s *SQLite) ListProjectsByOrganization(ctx context.Context, orgID string) ([]service.Project, error) {
	query, _, err := s.goqu.From(s.tableProjects).
		Select(projectColumns...).
		Where(goqu.I("organization_id").Eq(orgID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list projects by organization query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list projects by organization %q: %w", orgID, err)
	}
	defer rows.Close()

	var items []service.Project
	for rows.Next() {
		row, err := scanProjectRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan project row: %w", err)
		}

		items = append(items, projectRowToRecord(row))
	}

	return items, rows.Err()
}

func projectRowToRecord(row projectRow) service.Project {
	return service.Project{
		ID:             row.ID,
		OrganizationID: row.OrganizationID.String,
		GoalID:         row.GoalID.String,
		LeadAgentID:    row.LeadAgentID.String,
		Name:           row.Name,
		Description:    row.Description.String,
		Status:         row.Status,
		Color:          row.Color.String,
		TargetDate:     row.TargetDate.String,
		ArchivedAt:     row.ArchivedAt.String,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
		CreatedBy:      row.CreatedBy.String,
		UpdatedBy:      row.UpdatedBy.String,
	}
}
