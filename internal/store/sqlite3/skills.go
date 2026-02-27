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

// ─── Skill CRUD ───

type skillRow struct {
	ID           string `db:"id"`
	Name         string `db:"name"`
	Description  string `db:"description"`
	SystemPrompt string `db:"system_prompt"`
	Tools        string `db:"tools"`
	CreatedAt    string `db:"created_at"`
	UpdatedAt    string `db:"updated_at"`
	CreatedBy    string `db:"created_by"`
	UpdatedBy    string `db:"updated_by"`
}

func (s *SQLite) ListSkills(ctx context.Context) ([]service.Skill, error) {
	query, _, err := s.goqu.From(s.tableSkills).
		Select("id", "name", "description", "system_prompt", "tools", "created_at", "updated_at", "created_by", "updated_by").
		Order(goqu.I("name").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list skills query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	defer rows.Close()

	var result []service.Skill
	for rows.Next() {
		var row skillRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Description, &row.SystemPrompt, &row.Tools, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy); err != nil {
			return nil, fmt.Errorf("scan skill row: %w", err)
		}

		sk, err := skillRowToRecord(row)
		if err != nil {
			return nil, err
		}
		result = append(result, *sk)
	}

	return result, rows.Err()
}

func (s *SQLite) GetSkill(ctx context.Context, id string) (*service.Skill, error) {
	query, _, err := s.goqu.From(s.tableSkills).
		Select("id", "name", "description", "system_prompt", "tools", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get skill query: %w", err)
	}

	var row skillRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Description, &row.SystemPrompt, &row.Tools, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get skill %q: %w", id, err)
	}

	return skillRowToRecord(row)
}

func (s *SQLite) GetSkillByName(ctx context.Context, name string) (*service.Skill, error) {
	query, _, err := s.goqu.From(s.tableSkills).
		Select("id", "name", "description", "system_prompt", "tools", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("name").Eq(name)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get skill by name query: %w", err)
	}

	var row skillRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Description, &row.SystemPrompt, &row.Tools, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get skill by name %q: %w", name, err)
	}

	return skillRowToRecord(row)
}

func (s *SQLite) CreateSkill(ctx context.Context, sk service.Skill) (*service.Skill, error) {
	toolsJSON, err := json.Marshal(sk.Tools)
	if err != nil {
		return nil, fmt.Errorf("marshal skill tools: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := s.goqu.Insert(s.tableSkills).Rows(
		goqu.Record{
			"id":            id,
			"name":          sk.Name,
			"description":   sk.Description,
			"system_prompt": sk.SystemPrompt,
			"tools":         string(toolsJSON),
			"created_at":    now.Format(time.RFC3339),
			"updated_at":    now.Format(time.RFC3339),
			"created_by":    sk.CreatedBy,
			"updated_by":    sk.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert skill query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create skill %q: %w", sk.Name, err)
	}

	return &service.Skill{
		ID:           id,
		Name:         sk.Name,
		Description:  sk.Description,
		SystemPrompt: sk.SystemPrompt,
		Tools:        sk.Tools,
		CreatedAt:    now.Format(time.RFC3339),
		UpdatedAt:    now.Format(time.RFC3339),
		CreatedBy:    sk.CreatedBy,
		UpdatedBy:    sk.UpdatedBy,
	}, nil
}

func (s *SQLite) UpdateSkill(ctx context.Context, id string, sk service.Skill) (*service.Skill, error) {
	toolsJSON, err := json.Marshal(sk.Tools)
	if err != nil {
		return nil, fmt.Errorf("marshal skill tools: %w", err)
	}

	now := time.Now().UTC()

	query, _, err := s.goqu.Update(s.tableSkills).Set(
		goqu.Record{
			"name":          sk.Name,
			"description":   sk.Description,
			"system_prompt": sk.SystemPrompt,
			"tools":         string(toolsJSON),
			"updated_at":    now.Format(time.RFC3339),
			"updated_by":    sk.UpdatedBy,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update skill query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update skill %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return s.GetSkill(ctx, id)
}

func (s *SQLite) DeleteSkill(ctx context.Context, id string) error {
	query, _, err := s.goqu.Delete(s.tableSkills).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete skill query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete skill %q: %w", id, err)
	}

	return nil
}

// skillRowToRecord converts a database row to a Skill.
func skillRowToRecord(row skillRow) (*service.Skill, error) {
	var tools []service.Tool
	if err := json.Unmarshal([]byte(row.Tools), &tools); err != nil {
		return nil, fmt.Errorf("unmarshal skill tools for %q: %w", row.ID, err)
	}

	return &service.Skill{
		ID:           row.ID,
		Name:         row.Name,
		Description:  row.Description,
		SystemPrompt: row.SystemPrompt,
		Tools:        tools,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
		CreatedBy:    row.CreatedBy,
		UpdatedBy:    row.UpdatedBy,
	}, nil
}
