package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
	"github.com/worldline-go/types"
)

// ─── Audit Log ───

type auditRow struct {
	ID             string         `db:"id"`
	OrganizationID sql.NullString `db:"organization_id"`
	ActorType      string         `db:"actor_type"`
	ActorID        string         `db:"actor_id"`
	Action         string         `db:"action"`
	ResourceType   string         `db:"resource_type"`
	ResourceID     string         `db:"resource_id"`
	Details        types.RawJSON  `db:"details"`
	CreatedAt      time.Time      `db:"created_at"`
}

var auditColumns = []interface{}{
	"id", "organization_id", "actor_type", "actor_id", "action",
	"resource_type", "resource_id", "details", "created_at",
}

func scanAuditRow(scanner interface {
	Scan(dest ...interface{}) error
}, row *auditRow) error {
	return scanner.Scan(
		&row.ID, &row.OrganizationID, &row.ActorType, &row.ActorID,
		&row.Action, &row.ResourceType, &row.ResourceID, &row.Details, &row.CreatedAt,
	)
}

func (p *Postgres) RecordAudit(ctx context.Context, entry service.AuditEntry) error {
	id := ulid.Make().String()
	now := time.Now().UTC()

	var detailsVal interface{}
	if entry.Details != nil {
		detailsJSON, err := json.Marshal(entry.Details)
		if err != nil {
			return fmt.Errorf("marshal audit details: %w", err)
		}
		detailsVal = types.RawJSON(detailsJSON)
	}

	query, _, err := p.goqu.Insert(p.tableAuditLog).Rows(
		goqu.Record{
			"id":              id,
			"organization_id": nullString(entry.OrganizationID),
			"actor_type":      entry.ActorType,
			"actor_id":        entry.ActorID,
			"action":          entry.Action,
			"resource_type":   entry.ResourceType,
			"resource_id":     entry.ResourceID,
			"details":         detailsVal,
			"created_at":      now,
		},
	).ToSQL()
	if err != nil {
		return fmt.Errorf("build insert audit query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("record audit entry: %w", err)
	}

	return nil
}

func (p *Postgres) ListAuditEntries(ctx context.Context, q *query.Query) (*service.ListResult[service.AuditEntry], error) {
	sql, total, err := p.buildListQuery(ctx, p.tableAuditLog, q, auditColumns...)
	if err != nil {
		return nil, fmt.Errorf("build list audit entries query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list audit entries: %w", err)
	}
	defer rows.Close()

	var items []service.AuditEntry
	for rows.Next() {
		var row auditRow
		if err := scanAuditRow(rows, &row); err != nil {
			return nil, fmt.Errorf("scan audit row: %w", err)
		}

		entry, err := auditRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *entry)
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.AuditEntry]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (p *Postgres) GetAuditTrail(ctx context.Context, resourceType, resourceID string) ([]service.AuditEntry, error) {
	query, _, err := p.goqu.From(p.tableAuditLog).
		Select(auditColumns...).
		Where(
			goqu.I("resource_type").Eq(resourceType),
			goqu.I("resource_id").Eq(resourceID),
		).
		Order(goqu.I("created_at").Desc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get audit trail query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("get audit trail for %s/%s: %w", resourceType, resourceID, err)
	}
	defer rows.Close()

	var items []service.AuditEntry
	for rows.Next() {
		var row auditRow
		if err := scanAuditRow(rows, &row); err != nil {
			return nil, fmt.Errorf("scan audit row: %w", err)
		}

		entry, err := auditRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *entry)
	}

	return items, rows.Err()
}

func auditRowToRecord(row auditRow) (*service.AuditEntry, error) {
	var details map[string]any
	if len(row.Details) > 0 {
		if err := json.Unmarshal(row.Details, &details); err != nil {
			return nil, fmt.Errorf("unmarshal audit details for %q: %w", row.ID, err)
		}
	}

	return &service.AuditEntry{
		ID:             row.ID,
		OrganizationID: row.OrganizationID.String,
		ActorType:      row.ActorType,
		ActorID:        row.ActorID,
		Action:         row.Action,
		ResourceType:   row.ResourceType,
		ResourceID:     row.ResourceID,
		Details:        details,
		CreatedAt:      row.CreatedAt.Format(time.RFC3339),
	}, nil
}
