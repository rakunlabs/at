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

// ─── Skill CRUD ───

type skillRow struct {
	WorkspaceID        string        `db:"workspace_id"`
	OwnerUserID        string        `db:"owner_user_id"`
	ID                 string        `db:"id"`
	Name               string        `db:"name"`
	Description        string        `db:"description"`
	Category           string        `db:"category"`
	Tags               types.RawJSON `db:"tags"`
	SystemPrompt       string        `db:"system_prompt"`
	Tools              types.RawJSON `db:"tools"`
	Resources          types.RawJSON `db:"resources"`
	Context            string        `db:"execution_context"`
	Agent              string        `db:"execution_agent"`
	Background         bool          `db:"execution_background"`
	Version            string        `db:"version"`
	Author             string        `db:"author"`
	License            string        `db:"license"`
	SourceURL          string        `db:"source_url"`
	SourceType         string        `db:"source_type"`
	SourceRef          string        `db:"source_ref"`
	SourcePath         string        `db:"source_path"`
	SourceCredentialID string        `db:"source_credential_id"`
	SourceChecksum     string        `db:"source_checksum"`
	CreatedAt          time.Time     `db:"created_at"`
	UpdatedAt          time.Time     `db:"updated_at"`
	CreatedBy          string        `db:"created_by"`
	UpdatedBy          string        `db:"updated_by"`
}

var skillColumns = []any{"id", "name", "description", "category", "tags", "system_prompt", "tools", "resources", "execution_context", "execution_agent", "execution_background", "version", "author", "license", "source_url", "source_type", "source_ref", "source_path", "source_credential_id", "source_checksum", "created_at", "updated_at", "created_by", "updated_by", "workspace_id", "owner_user_id"}

func (p *Postgres) skillVisibilityScope(ctx context.Context) (exp.Expression, service.AccessPrincipal, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, service.AccessPrincipal{}, err
	}
	base, err := businessPredicate(a, "skills.read", "id")
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

func (p *Postgres) ListSkills(ctx context.Context, q *query.Query) (*service.ListResult[service.Skill], error) {
	scope, _, err := p.skillVisibilityScope(ctx)
	if err != nil {
		return nil, err
	}
	ds := p.goqu.From(p.tableSkills).Where(scope)
	countDS := ds
	if q != nil {
		if exprs := adaptergoqu.Expression(q); len(exprs) > 0 {
			countDS = countDS.Where(exprs...)
		}
	}
	var total uint64
	if _, err := countDS.Select(goqu.COUNT("*")).ScanValContext(ctx, &total); err != nil {
		return nil, fmt.Errorf("count skills: %w", err)
	}
	ds = adaptergoqu.Select(q, ds, adaptergoqu.WithParameterized(false))
	rows, err := ds.Select(skillColumns...).Executor().QueryContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	defer rows.Close()

	var items []service.Skill
	for rows.Next() {
		var row skillRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Description, &row.Category, &row.Tags, &row.SystemPrompt, &row.Tools, &row.Resources, &row.Context, &row.Agent, &row.Background, &row.Version, &row.Author, &row.License, &row.SourceURL, &row.SourceType, &row.SourceRef, &row.SourcePath, &row.SourceCredentialID, &row.SourceChecksum, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy, &row.WorkspaceID, &row.OwnerUserID); err != nil {
			return nil, fmt.Errorf("scan skill row: %w", err)
		}

		sk, err := skillRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *sk)
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.Skill]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (p *Postgres) GetSkill(ctx context.Context, id string) (*service.Skill, error) {
	scope, _, err := p.skillVisibilityScope(ctx)
	if err != nil {
		return nil, err
	}
	query, _, err := p.goqu.From(p.tableSkills).
		Select(skillColumns...).
		Where(scope, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get skill query: %w", err)
	}

	var row skillRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Description, &row.Category, &row.Tags, &row.SystemPrompt, &row.Tools, &row.Resources, &row.Context, &row.Agent, &row.Background, &row.Version, &row.Author, &row.License, &row.SourceURL, &row.SourceType, &row.SourceRef, &row.SourcePath, &row.SourceCredentialID, &row.SourceChecksum, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy, &row.WorkspaceID, &row.OwnerUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get skill %q: %w", id, err)
	}

	return skillRowToRecord(row)
}

