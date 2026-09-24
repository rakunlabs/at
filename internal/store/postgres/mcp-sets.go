package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/doug-martin/goqu/v9/exp"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
	"github.com/rakunlabs/query/adapter/adaptergoqu"
	"github.com/worldline-go/types"
)

type mcpSetRow struct {
	WorkspaceID string         `db:"workspace_id"`
	OwnerUserID string         `db:"owner_user_id"`
	ID          string         `db:"id"`
	Name        string         `db:"name"`
	Description string         `db:"description"`
	Category    string         `db:"category"`
	Tags        types.RawJSON  `db:"tags"`
	Config      types.RawJSON  `db:"config"`
	Servers     types.RawJSON  `db:"servers"`
	URLs        types.RawJSON  `db:"urls"`
	CreatedAt   time.Time      `db:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at"`
	CreatedBy   sql.NullString `db:"created_by"`
	UpdatedBy   sql.NullString `db:"updated_by"`
}

var mcpSetColumns = []any{"id", "name", "description", "category", "tags", "config", "servers", "urls", "created_at", "updated_at", "created_by", "updated_by", "workspace_id", "owner_user_id"}

func (p *Postgres) mcpSetVisibilityScope(ctx context.Context) (exp.Expression, service.AccessPrincipal, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, service.AccessPrincipal{}, err
	}
	base, err := businessPredicate(a, "mcp.read", "id")
	if err != nil {
		return nil, service.AccessPrincipal{}, err
	}
	_, hasPrincipal := service.AccessPrincipalFromContext(ctx)
	if !hasPrincipal && service.LegacyWorkspaceAccessFromContext(ctx) {
		base = goqu.And(base, goqu.C("owner_user_id").Eq(""))
	} else if !a.PlatformAdmin {
		base = goqu.And(base, goqu.Or(goqu.C("owner_user_id").Eq(""), goqu.C("owner_user_id").Eq(a.UserID)))
	}
	return base, a, nil
}

func (p *Postgres) ListMCPSets(ctx context.Context, q *query.Query) (*service.ListResult[service.MCPSet], error) {
	scope, a, err := p.mcpSetVisibilityScope(ctx)
	if err != nil {
		return nil, err
	}
	ds := p.goqu.From(p.tableMCPSets).Where(scope)
	countDS := ds
	if q != nil {
		if exprs := adaptergoqu.Expression(q); len(exprs) > 0 {
			countDS = countDS.Where(exprs...)
		}
	}
	var total uint64
	if _, err := countDS.Select(goqu.COUNT("*")).ScanValContext(ctx, &total); err != nil {
		return nil, fmt.Errorf("count mcp sets: %w", err)
	}
	ds = adaptergoqu.Select(q, ds, adaptergoqu.WithParameterized(false))
	rows, err := ds.Select(mcpSetColumns...).Executor().QueryContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("list mcp sets: %w", err)
	}
	defer rows.Close()

	var items []service.MCPSet
	for rows.Next() {
		var row mcpSetRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Description, &row.Category, &row.Tags, &row.Config, &row.Servers, &row.URLs, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy, &row.WorkspaceID, &row.OwnerUserID); err != nil {
			return nil, fmt.Errorf("scan mcp set row: %w", err)
		}

		rec, err := mcpSetRowToRecord(row)
		if err != nil {
			return nil, err
		}
		mcpReadOwnedDTO(a, rec.ID, rec.WorkspaceID, rec.OwnerUserID, &rec.Config, &rec.URLs)
		items = append(items, *rec)
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.MCPSet]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (p *Postgres) GetMCPSet(ctx context.Context, id string) (*service.MCPSet, error) {
	scope, a, err := p.mcpSetVisibilityScope(ctx)
	if err != nil {
		return nil, err
	}
	query, _, err := p.goqu.From(p.tableMCPSets).
		Select(mcpSetColumns...).
		Where(scope, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get mcp set query: %w", err)
	}

	var row mcpSetRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Description, &row.Category, &row.Tags, &row.Config, &row.Servers, &row.URLs, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy, &row.WorkspaceID, &row.OwnerUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mcp set %q: %w", id, err)
	}

	rec, err := mcpSetRowToRecord(row)
	if err != nil {
		return nil, err
	}
	mcpReadOwnedDTO(a, rec.ID, rec.WorkspaceID, rec.OwnerUserID, &rec.Config, &rec.URLs)
	return rec, nil
}

