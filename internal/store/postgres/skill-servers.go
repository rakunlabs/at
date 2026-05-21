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

type skillServerRow struct {
	ID          string         `db:"id"`
	Name        string         `db:"name"`
	Description string         `db:"description"`
	Mode        string         `db:"mode"`
	Skills      types.RawJSON  `db:"skills"`
	CreatedAt   time.Time      `db:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at"`
	CreatedBy   sql.NullString `db:"created_by"`
	UpdatedBy   sql.NullString `db:"updated_by"`
}

func (p *Postgres) ListSkillServers(ctx context.Context, q *query.Query) (*service.ListResult[service.SkillServer], error) {
	sql, total, err := p.buildListQuery(ctx, p.tableSkillServers, q, "id", "name", "description", "mode", "skills", "created_at", "updated_at", "created_by", "updated_by")
	if err != nil {
		return nil, fmt.Errorf("build list skill servers query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sql)
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

func (p *Postgres) GetSkillServer(ctx context.Context, id string) (*service.SkillServer, error) {
	query, _, err := p.goqu.From(p.tableSkillServers).
		Select("id", "name", "description", "mode", "skills", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get skill server query: %w", err)
	}

	var row skillServerRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Description, &row.Mode, &row.Skills, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get skill server %q: %w", id, err)
	}

	return skillServerRowToRecord(row)
}

func (p *Postgres) GetSkillServerByName(ctx context.Context, name string) (*service.SkillServer, error) {
	query, _, err := p.goqu.From(p.tableSkillServers).
		Select("id", "name", "description", "mode", "skills", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("name").Eq(name)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get skill server by name query: %w", err)
	}

	var row skillServerRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Description, &row.Mode, &row.Skills, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get skill server by name %q: %w", name, err)
	}

	return skillServerRowToRecord(row)
}

func (p *Postgres) CreateSkillServer(ctx context.Context, s service.SkillServer) (*service.SkillServer, error) {
	skillsJSON, err := json.Marshal(s.Skills)
	if err != nil {
		return nil, fmt.Errorf("marshal skill server skills: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()
	mode := s.Mode
	if mode == "" {
		mode = service.SkillServerModePackage
	}

	query, _, err := p.goqu.Insert(p.tableSkillServers).Rows(
		goqu.Record{
			"id":          id,
			"name":        s.Name,
			"description": s.Description,
			"mode":        mode,
			"skills":      types.RawJSON(skillsJSON),
			"created_at":  now,
			"updated_at":  now,
			"created_by":  s.CreatedBy,
			"updated_by":  s.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert skill server query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create skill server %q: %w", s.Name, err)
	}

	return &service.SkillServer{
		ID:          id,
		Name:        s.Name,
		Description: s.Description,
		Mode:        mode,
		Skills:      s.Skills,
		CreatedAt:   now.Format(time.RFC3339),
		UpdatedAt:   now.Format(time.RFC3339),
		CreatedBy:   s.CreatedBy,
		UpdatedBy:   s.UpdatedBy,
	}, nil
}

func (p *Postgres) UpdateSkillServer(ctx context.Context, id string, s service.SkillServer) (*service.SkillServer, error) {
	skillsJSON, err := json.Marshal(s.Skills)
	if err != nil {
		return nil, fmt.Errorf("marshal skill server skills: %w", err)
	}

	now := time.Now().UTC()
	mode := s.Mode
	if mode == "" {
		mode = service.SkillServerModePackage
	}

	query, _, err := p.goqu.Update(p.tableSkillServers).Set(
		goqu.Record{
			"name":        s.Name,
			"description": s.Description,
			"mode":        mode,
			"skills":      types.RawJSON(skillsJSON),
			"updated_at":  now,
			"updated_by":  s.UpdatedBy,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update skill server query: %w", err)
	}

	res, err := p.db.ExecContext(ctx, query)
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

	return p.GetSkillServer(ctx, id)
}

func (p *Postgres) DeleteSkillServer(ctx context.Context, id string) error {
	query, _, err := p.goqu.Delete(p.tableSkillServers).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete skill server query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete skill server %q: %w", id, err)
	}

	return nil
}

func skillServerRowToRecord(row skillServerRow) (*service.SkillServer, error) {
	var skills []string
	if len(row.Skills) > 0 {
		if err := json.Unmarshal(row.Skills, &skills); err != nil {
			return nil, fmt.Errorf("unmarshal skill server skills for %q: %w", row.ID, err)
		}
	}

	return &service.SkillServer{
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description,
		Mode:        row.Mode,
		Skills:      skills,
		CreatedAt:   row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   row.UpdatedAt.Format(time.RFC3339),
		CreatedBy:   row.CreatedBy.String,
		UpdatedBy:   row.UpdatedBy.String,
	}, nil
}
