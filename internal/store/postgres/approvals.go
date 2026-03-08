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

// ─── Approvals ───

type approvalRow struct {
	ID              string          `db:"id"`
	OrganizationID  sql.NullString  `db:"organization_id"`
	Type            string          `db:"type"`
	Status          string          `db:"status"`
	RequestedByType string          `db:"requested_by_type"`
	RequestedByID   string          `db:"requested_by_id"`
	RequestDetails  json.RawMessage `db:"request_details"`
	DecisionNote    sql.NullString  `db:"decision_note"`
	DecidedByUserID sql.NullString  `db:"decided_by_user_id"`
	DecidedAt       sql.NullTime    `db:"decided_at"`
	CreatedAt       time.Time       `db:"created_at"`
	UpdatedAt       time.Time       `db:"updated_at"`
}

var approvalColumns = []interface{}{
	"id", "organization_id", "type", "status",
	"requested_by_type", "requested_by_id", "request_details",
	"decision_note", "decided_by_user_id", "decided_at",
	"created_at", "updated_at",
}

func scanApprovalRow(scanner interface {
	Scan(dest ...interface{}) error
}, row *approvalRow) error {
	return scanner.Scan(
		&row.ID, &row.OrganizationID, &row.Type, &row.Status,
		&row.RequestedByType, &row.RequestedByID, &row.RequestDetails,
		&row.DecisionNote, &row.DecidedByUserID, &row.DecidedAt,
		&row.CreatedAt, &row.UpdatedAt,
	)
}

func (p *Postgres) ListApprovals(ctx context.Context, q *query.Query) (*service.ListResult[service.Approval], error) {
	sql, total, err := p.buildListQuery(ctx, p.tableApprovals, q, approvalColumns...)
	if err != nil {
		return nil, fmt.Errorf("build list approvals query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list approvals: %w", err)
	}
	defer rows.Close()

	var items []service.Approval
	for rows.Next() {
		var row approvalRow
		if err := scanApprovalRow(rows, &row); err != nil {
			return nil, fmt.Errorf("scan approval row: %w", err)
		}

		rec, err := approvalRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *rec)
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.Approval]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (p *Postgres) GetApproval(ctx context.Context, id string) (*service.Approval, error) {
	query, _, err := p.goqu.From(p.tableApprovals).
		Select(approvalColumns...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get approval query: %w", err)
	}

	var row approvalRow
	err = scanApprovalRow(p.db.QueryRowContext(ctx, query), &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get approval %q: %w", id, err)
	}

	return approvalRowToRecord(row)
}

func (p *Postgres) CreateApproval(ctx context.Context, approval service.Approval) (*service.Approval, error) {
	id := ulid.Make().String()
	now := time.Now().UTC()

	var detailsJSON json.RawMessage
	if approval.RequestDetails != nil {
		var err error
		detailsJSON, err = json.Marshal(approval.RequestDetails)
		if err != nil {
			return nil, fmt.Errorf("marshal request details: %w", err)
		}
	}

	query, _, err := p.goqu.Insert(p.tableApprovals).Rows(
		goqu.Record{
			"id":                 id,
			"organization_id":    nullString(approval.OrganizationID),
			"type":               approval.Type,
			"status":             approval.Status,
			"requested_by_type":  approval.RequestedByType,
			"requested_by_id":    approval.RequestedByID,
			"request_details":    detailsJSON,
			"decision_note":      nullString(approval.DecisionNote),
			"decided_by_user_id": nullString(approval.DecidedByUserID),
			"decided_at":         nil,
			"created_at":         now,
			"updated_at":         now,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert approval query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create approval: %w", err)
	}

	return &service.Approval{
		ID:              id,
		OrganizationID:  approval.OrganizationID,
		Type:            approval.Type,
		Status:          approval.Status,
		RequestedByType: approval.RequestedByType,
		RequestedByID:   approval.RequestedByID,
		RequestDetails:  approval.RequestDetails,
		DecisionNote:    approval.DecisionNote,
		DecidedByUserID: approval.DecidedByUserID,
		CreatedAt:       now.Format(time.RFC3339),
		UpdatedAt:       now.Format(time.RFC3339),
	}, nil
}

func (p *Postgres) UpdateApproval(ctx context.Context, id string, approval service.Approval) (*service.Approval, error) {
	now := time.Now().UTC()

	var detailsJSON json.RawMessage
	if approval.RequestDetails != nil {
		var err error
		detailsJSON, err = json.Marshal(approval.RequestDetails)
		if err != nil {
			return nil, fmt.Errorf("marshal request details: %w", err)
		}
	}

	record := goqu.Record{
		"status":             approval.Status,
		"request_details":    detailsJSON,
		"decision_note":      nullString(approval.DecisionNote),
		"decided_by_user_id": nullString(approval.DecidedByUserID),
		"updated_at":         now,
	}

	// Set decided_at if a decision has been made.
	if approval.DecidedByUserID != "" {
		record["decided_at"] = now
	}

	query, _, err := p.goqu.Update(p.tableApprovals).Set(record).
		Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update approval query: %w", err)
	}

	res, err := p.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update approval %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return p.GetApproval(ctx, id)
}

func (p *Postgres) ListPendingApprovals(ctx context.Context, orgID string) ([]service.Approval, error) {
	query, _, err := p.goqu.From(p.tableApprovals).
		Select(approvalColumns...).
		Where(
			goqu.I("organization_id").Eq(orgID),
			goqu.I("status").Eq("pending"),
		).
		Order(goqu.I("created_at").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list pending approvals query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list pending approvals for org %q: %w", orgID, err)
	}
	defer rows.Close()

	var items []service.Approval
	for rows.Next() {
		var row approvalRow
		if err := scanApprovalRow(rows, &row); err != nil {
			return nil, fmt.Errorf("scan approval row: %w", err)
		}

		rec, err := approvalRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *rec)
	}

	return items, rows.Err()
}

func approvalRowToRecord(row approvalRow) (*service.Approval, error) {
	var details map[string]any
	if len(row.RequestDetails) > 0 {
		if err := json.Unmarshal(row.RequestDetails, &details); err != nil {
			return nil, fmt.Errorf("unmarshal request details for approval %q: %w", row.ID, err)
		}
	}

	var decidedAt string
	if row.DecidedAt.Valid {
		decidedAt = row.DecidedAt.Time.Format(time.RFC3339)
	}

	return &service.Approval{
		ID:              row.ID,
		OrganizationID:  row.OrganizationID.String,
		Type:            row.Type,
		Status:          row.Status,
		RequestedByType: row.RequestedByType,
		RequestedByID:   row.RequestedByID,
		RequestDetails:  details,
		DecisionNote:    row.DecisionNote.String,
		DecidedByUserID: row.DecidedByUserID.String,
		DecidedAt:       decidedAt,
		CreatedAt:       row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       row.UpdatedAt.Format(time.RFC3339),
	}, nil
}