func (p *Postgres) GetMCPSetByName(ctx context.Context, name string) (*service.MCPSet, error) {
	scope, a, err := p.mcpSetVisibilityScope(ctx)
	if err != nil {
		return nil, err
	}
	query, _, err := p.goqu.From(p.tableMCPSets).
		Select(mcpSetColumns...).
		Where(scope, goqu.I("name").Eq(name)).
		Order(goqu.L("CASE WHEN owner_user_id = ? THEN 0 WHEN owner_user_id = '' THEN 1 ELSE 2 END", a.UserID).Asc()).
		Limit(1).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get mcp set by name query: %w", err)
	}

	var row mcpSetRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Description, &row.Category, &row.Tags, &row.Config, &row.Servers, &row.URLs, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy, &row.WorkspaceID, &row.OwnerUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mcp set by name %q: %w", name, err)
	}

	rec, err := mcpSetRowToRecord(row)
	if err != nil {
		return nil, err
	}
	mcpReadOwnedDTO(a, rec.ID, rec.WorkspaceID, rec.OwnerUserID, &rec.Config, &rec.URLs)
	return rec, nil
}

func (p *Postgres) CreateMCPSet(ctx context.Context, s service.MCPSet) (*service.MCPSet, error) {
	w, err := p.beginBusinessWrite(ctx, p.tableMCPSets, "mcp.read", "")
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()
	if s.WorkspaceID != "" && s.WorkspaceID != w.actor.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	if s.OwnerUserID != "" {
		if s.OwnerUserID != w.actor.UserID {
			return nil, service.ErrAccessDenied
		}
	} else if !w.actor.Allows("mcp.write", service.AccessResource{WorkspaceID: w.actor.WorkspaceID}) {
		return nil, service.ErrAccessDenied
	}
	if err = p.mcpSetReferences(ctx, w, s.Config, s.Servers, s.OwnerUserID != ""); err != nil {
		return nil, err
	}
	configJSON, err := json.Marshal(s.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp set config: %w", err)
	}
	serversJSON, err := json.Marshal(s.Servers)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp set servers: %w", err)
	}
	urlsJSON, err := json.Marshal(s.URLs)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp set urls: %w", err)
	}
	tagsJSON, err := json.Marshal(s.Tags)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp set tags: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := p.goqu.Insert(p.tableMCPSets).Rows(
		goqu.Record{
			"workspace_id":  w.actor.WorkspaceID,
			"owner_user_id": s.OwnerUserID,
			"id":            id,
			"name":          s.Name,
			"description":   s.Description,
			"category":      s.Category,
			"tags":          types.RawJSON(tagsJSON),
			"config":        types.RawJSON(configJSON),
			"servers":       types.RawJSON(serversJSON),
			"urls":          types.RawJSON(urlsJSON),
			"created_at":    now,
			"updated_at":    now,
			"created_by":    s.CreatedBy,
			"updated_by":    s.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert mcp set query: %w", err)
	}

	if _, err := w.tx.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create mcp set %q: %w", s.Name, err)
	}
	if err = w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit MCP set: %w", err)
	}

	return &service.MCPSet{
		WorkspaceID: w.actor.WorkspaceID,
		OwnerUserID: s.OwnerUserID,
		Scope:       mcpSetScope(s.OwnerUserID),
		ID:          id,
		Name:        s.Name,
		Description: s.Description,
		Category:    s.Category,
		Tags:        s.Tags,
		Config:      s.Config,
		Servers:     s.Servers,
		URLs:        s.URLs,
		CreatedAt:   now.Format(time.RFC3339),
		UpdatedAt:   now.Format(time.RFC3339),
		CreatedBy:   s.CreatedBy,
		UpdatedBy:   s.UpdatedBy,
	}, nil
}

func (p *Postgres) UpdateMCPSet(ctx context.Context, id string, s service.MCPSet) (*service.MCPSet, error) {
	w, err := p.beginBusinessWrite(ctx, p.tableMCPSets, "mcp.read", id)
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()
	w.predicate = ownedResourceWritePredicate(w.actor, "mcp.write")
	if s.WorkspaceID != "" && s.WorkspaceID != w.actor.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	var owner string
	found, err := w.tx.From(p.tableMCPSets).Select("owner_user_id").Where(w.predicate, goqu.C("id").Eq(id)).ForUpdate(goqu.Wait).ScanValContext(ctx, &owner)
	if err != nil {
		return nil, fmt.Errorf("lock mcp set for update: %w", err)
	}
	if !found {
		return nil, nil
	}
	if err = p.mcpSetReferences(ctx, w, s.Config, s.Servers, owner != ""); err != nil {
		return nil, err
	}
	configJSON, err := json.Marshal(s.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp set config: %w", err)
	}
	serversJSON, err := json.Marshal(s.Servers)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp set servers: %w", err)
	}
	urlsJSON, err := json.Marshal(s.URLs)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp set urls: %w", err)
	}
	tagsJSON, err := json.Marshal(s.Tags)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp set tags: %w", err)
	}

	now := time.Now().UTC()

	query, _, err := p.goqu.Update(p.tableMCPSets).Set(
		goqu.Record{
			"name":        s.Name,
			"description": s.Description,
			"category":    s.Category,
			"tags":        types.RawJSON(tagsJSON),
			"config":      types.RawJSON(configJSON),
			"servers":     types.RawJSON(serversJSON),
			"urls":        types.RawJSON(urlsJSON),
			"updated_at":  now,
			"updated_by":  s.UpdatedBy,
		},
	).Where(w.predicate, goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update mcp set query: %w", err)
	}

	res, err := w.tx.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update mcp set %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}
	if err = w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit MCP set update: %w", err)
	}

	return p.GetMCPSet(ctx, id)
}

