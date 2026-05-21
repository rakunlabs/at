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
	"github.com/rakunlabs/query"
)

type skillServerRow struct {
	ID          string         `db:"id"`
	Name        string         `db:"name"`
	Description string         `db:"description"`
	Mode        string         `db:"mode"`
	Skills      string         `db:"skills"`
	CreatedAt   string         `db:"created_at"`
	UpdatedAt   string         `db:"updated_at"`
	CreatedBy   sql.NullString `db:"created_by"`
	UpdatedBy   sql.NullString `db:"updated_by"`
}

func (s *SQLite) ListSkillServers(ctx context.Context, q *query.Query) (*service.ListResult[service.SkillServer], error) {
	sql, total, err := s.buildListQuery(ctx, s.tableSkillServers, q, "id", "name", "description", "mode", "skills", "created_at", "updated_at", "created_by", "updated_by")
	if err != nil {
		return nil, fmt.Errorf("build list skill servers query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list skill servers: %w", err)
	}
	defer rows.Close()

	var items []service.SkillServer
	for rows.Next() {
		var row skillServerRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Description, &row.Mode, &row.Skills, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy); err != nil {
			return nil, fmt.Errorf("scan skill server row: %w", err)
		}

		rec, err := skillServerRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *rec)
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.SkillServer]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (s *SQLite) GetSkillServer(ctx context.Context, id string) (*service.SkillServer, error) {
	query, _, err := s.goqu.From(s.tableSkillServers).
		Select("id", "name", "description", "mode", "skills", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get skill server query: %w", err)
	}

	var row skillServerRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Description, &row.Mode, &row.Skills, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get skill server %q: %w", id, err)
	}

	return skillServerRowToRecord(row)
}

func (s *SQLite) GetSkillServerByName(ctx context.Context, name string) (*service.SkillServer, error) {
	query, _, err := s.goqu.From(s.tableSkillServers).
		Select("id", "name", "description", "mode", "skills", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("name").Eq(name)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get skill server by name query: %w", err)
	}

	var row skillServerRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Description, &row.Mode, &row.Skills, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get skill server by name %q: %w", name, err)
	}

	return skillServerRowToRecord(row)
}

func (s *SQLite) CreateSkillServer(ctx context.Context, srv service.SkillServer) (*service.SkillServer, error) {
	skillsJSON, err := json.Marshal(srv.Skills)
	if err != nil {
		return nil, fmt.Errorf("marshal skill server skills: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()
	mode := srv.Mode
	if mode == "" {
		mode = service.SkillServerModePackage
	}

	query, _, err := s.goqu.Insert(s.tableSkillServers).Rows(
		goqu.Record{
			"id":          id,
			"name":        srv.Name,
			"description": srv.Description,
			"mode":        mode,
			"skills":      string(skillsJSON),
			"created_at":  now.Format(time.RFC3339),
			"updated_at":  now.Format(time.RFC3339),
			"created_by":  srv.CreatedBy,
			"updated_by":  srv.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert skill server query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create skill server %q: %w", srv.Name, err)
	}

	return &service.SkillServer{
		ID:          id,
		Name:        srv.Name,
		Description: srv.Description,
		Mode:        mode,
		Skills:      srv.Skills,
		CreatedAt:   now.Format(time.RFC3339),
		UpdatedAt:   now.Format(time.RFC3339),
		CreatedBy:   srv.CreatedBy,
		UpdatedBy:   srv.UpdatedBy,
	}, nil
}

func (s *SQLite) UpdateSkillServer(ctx context.Context, id string, srv service.SkillServer) (*service.SkillServer, error) {
	skillsJSON, err := json.Marshal(srv.Skills)
	if err != nil {
		return nil, fmt.Errorf("marshal skill server skills: %w", err)
	}

	now := time.Now().UTC()
	mode := srv.Mode
	if mode == "" {
		mode = service.SkillServerModePackage
	}

	query, _, err := s.goqu.Update(s.tableSkillServers).Set(
		goqu.Record{
			"name":        srv.Name,
			"description": srv.Description,
			"mode":        mode,
			"skills":      string(skillsJSON),
			"updated_at":  now.Format(time.RFC3339),
			"updated_by":  srv.UpdatedBy,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update skill server query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update skill server %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return s.GetSkillServer(ctx, id)
}

func (s *SQLite) DeleteSkillServer(ctx context.Context, id string) error {
	query, _, err := s.goqu.Delete(s.tableSkillServers).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete skill server query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete skill server %q: %w", id, err)
	}

	return nil
}

func skillServerRowToRecord(row skillServerRow) (*service.SkillServer, error) {
	var skills []string
	if row.Skills != "" {
		if err := json.Unmarshal([]byte(row.Skills), &skills); err != nil {
			return nil, fmt.Errorf("unmarshal skill server skills for %q: %w", row.ID, err)
		}
	}

	return &service.SkillServer{
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description,
		Mode:        row.Mode,
		Skills:      skills,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
		CreatedBy:   row.CreatedBy.String,
		UpdatedBy:   row.UpdatedBy.String,
	}, nil
}
