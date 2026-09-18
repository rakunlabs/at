package postgres

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
	"github.com/worldline-go/types"
)

// ─── Routing profiles ───

type routingProfileRow struct {
	WorkspaceID string              `db:"workspace_id"`
	ID          string              `db:"id"`
	Name        string              `db:"name"`
	Description string              `db:"description"`
	Targets     types.Slice[string] `db:"targets"`
	CreatedAt   time.Time           `db:"created_at"`
	UpdatedAt   time.Time           `db:"updated_at"`
	CreatedBy   string              `db:"created_by"`
	UpdatedBy   string              `db:"updated_by"`
}

var routingProfileColumns = []interface{}{
	"id", "name", "description", "targets",
	"created_at", "updated_at", "created_by", "updated_by", "workspace_id",
}

func scanRoutingProfileRow(scanner interface {
	Scan(dest ...interface{}) error
}, row *routingProfileRow) error {
	return scanner.Scan(
		&row.ID, &row.Name, &row.Description, &row.Targets,
		&row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy,
		&row.WorkspaceID,
	)
}

func routingProfileRowToRecord(row routingProfileRow) *service.RoutingProfile {
	targets := row.Targets
	if targets == nil {
		// Callers iterate Targets without a nil check; an absent JSON array must
		// read as empty, not nil.
		targets = types.Slice[string]{}
	}

	return &service.RoutingProfile{
		WorkspaceID: row.WorkspaceID,
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description,
		Targets:     targets,
		CreatedAt:   row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   row.UpdatedAt.Format(time.RFC3339),
		CreatedBy:   row.CreatedBy,
		UpdatedBy:   row.UpdatedBy,
	}
}