func (p *Postgres) DeleteMCPSet(ctx context.Context, id string) error {
	w, err := p.beginBusinessWrite(ctx, p.tableMCPSets, "mcp.read", id)
	if err != nil {
		return err
	}
	defer w.tx.Rollback()
	w.predicate = ownedResourceWritePredicate(w.actor, "mcp.write")
	query, _, err := p.goqu.Delete(p.tableMCPSets).
		Where(w.predicate, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete mcp set query: %w", err)
	}

	_, err = w.tx.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete mcp set %q: %w", id, err)
	}

	return w.tx.Commit()
}

func mcpSetRowToRecord(row mcpSetRow) (*service.MCPSet, error) {
	var cfg service.MCPServerConfig
	if len(row.Config) > 0 && string(row.Config) != "{}" {
		if err := json.Unmarshal(row.Config, &cfg); err != nil {
			return nil, fmt.Errorf("unmarshal mcp set config for %q: %w", row.ID, err)
		}
	}

	var servers []string
	if len(row.Servers) > 0 {
		if err := json.Unmarshal(row.Servers, &servers); err != nil {
			return nil, fmt.Errorf("unmarshal mcp set servers for %q: %w", row.ID, err)
		}
	}

	var urls []string
	if len(row.URLs) > 0 {
		if err := json.Unmarshal(row.URLs, &urls); err != nil {
			return nil, fmt.Errorf("unmarshal mcp set urls for %q: %w", row.ID, err)
		}
	}

	var tags []string
	if len(row.Tags) > 0 {
		if err := json.Unmarshal(row.Tags, &tags); err != nil {
			return nil, fmt.Errorf("unmarshal mcp set tags for %q: %w", row.ID, err)
		}
	}

	return &service.MCPSet{
		WorkspaceID: row.WorkspaceID,
		OwnerUserID: row.OwnerUserID,
		Scope:       mcpSetScope(row.OwnerUserID),
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description,
		Category:    row.Category,
		Tags:        tags,
		Config:      cfg,
		Servers:     servers,
		URLs:        urls,
		CreatedAt:   row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   row.UpdatedAt.Format(time.RFC3339),
		CreatedBy:   row.CreatedBy.String,
		UpdatedBy:   row.UpdatedBy.String,
	}, nil
}

func mcpSetScope(owner string) string {
	if owner != "" {
		return "personal"
	}
	return "workspace"
}

func (p *Postgres) PublishMCPSetToWorkspace(ctx context.Context, id, by string) (*service.MCPSet, error) {
	w, err := p.beginBusinessWrite(ctx, p.tableMCPSets, "mcp.write", "")
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()
	var row mcpSetRow
	found, err := w.tx.From(p.tableMCPSets).Select(mcpSetColumns...).Where(
		goqu.C("workspace_id").Eq(w.actor.WorkspaceID),
		goqu.C("id").Eq(id),
		goqu.C("owner_user_id").Eq(w.actor.UserID),
	).ForShare(goqu.Wait).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("load personal mcp set for publish: %w", err)
	}
	if !found {
		return nil, service.ErrAccessResourceNotFound
	}
	record, err := mcpSetRowToRecord(row)
	if err != nil {
		return nil, err
	}
	if err = p.mcpSetReferences(ctx, w, record.Config, record.Servers, false); err != nil {
		return nil, err
	}
	newID := ulid.Make().String()
	now := time.Now().UTC()
	_, err = w.tx.Insert(p.tableMCPSets).Rows(goqu.Record{
		"workspace_id": w.actor.WorkspaceID, "owner_user_id": "", "id": newID,
		"name": row.Name, "description": row.Description, "category": row.Category,
		"tags": row.Tags, "config": row.Config, "servers": row.Servers, "urls": row.URLs,
		"created_at": now, "updated_at": now, "created_by": by, "updated_by": by,
	}).Executor().ExecContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("publish mcp set %q: %w", row.Name, err)
	}
	if err := w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit mcp set publish: %w", err)
	}
	return p.GetMCPSet(ctx, newID)
}