func (p *Postgres) GetSkillByName(ctx context.Context, name string) (*service.Skill, error) {
	scope, actor, err := p.skillVisibilityScope(ctx)
	if err != nil {
		return nil, err
	}
	query, _, err := p.goqu.From(p.tableSkills).
		Select(skillColumns...).
		Where(scope, goqu.I("name").Eq(name)).
		Order(goqu.L("CASE WHEN owner_user_id = ? THEN 0 WHEN owner_user_id = '' THEN 1 ELSE 2 END", actor.UserID).Asc()).
		Limit(1).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get skill by name query: %w", err)
	}

	var row skillRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Description, &row.Category, &row.Tags, &row.SystemPrompt, &row.Tools, &row.Resources, &row.Context, &row.Agent, &row.Background, &row.Version, &row.Author, &row.License, &row.SourceURL, &row.SourceType, &row.SourceRef, &row.SourcePath, &row.SourceCredentialID, &row.SourceChecksum, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy, &row.WorkspaceID, &row.OwnerUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get skill by name %q: %w", name, err)
	}

	return skillRowToRecord(row)
}

func (p *Postgres) CreateSkill(ctx context.Context, sk service.Skill) (*service.Skill, error) {
	if err := service.ValidateSkillExecution(sk); err != nil {
		return nil, err
	}
	w, err := p.beginBusinessWrite(ctx, p.tableSkills, "skills.read", "")
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()
	if sk.WorkspaceID != "" && sk.WorkspaceID != w.actor.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	if sk.OwnerUserID != "" {
		if sk.OwnerUserID != w.actor.UserID {
			return nil, service.ErrAccessDenied
		}
	} else if !w.actor.Allows("skills.write", service.AccessResource{WorkspaceID: w.actor.WorkspaceID}) {
		return nil, service.ErrAccessDenied
	}
	if sk.Context == "fork" {
		if referenceErr := p.agentBusinessNamedReference(ctx, w, sk.Agent, sk.OwnerUserID != ""); referenceErr != nil {
			return nil, fmt.Errorf("validate forked skill agent: %w", referenceErr)
		}
	}
	toolsJSON, err := json.Marshal(sk.Tools)
	if err != nil {
		return nil, fmt.Errorf("marshal skill tools: %w", err)
	}
	resourcesJSON, err := json.Marshal(sk.Resources)
	if err != nil {
		return nil, fmt.Errorf("marshal skill resources: %w", err)
	}
	tagsJSON, err := json.Marshal(sk.Tags)
	if err != nil {
		return nil, fmt.Errorf("marshal skill tags: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := p.goqu.Insert(p.tableSkills).Rows(
		goqu.Record{
			"workspace_id":         w.actor.WorkspaceID,
			"owner_user_id":        sk.OwnerUserID,
			"id":                   id,
			"name":                 sk.Name,
			"description":          sk.Description,
			"category":             sk.Category,
			"tags":                 types.RawJSON(tagsJSON),
			"system_prompt":        sk.SystemPrompt,
			"tools":                types.RawJSON(toolsJSON),
			"resources":            types.RawJSON(resourcesJSON),
			"execution_context":    sk.Context,
			"execution_agent":      sk.Agent,
			"execution_background": sk.Background,
			"version":              sk.Version,
			"author":               sk.Author,
			"license":              sk.License,
			"source_url":           sk.SourceURL,
			"source_type":          sk.SourceType,
			"source_ref":           sk.SourceRef,
			"source_path":          sk.SourcePath,
			"source_credential_id": sk.SourceCredentialID,
			"source_checksum":      sk.SourceChecksum,
			"created_at":           now,
			"updated_at":           now,
			"created_by":           sk.CreatedBy,
			"updated_by":           sk.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert skill query: %w", err)
	}

	if _, err := w.tx.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create skill %q: %w", sk.Name, err)
	}
	if err = w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit skill: %w", err)
	}

	return &service.Skill{
		WorkspaceID:        w.actor.WorkspaceID,
		OwnerUserID:        sk.OwnerUserID,
		Scope:              skillScope(sk.OwnerUserID),
		ID:                 id,
		Name:               sk.Name,
		Description:        sk.Description,
		Category:           sk.Category,
		Tags:               sk.Tags,
		SystemPrompt:       sk.SystemPrompt,
		Tools:              sk.Tools,
		Resources:          sk.Resources,
		Context:            sk.Context,
		Agent:              sk.Agent,
		Background:         sk.Background,
		Version:            sk.Version,
		Author:             sk.Author,
		License:            sk.License,
		SourceURL:          sk.SourceURL,
		SourceType:         sk.SourceType,
		SourceRef:          sk.SourceRef,
		SourcePath:         sk.SourcePath,
		SourceCredentialID: sk.SourceCredentialID,
		SourceChecksum:     sk.SourceChecksum,
		CreatedAt:          now.Format(time.RFC3339),
		UpdatedAt:          now.Format(time.RFC3339),
		CreatedBy:          sk.CreatedBy,
		UpdatedBy:          sk.UpdatedBy,
	}, nil
}