func (p *Postgres) ListRoutingProfiles(ctx context.Context, q *query.Query) (*service.ListResult[service.RoutingProfile], error) {
	sqlStr, total, err := p.buildListQuery(ctx, p.tableRoutingProfiles, q, routingProfileColumns...)
	if err != nil {
		return nil, fmt.Errorf("build list routing profiles query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sqlStr)
	if err != nil {
		return nil, fmt.Errorf("list routing profiles: %w", err)
	}
	defer rows.Close()

	items := []service.RoutingProfile{}
	for rows.Next() {
		var row routingProfileRow
		if err := scanRoutingProfileRow(rows, &row); err != nil {
			return nil, fmt.Errorf("scan routing profile row: %w", err)
		}

		items = append(items, *routingProfileRowToRecord(row))
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.RoutingProfile]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (p *Postgres) GetRoutingProfile(ctx context.Context, id string) (*service.RoutingProfile, error) {
	return p.getRoutingProfileWhere(ctx, goqu.I("id").Eq(id), "id", id)
}

// GetRoutingProfileByName resolves the name a gateway request carried as its
// model. It is on the request path, so it stays a single indexed lookup.
func (p *Postgres) GetRoutingProfileByName(ctx context.Context, name string) (*service.RoutingProfile, error) {
	return p.getRoutingProfileWhere(ctx, goqu.I("name").Eq(name), "name", name)
}

func (p *Postgres) getRoutingProfileWhere(ctx context.Context, cond goqu.Expression, label, value string) (*service.RoutingProfile, error) {
	scope, err := p.businessReadScope(ctx, p.tableRoutingProfiles)
	if err != nil {
		return nil, err
	}

	sqlStr, _, err := p.goqu.From(p.tableRoutingProfiles).
		Select(routingProfileColumns...).
		Where(scope, cond).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get routing profile query: %w", err)
	}

	var row routingProfileRow
	err = scanRoutingProfileRow(p.db.QueryRowContext(ctx, sqlStr), &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get routing profile by %s %q: %w", label, value, err)
	}

	return routingProfileRowToRecord(row), nil
}

// GetGatewayRoutingProfile resolves a profile for the gateway request path.
// The workspace comes from the presented token's persisted binding rather than
// from a session principal, so the scope predicate is written explicitly here.
// An empty workspace matches nothing: there is no public profile.
func (p *Postgres) GetGatewayRoutingProfile(ctx context.Context, name, workspaceID string) (*service.RoutingProfile, error) {
	if name == "" || workspaceID == "" {
		return nil, nil
	}

	sqlStr, _, err := p.goqu.From(p.tableRoutingProfiles).
		Select(routingProfileColumns...).
		Where(goqu.I("workspace_id").Eq(workspaceID), goqu.I("name").Eq(name)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build gateway routing profile query: %w", err)
	}

	var row routingProfileRow
	err = scanRoutingProfileRow(p.db.QueryRowContext(ctx, sqlStr), &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get gateway routing profile %q: %w", name, err)
	}

	return routingProfileRowToRecord(row), nil
}

// ListGatewayRoutingProfiles backs the profile entries advertised by
// /gateway/v1/models. Same scoping rationale as GetGatewayRoutingProfile.
func (p *Postgres) ListGatewayRoutingProfiles(ctx context.Context, workspaceID string) ([]service.RoutingProfile, error) {
	if workspaceID == "" {
		return nil, nil
	}

	sqlStr, _, err := p.goqu.From(p.tableRoutingProfiles).
		Select(routingProfileColumns...).
		Where(goqu.I("workspace_id").Eq(workspaceID)).
		Order(goqu.I("name").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build gateway routing profile list query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sqlStr)
	if err != nil {
		return nil, fmt.Errorf("list gateway routing profiles: %w", err)
	}
	defer rows.Close()

	items := []service.RoutingProfile{}
	for rows.Next() {
		var row routingProfileRow
		if err := scanRoutingProfileRow(rows, &row); err != nil {
			return nil, fmt.Errorf("scan routing profile row: %w", err)
		}

		items = append(items, *routingProfileRowToRecord(row))
	}

	return items, rows.Err()
}

func (p *Postgres) CreateRoutingProfile(ctx context.Context, profile service.RoutingProfile) (*service.RoutingProfile, error) {
	w, err := p.beginBusinessWrite(ctx, p.tableRoutingProfiles, "providers.write", "")
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()

	if profile.WorkspaceID != "" && profile.WorkspaceID != w.actor.WorkspaceID {
		return nil, service.ErrAccessDenied
	}

	profile = service.NormalizeRoutingProfile(profile)
	if err := service.ValidateRoutingProfile(profile); err != nil {
		return nil, err
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	sqlStr, _, err := w.tx.Insert(p.tableRoutingProfiles).Rows(
		goqu.Record{
			"workspace_id": w.actor.WorkspaceID,
			"id":           id,
			"name":         profile.Name,
			"description":  profile.Description,
			"targets":      profile.Targets,
			"created_at":   now,
			"updated_at":   now,
			"created_by":   profile.CreatedBy,
			"updated_by":   profile.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert routing profile query: %w", err)
	}

	if _, err := w.tx.ExecContext(ctx, sqlStr); err != nil {
		return nil, fmt.Errorf("create routing profile %q: %w", profile.Name, err)
	}
	if err := w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit routing profile: %w", err)
	}

	profile.WorkspaceID = w.actor.WorkspaceID
	profile.ID = id
	profile.CreatedAt = now.Format(time.RFC3339)
	profile.UpdatedAt = now.Format(time.RFC3339)

	return &profile, nil
}

func (p *Postgres) UpdateRoutingProfile(ctx context.Context, id string, profile service.RoutingProfile) (*service.RoutingProfile, error) {
	w, err := p.beginBusinessWrite(ctx, p.tableRoutingProfiles, "providers.write", id)
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()

	if profile.WorkspaceID != "" && profile.WorkspaceID != w.actor.WorkspaceID {
		return nil, service.ErrAccessDenied
	}

	profile = service.NormalizeRoutingProfile(profile)
	if err := service.ValidateRoutingProfile(profile); err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	sqlStr, _, err := p.goqu.Update(p.tableRoutingProfiles).Set(
		goqu.Record{
			"name":        profile.Name,
			"description": profile.Description,
			"targets":     profile.Targets,
			"updated_at":  now,
			"updated_by":  profile.UpdatedBy,
		},
	).Where(w.predicate, goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update routing profile query: %w", err)
	}

	res, err := w.tx.ExecContext(ctx, sqlStr)
	if err != nil {
		return nil, fmt.Errorf("update routing profile %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}
	if err := w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit routing profile update: %w", err)
	}

	return p.GetRoutingProfile(ctx, id)
}

func (p *Postgres) DeleteRoutingProfile(ctx context.Context, id string) error {
	w, err := p.beginBusinessWrite(ctx, p.tableRoutingProfiles, "providers.write", id)
	if err != nil {
		return err
	}
	defer w.tx.Rollback()

	sqlStr, _, err := p.goqu.Delete(p.tableRoutingProfiles).
		Where(w.predicate, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete routing profile query: %w", err)
	}
	if _, err := w.tx.ExecContext(ctx, sqlStr); err != nil {
		return fmt.Errorf("delete routing profile %q: %w", id, err)
	}

	return w.tx.Commit()
}
