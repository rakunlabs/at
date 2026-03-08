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
)

// ─── Organization CRUD ───

type orgRow struct {
	ID                   string       `db:"id"`
	Name                 string       `db:"name"`
	Description          string       `db:"description"`
	IssuePrefix          string       `db:"issue_prefix"`
	IssueCounter         int64        `db:"issue_counter"`
	BudgetMonthlyCents   int64        `db:"budget_monthly_cents"`
	SpentMonthlyCents    int64        `db:"spent_monthly_cents"`
	BudgetResetAt        sql.NullTime `db:"budget_reset_at"`
	RequireBoardApproval bool         `db:"require_board_approval_for_new_agents"`
	HeadAgentID          string       `db:"head_agent_id"`
	MaxDelegationDepth   int          `db:"max_delegation_depth"`
	CanvasLayout         string       `db:"canvas_layout"`
	CreatedAt            time.Time    `db:"created_at"`
	UpdatedAt            time.Time    `db:"updated_at"`
	CreatedBy            string       `db:"created_by"`
	UpdatedBy            string       `db:"updated_by"`
}

func (p *Postgres) ListOrganizations(ctx context.Context, q *query.Query) (*service.ListResult[service.Organization], error) {
	sql, total, err := p.buildListQuery(ctx, p.tableOrganizations, q,
		"id", "name", "description",
		"issue_prefix", "issue_counter",
		"budget_monthly_cents", "spent_monthly_cents", "budget_reset_at",
		"require_board_approval_for_new_agents",
		"head_agent_id", "max_delegation_depth",
		"canvas_layout", "created_at", "updated_at", "created_by", "updated_by",
	)
	if err != nil {
		return nil, fmt.Errorf("build list organizations query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	defer rows.Close()

	var items []service.Organization
	for rows.Next() {
		var row orgRow
		if err := rows.Scan(
			&row.ID, &row.Name, &row.Description,
			&row.IssuePrefix, &row.IssueCounter,
			&row.BudgetMonthlyCents, &row.SpentMonthlyCents, &row.BudgetResetAt,
			&row.RequireBoardApproval,
			&row.HeadAgentID, &row.MaxDelegationDepth,
			&row.CanvasLayout, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy,
		); err != nil {
			return nil, fmt.Errorf("scan organization row: %w", err)
		}

		items = append(items, *orgRowToRecord(row))
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.Organization]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (p *Postgres) GetOrganization(ctx context.Context, id string) (*service.Organization, error) {
	query, _, err := p.goqu.From(p.tableOrganizations).
		Select(
			"id", "name", "description",
			"issue_prefix", "issue_counter",
			"budget_monthly_cents", "spent_monthly_cents", "budget_reset_at",
			"require_board_approval_for_new_agents",
			"head_agent_id", "max_delegation_depth",
			"canvas_layout", "created_at", "updated_at", "created_by", "updated_by",
		).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get organization query: %w", err)
	}

	var row orgRow
	err = p.db.QueryRowContext(ctx, query).Scan(
		&row.ID, &row.Name, &row.Description,
		&row.IssuePrefix, &row.IssueCounter,
		&row.BudgetMonthlyCents, &row.SpentMonthlyCents, &row.BudgetResetAt,
		&row.RequireBoardApproval,
		&row.HeadAgentID, &row.MaxDelegationDepth,
		&row.CanvasLayout, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get organization %q: %w", id, err)
	}

	return orgRowToRecord(row), nil
}

func (p *Postgres) CreateOrganization(ctx context.Context, org service.Organization) (*service.Organization, error) {
	id := ulid.Make().String()
	now := time.Now().UTC()

	canvasLayout := string(org.CanvasLayout)
	if canvasLayout == "" {
		canvasLayout = "{}"
	}

	maxDepth := org.MaxDelegationDepth
	if maxDepth == 0 {
		maxDepth = 10
	}

	rec := goqu.Record{
		"id":                                    id,
		"name":                                  org.Name,
		"description":                           org.Description,
		"issue_prefix":                          org.IssuePrefix,
		"issue_counter":                         org.IssueCounter,
		"budget_monthly_cents":                  org.BudgetMonthlyCents,
		"spent_monthly_cents":                   org.SpentMonthlyCents,
		"require_board_approval_for_new_agents": org.RequireBoardApproval,
		"head_agent_id":                         org.HeadAgentID,
		"max_delegation_depth":                  maxDepth,
		"canvas_layout":                         canvasLayout,
		"created_at":                            now,
		"updated_at":                            now,
		"created_by":                            org.CreatedBy,
		"updated_by":                            org.UpdatedBy,
	}
	if org.BudgetResetAt != "" {
		t, err := time.Parse(time.RFC3339, org.BudgetResetAt)
		if err == nil {
			rec["budget_reset_at"] = t
		}
	}

	query, _, err := p.goqu.Insert(p.tableOrganizations).Rows(rec).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert organization query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create organization %q: %w", org.Name, err)
	}

	return &service.Organization{
		ID:                   id,
		Name:                 org.Name,
		Description:          org.Description,
		IssuePrefix:          org.IssuePrefix,
		IssueCounter:         org.IssueCounter,
		BudgetMonthlyCents:   org.BudgetMonthlyCents,
		SpentMonthlyCents:    org.SpentMonthlyCents,
		BudgetResetAt:        org.BudgetResetAt,
		RequireBoardApproval: org.RequireBoardApproval,
		HeadAgentID:          org.HeadAgentID,
		MaxDelegationDepth:   maxDepth,
		CanvasLayout:         org.CanvasLayout,
		CreatedAt:            now.Format(time.RFC3339),
		UpdatedAt:            now.Format(time.RFC3339),
		CreatedBy:            org.CreatedBy,
		UpdatedBy:            org.UpdatedBy,
	}, nil
}

func (p *Postgres) UpdateOrganization(ctx context.Context, id string, org service.Organization) (*service.Organization, error) {
	now := time.Now().UTC()

	rec := goqu.Record{
		"name":                                  org.Name,
		"description":                           org.Description,
		"require_board_approval_for_new_agents": org.RequireBoardApproval,
		"head_agent_id":                         org.HeadAgentID,
		"budget_monthly_cents":                  org.BudgetMonthlyCents,
		"spent_monthly_cents":                   org.SpentMonthlyCents,
		"updated_at":                            now,
		"updated_by":                            org.UpdatedBy,
	}
	if len(org.CanvasLayout) > 0 {
		rec["canvas_layout"] = string(org.CanvasLayout)
	}
	if org.IssuePrefix != "" {
		rec["issue_prefix"] = org.IssuePrefix
	}
	if org.MaxDelegationDepth > 0 {
		rec["max_delegation_depth"] = org.MaxDelegationDepth
	}
	if org.BudgetResetAt != "" {
		t, err := time.Parse(time.RFC3339, org.BudgetResetAt)
		if err == nil {
			rec["budget_reset_at"] = t
		}
	}

	query, _, err := p.goqu.Update(p.tableOrganizations).Set(rec).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update organization query: %w", err)
	}

	res, err := p.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update organization %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return p.GetOrganization(ctx, id)
}