func (p *Postgres) UpdateSkill(ctx context.Context, id string, sk service.Skill) (*service.Skill, error) {
	if err := service.ValidateSkillExecution(sk); err != nil {
		return nil, err
	}
	w, err := p.beginBusinessWrite(ctx, p.tableSkills, "skills.read", id)
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()
	w.predicate = ownedResourceWritePredicate(w.actor, "skills.write")
	if sk.WorkspaceID != "" && sk.WorkspaceID != w.actor.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	var ownerUserID string
	found, err := w.tx.From(p.tableSkills).Select("owner_user_id").Where(w.predicate, goqu.C("id").Eq(id)).ForKeyShare(goqu.Wait).ScanValContext(ctx, &ownerUserID)
	if err != nil {
		return nil, fmt.Errorf("load skill ownership: %w", err)
	}
	if !found {
		return nil, service.ErrAccessResourceNotFound
	}
	if sk.Context == "fork" {
		if referenceErr := p.agentBusinessNamedReference(ctx, w, sk.Agent, ownerUserID != ""); referenceErr != nil {
			return nil, fmt.Errorf("validate forked skill agent: %w", referenceErr)
		}
	}
	toolsJSON, err := json.Marshal(sk.Tools)
	if err != nil {
		return nil, fmt.Errorf("marshal skill tools: %w", err)
	}
	resourcesJSON, err := json.Marshal(sk.Resources)
	if err != nil {
		return nil, fmt.Errorf("marshal skill resources: %w", err)
	}
	tagsJSON, err := json.Marshal(sk.Tags)
	if err != nil {
		return nil, fmt.Errorf("marshal skill tags: %w", err)
	}

	now := time.Now().UTC()

	query, _, err := p.goqu.Update(p.tableSkills).Set(
		goqu.Record{
			"name":                 sk.Name,
			"description":          sk.Description,
			"category":             sk.Category,
			"tags":                 types.RawJSON(tagsJSON),
			"system_prompt":        sk.SystemPrompt,
			"tools":                types.RawJSON(toolsJSON),
			"resources":            types.RawJSON(resourcesJSON),
			"execution_context":    sk.Context,
			"execution_agent":      sk.Agent,
			"execution_background": sk.Background,
			"version":              sk.Version,
			"author":               sk.Author,
			"license":              sk.License,
			"source_url":           sk.SourceURL,
			"source_type":          sk.SourceType,
			"source_ref":           sk.SourceRef,
			"source_path":          sk.SourcePath,
			"source_credential_id": sk.SourceCredentialID,
			"source_checksum":      sk.SourceChecksum,
			"updated_at":           now,
			"updated_by":           sk.UpdatedBy,
		},
	).Where(w.predicate, goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update skill query: %w", err)
	}

	res, err := w.tx.ExecContext(ctx, query)
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
	if err = w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit skill update: %w", err)
	}

	return p.GetSkill(ctx, id)
}

func (p *Postgres) DeleteSkill(ctx context.Context, id string) error {
	w, err := p.beginBusinessWrite(ctx, p.tableSkills, "skills.read", id)
	if err != nil {
		return err
	}
	defer w.tx.Rollback()
	w.predicate = ownedResourceWritePredicate(w.actor, "skills.write")
	query, _, err := p.goqu.Delete(p.tableSkills).
		Where(w.predicate, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete skill query: %w", err)
	}

	_, err = w.tx.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete skill %q: %w", id, err)
	}

	return w.tx.Commit()
}

