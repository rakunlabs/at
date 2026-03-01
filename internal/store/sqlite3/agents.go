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

type agentRow struct {
	ID            string         `db:"id"`
	Name          string         `db:"name"`
	Description   sql.NullString `db:"description"`
	Provider      string         `db:"provider"`
	Model         sql.NullString `db:"model"`
	SystemPrompt  sql.NullString `db:"system_prompt"`
	Skills        sql.NullString `db:"skills"`
	MCPs          sql.NullString `db:"mcp_urls"`
	MaxIterations int            `db:"max_iterations"`
	ToolTimeout   int            `db:"tool_timeout"`
	CreatedAt     string         `db:"created_at"`
	UpdatedAt     string         `db:"updated_at"`
	CreatedBy     sql.NullString `db:"created_by"`
	UpdatedBy     sql.NullString `db:"updated_by"`
}

func (s *SQLite) ListAgents(ctx context.Context) ([]service.Agent, error) {
	query, _, err := s.goqu.From(s.tableAgents).
		Select("id", "name", "description", "provider", "model", "system_prompt", "skills", "mcp_urls", "max_iterations", "tool_timeout", "created_at", "updated_at", "created_by", "updated_by").
		Order(goqu.I("name").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list agents query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()

	var result []service.Agent
	for rows.Next() {
		var row agentRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Description, &row.Provider, &row.Model, &row.SystemPrompt, &row.Skills, &row.MCPs, &row.MaxIterations, &row.ToolTimeout, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy); err != nil {
			return nil, fmt.Errorf("scan agent row: %w", err)
		}

		agent, err := agentRowToRecord(row)
		if err != nil {
			return nil, err
		}
		result = append(result, *agent)
	}

	return result, rows.Err()
}

func (s *SQLite) GetAgent(ctx context.Context, id string) (*service.Agent, error) {
	query, _, err := s.goqu.From(s.tableAgents).
		Select("id", "name", "description", "provider", "model", "system_prompt", "skills", "mcp_urls", "max_iterations", "tool_timeout", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get agent query: %w", err)
	}

	var row agentRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Description, &row.Provider, &row.Model, &row.SystemPrompt, &row.Skills, &row.MCPs, &row.MaxIterations, &row.ToolTimeout, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get agent %q: %w", id, err)
	}

	return agentRowToRecord(row)
}

func (s *SQLite) CreateAgent(ctx context.Context, agent service.Agent) (*service.Agent, error) {
	skillsJSON, err := json.Marshal(agent.Skills)
	if err != nil {
		return nil, fmt.Errorf("marshal agent skills: %w", err)
	}
	mcpsJSON, err := json.Marshal(agent.MCPs)
	if err != nil {
		return nil, fmt.Errorf("marshal agent mcps: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := s.goqu.Insert(s.tableAgents).Rows(
		goqu.Record{
			"id":             id,
			"name":           agent.Name,
			"description":    agent.Description,
			"provider":       agent.Provider,
			"model":          agent.Model,
			"system_prompt":  agent.SystemPrompt,
			"skills":         string(skillsJSON),
			"mcp_urls":       string(mcpsJSON),
			"max_iterations": agent.MaxIterations,
			"tool_timeout":   agent.ToolTimeout,
			"created_at":     now.Format(time.RFC3339),
			"updated_at":     now.Format(time.RFC3339),
			"created_by":     agent.CreatedBy,
			"updated_by":     agent.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert agent query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create agent %q: %w", agent.Name, err)
	}

	return &service.Agent{
		ID:            id,
		Name:          agent.Name,
		Description:   agent.Description,
		Provider:      agent.Provider,
		Model:         agent.Model,
		SystemPrompt:  agent.SystemPrompt,
		Skills:        agent.Skills,
		MCPs:          agent.MCPs,
		MaxIterations: agent.MaxIterations,
		ToolTimeout:   agent.ToolTimeout,
		CreatedAt:     now.Format(time.RFC3339),
		UpdatedAt:     now.Format(time.RFC3339),
		CreatedBy:     agent.CreatedBy,
		UpdatedBy:     agent.UpdatedBy,
	}, nil
}

func (s *SQLite) UpdateAgent(ctx context.Context, id string, agent service.Agent) (*service.Agent, error) {
	skillsJSON, err := json.Marshal(agent.Skills)
	if err != nil {
		return nil, fmt.Errorf("marshal agent skills: %w", err)
	}
	mcpsJSON, err := json.Marshal(agent.MCPs)
	if err != nil {
		return nil, fmt.Errorf("marshal agent mcps: %w", err)
	}

	now := time.Now().UTC()

	query, _, err := s.goqu.Update(s.tableAgents).Set(
		goqu.Record{
			"name":           agent.Name,
			"description":    agent.Description,
			"provider":       agent.Provider,
			"model":          agent.Model,
			"system_prompt":  agent.SystemPrompt,
			"skills":         string(skillsJSON),
			"mcp_urls":       string(mcpsJSON),
			"max_iterations": agent.MaxIterations,
			"tool_timeout":   agent.ToolTimeout,
			"updated_at":     now.Format(time.RFC3339),
			"updated_by":     agent.UpdatedBy,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update agent query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
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

	return s.GetAgent(ctx, id)
}

func (s *SQLite) DeleteAgent(ctx context.Context, id string) error {
	query, _, err := s.goqu.Delete(s.tableAgents).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete agent query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete agent %q: %w", id, err)
	}

	return nil
}

func agentRowToRecord(row agentRow) (*service.Agent, error) {
	var skills []string
	if row.Skills.Valid && row.Skills.String != "" {
		if err := json.Unmarshal([]byte(row.Skills.String), &skills); err != nil {
			return nil, fmt.Errorf("unmarshal agent skills for %q: %w", row.ID, err)
		}
	} else {
		skills = []string{}
	}

	var mcps []string
	if row.MCPs.Valid && row.MCPs.String != "" {
		if err := json.Unmarshal([]byte(row.MCPs.String), &mcps); err != nil {
			return nil, fmt.Errorf("unmarshal agent mcps for %q: %w", row.ID, err)
		}
	} else {
		mcps = []string{}
	}

	return &service.Agent{
		ID:            row.ID,
		Name:          row.Name,
		Description:   row.Description.String,
		Provider:      row.Provider,
		Model:         row.Model.String,
		SystemPrompt:  row.SystemPrompt.String,
		Skills:        skills,
		MCPs:          mcps,
		MaxIterations: row.MaxIterations,
		ToolTimeout:   row.ToolTimeout,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
		CreatedBy:     row.CreatedBy.String,
		UpdatedBy:     row.UpdatedBy.String,
	}, nil
}
