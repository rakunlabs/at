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

type approvalRow struct {
	ID              string         `db:"id"`
	OrganizationID  sql.NullString `db:"organization_id"`
	Type            string         `db:"type"`
	Status          string         `db:"status"`
	RequestedByType string         `db:"requested_by_type"`
	RequestedByID   string         `db:"requested_by_id"`
	RequestDetails  sql.NullString `db:"request_details"`
	DecisionNote    sql.NullString `db:"decision_note"`
	DecidedByUserID sql.NullString `db:"decided_by_user_id"`
	DecidedAt       sql.NullString `db:"decided_at"`
	CreatedAt       string         `db:"created_at"`
	UpdatedAt       string         `db:"updated_at"`
}

var approvalColumns = []interface{}{
	"id", "organization_id", "type", "status", "requested_by_type", "requested_by_id",
	"request_details", "decision_note", "decided_by_user_id", "decided_at",
	"created_at", "updated_at",
}

func scanApprovalRow(scanner interface{ Scan(dest ...any) error }) (approvalRow, error) {
	var row approvalRow
	err := scanner.Scan(
		&row.ID, &row.OrganizationID, &row.Type, &row.Status, &row.RequestedByType, &row.RequestedByID,
		&row.RequestDetails, &row.DecisionNote, &row.DecidedByUserID, &row.DecidedAt,
		&row.CreatedAt, &row.UpdatedAt,
	)

	return row, err
}

func (s *SQLite) ListApprovals(ctx context.Context, q *query.Query) (*service.ListResult[service.Approval], error) {
	sql, total, err := s.buildListQuery(ctx, s.tableApprovals, q, approvalColumns...)
	if err != nil {
		return nil, fmt.Errorf("build list approvals query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list approvals: %w", err)
	}
	defer rows.Close()

	var items []service.Approval
	for rows.Next() {
		row, err := scanApprovalRow(rows)
		if err != nil {
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

func (s *SQLite) GetApproval(ctx context.Context, id string) (*service.Approval, error) {
	query, _, err := s.goqu.From(s.tableApprovals).
		Select(approvalColumns...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get approval query: %w", err)
	}

	row, err := scanApprovalRow(s.db.QueryRowContext(ctx, query))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get approval %q: %w", id, err)
	}

	return approvalRowToRecord(row)
}

func (s *SQLite) CreateApproval(ctx context.Context, approval service.Approval) (*service.Approval, error) {
	id := ulid.Make().String()
	now := time.Now().UTC()

	detailsStr := marshalJSONField(approval.RequestDetails)

	query, _, err := s.goqu.Insert(s.tableApprovals).Rows(
		goqu.Record{
			"id":                 id,
			"organization_id":    approval.OrganizationID,
			"type":               approval.Type,
			"status":             approval.Status,
			"requested_by_type":  approval.RequestedByType,
			"requested_by_id":    approval.RequestedByID,
			"request_details":    detailsStr,
			"decision_note":      approval.DecisionNote,
			"decided_by_user_id": approval.DecidedByUserID,
			"decided_at":         approval.DecidedAt,
			"created_at":         now.Format(time.RFC3339),
			"updated_at":         now.Format(time.RFC3339),
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert approval query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
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
		DecidedAt:       approval.DecidedAt,
		CreatedAt:       now.Format(time.RFC3339),
		UpdatedAt:       now.Format(time.RFC3339),
	}, nil
}

func (s *SQLite) UpdateApproval(ctx context.Context, id string, approval service.Approval) (*service.Approval, error) {
	now := time.Now().UTC()

	detailsStr := marshalJSONField(approval.RequestDetails)

	query, _, err := s.goqu.Update(s.tableApprovals).Set(
		goqu.Record{
			"status":             approval.Status,
			"request_details":    detailsStr,
			"decision_note":      approval.DecisionNote,
			"decided_by_user_id": approval.DecidedByUserID,
			"decided_at":         approval.DecidedAt,
			"updated_at":         now.Format(time.RFC3339),
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update approval query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
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

	return s.GetApproval(ctx, id)
}

func (s *SQLite) ListPendingApprovals(ctx context.Context, orgID string) ([]service.Approval, error) {
	query, _, err := s.goqu.From(s.tableApprovals).
		Select(approvalColumns...).
		Where(
			goqu.I("organization_id").Eq(orgID),
			goqu.I("status").Eq(service.ApprovalStatusPending),
		).
		Order(goqu.I("created_at").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list pending approvals query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list pending approvals for org %q: %w", orgID, err)
	}
	defer rows.Close()

	var items []service.Approval
	for rows.Next() {
		row, err := scanApprovalRow(rows)
		if err != nil {
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
	if row.RequestDetails.Valid && row.RequestDetails.String != "" {
		if err := json.Unmarshal([]byte(row.RequestDetails.String), &details); err != nil {
			return nil, fmt.Errorf("unmarshal request_details for approval %q: %w", row.ID, err)
		}
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
		DecidedAt:       row.DecidedAt.String,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}, nil
}