func (p *Postgres) DeleteOrganization(ctx context.Context, id string) error {
	query, _, err := p.goqu.Delete(p.tableOrganizations).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete organization query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete organization %q: %w", id, err)
	}

	return nil
}

func (p *Postgres) IncrementIssueCounter(ctx context.Context, orgID string) (int64, error) {
	// Atomically increment and return the new counter value.
	q := fmt.Sprintf(
		`UPDATE %s SET issue_counter = issue_counter + 1, updated_at = $1 WHERE id = $2 RETURNING issue_counter`,
		p.tableOrganizations.GetTable(),
	)
	now := time.Now().UTC()
	var counter int64
	err := p.db.QueryRowContext(ctx, q, now, orgID).Scan(&counter)
	if err != nil {
		return 0, fmt.Errorf("increment issue counter for org %q: %w", orgID, err)
	}
	return counter, nil
}

func orgRowToRecord(row orgRow) *service.Organization {
	var canvasLayout json.RawMessage
	if row.CanvasLayout != "" && row.CanvasLayout != "{}" {
		canvasLayout = json.RawMessage(row.CanvasLayout)
	}

	var budgetResetAt string
	if row.BudgetResetAt.Valid {
		budgetResetAt = row.BudgetResetAt.Time.Format(time.RFC3339)
	}

	return &service.Organization{
		ID:                   row.ID,
		Name:                 row.Name,
		Description:          row.Description,
		IssuePrefix:          row.IssuePrefix,
		IssueCounter:         row.IssueCounter,
		BudgetMonthlyCents:   row.BudgetMonthlyCents,
		SpentMonthlyCents:    row.SpentMonthlyCents,
		BudgetResetAt:        budgetResetAt,
		RequireBoardApproval: row.RequireBoardApproval,
		HeadAgentID:          row.HeadAgentID,
		MaxDelegationDepth:   row.MaxDelegationDepth,
		CanvasLayout:         canvasLayout,
		CreatedAt:            row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:            row.UpdatedAt.Format(time.RFC3339),
		CreatedBy:            row.CreatedBy,
		UpdatedBy:            row.UpdatedBy,
	}
}