// skillRowToRecord converts a database row to a Skill.
func skillRowToRecord(row skillRow) (*service.Skill, error) {
	var tools []service.Tool
	if err := json.Unmarshal(row.Tools, &tools); err != nil {
		return nil, fmt.Errorf("unmarshal skill tools for %q: %w", row.ID, err)
	}

	var tags []string
	if len(row.Tags) > 0 {
		if err := json.Unmarshal(row.Tags, &tags); err != nil {
			return nil, fmt.Errorf("unmarshal skill tags for %q: %w", row.ID, err)
		}
	}
	var resources []service.SkillResource
	if len(row.Resources) > 0 {
		if err := json.Unmarshal(row.Resources, &resources); err != nil {
			return nil, fmt.Errorf("unmarshal skill resources for %q: %w", row.ID, err)
		}
	}

	return &service.Skill{
		WorkspaceID:        row.WorkspaceID,
		OwnerUserID:        row.OwnerUserID,
		Scope:              skillScope(row.OwnerUserID),
		ID:                 row.ID,
		Name:               row.Name,
		Description:        row.Description,
		Category:           row.Category,
		Tags:               tags,
		SystemPrompt:       row.SystemPrompt,
		Tools:              tools,
		Resources:          resources,
		Context:            row.Context,
		Agent:              row.Agent,
		Background:         row.Background,
		Version:            row.Version,
		Author:             row.Author,
		License:            row.License,
		SourceURL:          row.SourceURL,
		SourceType:         row.SourceType,
		SourceRef:          row.SourceRef,
		SourcePath:         row.SourcePath,
		SourceCredentialID: row.SourceCredentialID,
		SourceChecksum:     row.SourceChecksum,
		CreatedAt:          row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:          row.UpdatedAt.Format(time.RFC3339),
		CreatedBy:          row.CreatedBy,
		UpdatedBy:          row.UpdatedBy,
	}, nil
}

func skillScope(owner string) string {
	if owner != "" {
		return "personal"
	}
	return "workspace"
}

func (p *Postgres) PublishSkillToWorkspace(ctx context.Context, id, by string) (*service.Skill, error) {
	w, err := p.beginBusinessWrite(ctx, p.tableSkills, "skills.write", "")
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()
	var row skillRow
	found, err := w.tx.From(p.tableSkills).Select(skillColumns...).Where(
		goqu.C("workspace_id").Eq(w.actor.WorkspaceID),
		goqu.C("id").Eq(id),
		goqu.C("owner_user_id").Eq(w.actor.UserID),
	).ForShare(goqu.Wait).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("load personal skill for publish: %w", err)
	}
	if !found {
		return nil, service.ErrAccessResourceNotFound
	}
	if row.Context == "fork" {
		if err := p.agentBusinessNamedReference(ctx, w, row.Agent, false); err != nil {
			return nil, fmt.Errorf("publish forked skill agent: %w", err)
		}
	}
	newID := ulid.Make().String()
	now := time.Now().UTC()
	_, err = w.tx.Insert(p.tableSkills).Rows(goqu.Record{
		"workspace_id": w.actor.WorkspaceID, "owner_user_id": "", "id": newID,
		"name": row.Name, "description": row.Description, "category": row.Category,
		"tags": row.Tags, "system_prompt": row.SystemPrompt, "tools": row.Tools,
		"resources": row.Resources, "execution_context": row.Context,
		"execution_agent": row.Agent, "execution_background": row.Background,
		"version": row.Version, "author": row.Author,
		"license": row.License, "source_url": row.SourceURL, "source_type": row.SourceType,
		"source_ref": row.SourceRef, "source_path": row.SourcePath,
		"source_credential_id": row.SourceCredentialID, "source_checksum": row.SourceChecksum,
		"created_at": now, "updated_at": now, "created_by": by, "updated_by": by,
	}).Executor().ExecContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("publish skill %q: %w", row.Name, err)
	}
	if err := w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit skill publish: %w", err)
	}
	return p.GetSkill(ctx, newID)
}
