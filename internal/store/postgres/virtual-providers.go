package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/query"

	"github.com/rakunlabs/at/internal/service"
)

var (
	_                          service.VirtualProviderStorer = (*Postgres)(nil)
	_                          service.ProviderRouteStorer   = (*Postgres)(nil)
	errVirtualProviderNotFound                               = errors.New("virtual provider not found")
)

type virtualProviderRow struct {
	ID           string    `db:"id"`
	WorkspaceID  string    `db:"workspace_id"`
	Key          string    `db:"key"`
	Name         string    `db:"name"`
	Description  string    `db:"description"`
	DefaultModel string    `db:"default_model"`
	Disabled     bool      `db:"disabled"`
	CreatedAt    time.Time `db:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"`
	CreatedBy    string    `db:"created_by"`
	UpdatedBy    string    `db:"updated_by"`
}

type virtualProviderModelRow struct {
	VirtualProviderID string `db:"virtual_provider_id"`
	Alias             string `db:"alias"`
	ProviderRef       string `db:"provider_ref"`
	Model             string `db:"model"`
	Position          int    `db:"position"`
}

func (p *Postgres) virtualProviderRecord(ctx context.Context, row virtualProviderRow) (*service.VirtualProvider, error) {
	var models []virtualProviderModelRow
	if err := p.goqu.From(p.tableVirtualProviderModels).Where(goqu.Ex{"virtual_provider_id": row.ID}).Order(goqu.C("position").Asc(), goqu.C("alias").Asc()).ScanStructsContext(ctx, &models); err != nil {
		return nil, fmt.Errorf("list virtual provider models: %w", err)
	}
	out := &service.VirtualProvider{WorkspaceID: row.WorkspaceID, ID: row.ID, Key: row.Key, Name: row.Name, Description: row.Description, DefaultModel: row.DefaultModel, Disabled: row.Disabled, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339), CreatedBy: row.CreatedBy, UpdatedBy: row.UpdatedBy}
	out.Models = make([]service.VirtualProviderModel, 0, len(models))
	for _, model := range models {
		out.Models = append(out.Models, service.VirtualProviderModel{Alias: model.Alias, ProviderRef: model.ProviderRef, Model: model.Model, Position: model.Position})
	}
	return out, nil
}

func (p *Postgres) ListVirtualProviders(ctx context.Context, _ *query.Query) (*service.ListResult[service.VirtualProvider], error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if !a.Allows("providers.read", service.AccessResource{WorkspaceID: a.WorkspaceID}) {
		return nil, service.ErrAccessDenied
	}
	var rows []virtualProviderRow
	if err = p.goqu.From(p.tableVirtualProviders).Where(goqu.Ex{"workspace_id": a.WorkspaceID}).Order(goqu.C("key").Asc()).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list virtual providers: %w", err)
	}
	out := make([]service.VirtualProvider, 0, len(rows))
	for _, row := range rows {
		record, readErr := p.virtualProviderRecord(ctx, row)
		if readErr != nil {
			return nil, readErr
		}
		out = append(out, *record)
	}
	return &service.ListResult[service.VirtualProvider]{Data: out, Meta: service.ListMeta{Total: uint64(len(out)), Limit: uint64(len(out))}}, nil
}

func (p *Postgres) GetVirtualProvider(ctx context.Context, id string) (*service.VirtualProvider, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	var row virtualProviderRow
	found, err := p.goqu.From(p.tableVirtualProviders).Where(goqu.Ex{"id": id, "workspace_id": a.WorkspaceID}).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("get virtual provider: %w", err)
	}
	if !found {
		return nil, nil
	}
	return p.virtualProviderRecord(ctx, row)
}

func (p *Postgres) validateVirtualTargets(ctx context.Context, workspaceID string, models []service.VirtualProviderModel) error {
	for _, model := range models {
		if _, personal := service.ParsePersonalProviderReference(model.ProviderRef); personal {
			return fmt.Errorf("virtual providers may reference workspace providers only")
		}
		var row providerRow
		found, err := p.goqu.From(p.tableProviders).Where(goqu.Ex{"workspace_id": workspaceID, "owner_user_id": "", "key": model.ProviderRef}).ScanStructContext(ctx, &row)
		if err != nil {
			return fmt.Errorf("validate virtual provider target: %w", err)
		}
		if !found {
			return fmt.Errorf("provider %q is not owned by this workspace", model.ProviderRef)
		}
	}
	return nil
}

func (p *Postgres) validateVirtualKey(ctx context.Context, workspaceID, key, excludingID string) error {
	var id string
	found, err := p.goqu.From(p.tableProviders).Select("id").Where(goqu.Ex{"workspace_id": workspaceID, "owner_user_id": "", "key": key}).Limit(1).ScanValContext(ctx, &id)
	if err != nil {
		return fmt.Errorf("check virtual provider key: %w", err)
	}
	if found {
		return fmt.Errorf("provider key %q is already used by a physical provider", key)
	}
	query := p.goqu.From(p.tableVirtualProviders).Select("id").Where(goqu.Ex{"workspace_id": workspaceID, "key": key})
	if excludingID != "" {
		query = query.Where(goqu.C("id").Neq(excludingID))
	}
	found, err = query.Limit(1).ScanValContext(ctx, &id)
	if err != nil {
		return fmt.Errorf("check virtual provider key: %w", err)
	}
	if found {
		return fmt.Errorf("virtual provider key %q already exists", key)
	}
	return nil
}

func (p *Postgres) CreateVirtualProvider(ctx context.Context, v service.VirtualProvider) (*service.VirtualProvider, error) {
	if err := service.ValidateVirtualProvider(v); err != nil {
		return nil, err
	}
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if !a.Allows("providers.write", service.AccessResource{WorkspaceID: a.WorkspaceID}) {
		return nil, service.ErrAccessDenied
	}
	if err = p.validateVirtualTargets(ctx, a.WorkspaceID, v.Models); err != nil {
		return nil, err
	}
	if err = p.validateVirtualKey(ctx, a.WorkspaceID, v.Key, ""); err != nil {
		return nil, err
	}
	if v.ID == "" {
		v.ID = ulid.Make().String()
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	_, err = tx.Insert(p.tableVirtualProviders).Rows(goqu.Record{"id": v.ID, "workspace_id": a.WorkspaceID, "key": strings.TrimSpace(v.Key), "name": strings.TrimSpace(v.Name), "description": strings.TrimSpace(v.Description), "default_model": v.DefaultModel, "disabled": v.Disabled, "created_by": a.UserID, "updated_by": a.UserID}).Executor().ExecContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("create virtual provider: %w", err)
	}
	for i, model := range v.Models {
		_, err = tx.Insert(p.tableVirtualProviderModels).Rows(goqu.Record{"virtual_provider_id": v.ID, "alias": model.Alias, "provider_ref": model.ProviderRef, "model": model.Model, "position": i}).Executor().ExecContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("create virtual provider model: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return p.GetVirtualProvider(ctx, v.ID)
}

func (p *Postgres) UpdateVirtualProvider(ctx context.Context, id string, v service.VirtualProvider) (*service.VirtualProvider, error) {
	if err := service.ValidateVirtualProvider(v); err != nil {
		return nil, err
	}
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if !a.Allows("providers.write", service.AccessResource{WorkspaceID: a.WorkspaceID}) {
		return nil, service.ErrAccessDenied
	}
	if err = p.validateVirtualTargets(ctx, a.WorkspaceID, v.Models); err != nil {
		return nil, err
	}
	if err = p.validateVirtualKey(ctx, a.WorkspaceID, v.Key, id); err != nil {
		return nil, err
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := tx.Update(p.tableVirtualProviders).Set(goqu.Record{"key": strings.TrimSpace(v.Key), "name": strings.TrimSpace(v.Name), "description": strings.TrimSpace(v.Description), "default_model": v.DefaultModel, "disabled": v.Disabled, "updated_at": goqu.L("NOW()"), "updated_by": a.UserID}).Where(goqu.Ex{"id": id, "workspace_id": a.WorkspaceID}).Executor().ExecContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("update virtual provider: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil, service.ErrAccessResourceNotFound
	}
	if _, err = tx.Delete(p.tableVirtualProviderModels).Where(goqu.Ex{"virtual_provider_id": id}).Executor().ExecContext(ctx); err != nil {
		return nil, err
	}
	for i, model := range v.Models {
		if _, err = tx.Insert(p.tableVirtualProviderModels).Rows(goqu.Record{"virtual_provider_id": id, "alias": model.Alias, "provider_ref": model.ProviderRef, "model": model.Model, "position": i}).Executor().ExecContext(ctx); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return p.GetVirtualProvider(ctx, id)
}

func (p *Postgres) DeleteVirtualProvider(ctx context.Context, id string) error {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return err
	}
	if !a.Allows("providers.write", service.AccessResource{WorkspaceID: a.WorkspaceID}) {
		return service.ErrAccessDenied
	}
	result, err := p.goqu.Delete(p.tableVirtualProviders).Where(goqu.Ex{"id": id, "workspace_id": a.WorkspaceID}).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("delete virtual provider: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return service.ErrAccessResourceNotFound
	}
	return nil
}

func (p *Postgres) ListVirtualProviderGrants(ctx context.Context, virtualID string) ([]service.VirtualProviderGrant, error) {
	if _, err := p.GetVirtualProvider(ctx, virtualID); err != nil {
		return nil, err
	}
	var rows []struct {
		ID                string    `db:"id"`
		VirtualProviderID string    `db:"virtual_provider_id"`
		WorkspaceID       string    `db:"workspace_id"`
		Patterns          []byte    `db:"model_patterns"`
		Allow             bool      `db:"allow_user_overrides"`
		Max               float64   `db:"max_user_limit_cents"`
		CreatedAt         time.Time `db:"created_at"`
		CreatedBy         string    `db:"created_by"`
	}
	if err := p.goqu.From(p.tableVirtualProviderGrants).Where(goqu.Ex{"virtual_provider_id": virtualID}).Order(goqu.C("workspace_id").Asc()).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list virtual provider grants: %w", err)
	}
	out := make([]service.VirtualProviderGrant, 0, len(rows))
	for _, row := range rows {
		var patterns []string
		if err := json.Unmarshal(row.Patterns, &patterns); err != nil {
			return nil, err
		}
		out = append(out, service.VirtualProviderGrant{ID: row.ID, VirtualProviderID: row.VirtualProviderID, WorkspaceID: row.WorkspaceID, ModelPatterns: patterns, AllowUserOverrides: row.Allow, MaxUserLimitCents: row.Max, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339), CreatedBy: row.CreatedBy})
	}
	return out, nil
}

func (p *Postgres) SaveVirtualProviderGrant(ctx context.Context, grant service.VirtualProviderGrant) (*service.VirtualProviderGrant, error) {
	if grant.WorkspaceID == "" || len(grant.ModelPatterns) == 0 || grant.MaxUserLimitCents < 0 {
		return nil, fmt.Errorf("workspace_id, model_patterns and a non-negative maximum are required")
	}
	if _, err := p.GetVirtualProvider(ctx, grant.VirtualProviderID); err != nil {
		return nil, err
	}
	a, _ := service.AccessPrincipalFromContext(ctx)
	if grant.ID == "" {
		grant.ID = ulid.Make().String()
	}
	patterns, _ := json.Marshal(grant.ModelPatterns)
	_, err := p.goqu.Insert(p.tableVirtualProviderGrants).Rows(goqu.Record{"id": grant.ID, "virtual_provider_id": grant.VirtualProviderID, "workspace_id": grant.WorkspaceID, "model_patterns": string(patterns), "allow_user_overrides": grant.AllowUserOverrides, "max_user_limit_cents": grant.MaxUserLimitCents, "created_by": a.UserID}).OnConflict(goqu.DoUpdate("virtual_provider_id,workspace_id", goqu.Record{"model_patterns": string(patterns), "allow_user_overrides": grant.AllowUserOverrides, "max_user_limit_cents": grant.MaxUserLimitCents})).Executor().ExecContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("save virtual provider grant: %w", err)
	}
	grants, err := p.ListVirtualProviderGrants(ctx, grant.VirtualProviderID)
	if err != nil {
		return nil, err
	}
	for i := range grants {
		if grants[i].WorkspaceID == grant.WorkspaceID {
			return &grants[i], nil
		}
	}
	return nil, service.ErrAccessResourceNotFound
}

func (p *Postgres) DeleteVirtualProviderGrant(ctx context.Context, virtualID, workspaceID string) error {
	if _, err := p.GetVirtualProvider(ctx, virtualID); err != nil {
		return err
	}
	_, err := p.goqu.Delete(p.tableVirtualProviderGrants).Where(goqu.Ex{"virtual_provider_id": virtualID, "workspace_id": workspaceID}).Executor().ExecContext(ctx)
	return err
}

func (p *Postgres) virtualRoute(ctx context.Context, workspaceID, userID, key, alias string, checkAccess bool) (*service.ProviderRoute, error) {
	local := goqu.C("workspace_id").Eq(workspaceID)
	granted := goqu.C("id").In(p.goqu.From(p.tableVirtualProviderGrants).Select("virtual_provider_id").Where(goqu.Ex{"workspace_id": workspaceID}))
	var virtual virtualProviderRow
	found, err := p.goqu.From(p.tableVirtualProviders).Where(goqu.Ex{"key": key}, goqu.Or(local, granted)).Order(goqu.Case().When(local, 0).Else(1).Asc()).Limit(1).ScanStructContext(ctx, &virtual)
	if err != nil {
		return nil, fmt.Errorf("resolve virtual provider: %w", err)
	}
	if !found || virtual.Disabled {
		return nil, errVirtualProviderNotFound
	}
	if virtual.WorkspaceID != workspaceID {
		var raw []byte
		grantFound, grantErr := p.goqu.From(p.tableVirtualProviderGrants).Select("model_patterns").Where(goqu.Ex{"virtual_provider_id": virtual.ID, "workspace_id": workspaceID}).ScanValContext(ctx, &raw)
		if grantErr != nil || !grantFound {
			return nil, service.ErrAccessDenied
		}
		var patterns []string
		if json.Unmarshal(raw, &patterns) != nil || !providerModelMatches(patterns, alias) {
			return nil, service.ErrAccessDenied
		}
	}
	if checkAccess {
		a, ok := service.AccessPrincipalFromContext(ctx)
		if !ok || a.UserID != userID || !a.Allows("models.use", service.AccessResource{Kind: "models", WorkspaceID: workspaceID, ID: virtual.ID, Path: key + "/" + alias}) {
			return nil, service.ErrAccessDenied
		}
	}
	var mapping virtualProviderModelRow
	found, err = p.goqu.From(p.tableVirtualProviderModels).Where(goqu.Ex{"virtual_provider_id": virtual.ID, "alias": alias}).ScanStructContext(ctx, &mapping)
	if err != nil || !found {
		return nil, service.ErrAccessResourceNotFound
	}
	var provider providerRow
	found, err = p.goqu.From(p.tableProviders).Where(goqu.Ex{"workspace_id": virtual.WorkspaceID, "owner_user_id": "", "key": mapping.ProviderRef}).ScanStructContext(ctx, &provider)
	if err != nil {
		return nil, fmt.Errorf("resolve virtual provider target: %w", err)
	}
	if !found {
		return nil, service.ErrAccessResourceNotFound
	}
	p.encKeyMu.RLock()
	record, err := rowToRecord(provider, p.encKey)
	p.encKeyMu.RUnlock()
	if err != nil {
		return nil, err
	}
	if record.Config.Disabled {
		return nil, service.ErrProviderDisabled
	}
	if len(record.Config.Models) > 0 {
		available := false
		for _, model := range record.Config.Models {
			available = available || model == mapping.Model
		}
		if !available {
			return nil, service.ErrAccessResourceNotFound
		}
	}
	return &service.ProviderRoute{Record: *record, ActualModel: mapping.Model, VirtualProviderID: virtual.ID, VirtualKey: virtual.Key}, nil
}

func (p *Postgres) ResolveWorkspaceProviderRoute(ctx context.Context, key, model string) (*service.ProviderRoute, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if route, routeErr := p.virtualRoute(ctx, a.WorkspaceID, a.UserID, key, model, true); routeErr == nil {
		return route, nil
	} else if routeErr != nil && !errors.Is(routeErr, errVirtualProviderNotFound) {
		return nil, routeErr
	}
	record, err := p.ResolveWorkspaceProviderForUse(ctx, key, model)
	if err != nil {
		return nil, err
	}
	return &service.ProviderRoute{Record: *record, ActualModel: model}, nil
}

func (p *Postgres) ResolveGatewayProviderRoute(ctx context.Context, workspaceID, userID, key, model string) (*service.ProviderRoute, error) {
	return p.virtualRoute(ctx, workspaceID, userID, key, model, false)
}

func (p *Postgres) ListGatewayVirtualProviderCatalog(ctx context.Context, workspaceID, userID string) ([]service.ProviderCatalogEntry, error) {
	if workspaceID == "" {
		return []service.ProviderCatalogEntry{}, nil
	}
	local := goqu.C("workspace_id").Eq(workspaceID)
	granted := goqu.C("id").In(p.goqu.From(p.tableVirtualProviderGrants).Select("virtual_provider_id").Where(goqu.Ex{"workspace_id": workspaceID}))
	var rows []virtualProviderRow
	if err := p.goqu.From(p.tableVirtualProviders).Where(goqu.Ex{"disabled": false}, goqu.Or(local, granted)).Order(goqu.C("key").Asc(), goqu.Case().When(local, 0).Else(1).Asc()).ScanStructsContext(ctx, &rows); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []service.ProviderCatalogEntry{}
	for _, row := range rows {
		if seen[row.Key] {
			continue
		}
		seen[row.Key] = true
		var models []virtualProviderModelRow
		if err := p.goqu.From(p.tableVirtualProviderModels).Where(goqu.Ex{"virtual_provider_id": row.ID}).Order(goqu.C("position").Asc()).ScanStructsContext(ctx, &models); err != nil {
			return nil, err
		}
		aliases := make([]string, 0, len(models))
		for _, model := range models {
			if row.WorkspaceID == workspaceID {
				aliases = append(aliases, model.Alias)
				continue
			}
			var raw []byte
			_, _ = p.goqu.From(p.tableVirtualProviderGrants).Select("model_patterns").Where(goqu.Ex{"virtual_provider_id": row.ID, "workspace_id": workspaceID}).ScanValContext(ctx, &raw)
			var patterns []string
			_ = json.Unmarshal(raw, &patterns)
			if providerModelMatches(patterns, model.Alias) {
				aliases = append(aliases, model.Alias)
			}
		}
		if len(aliases) == 0 {
			continue
		}
		defaultModel := row.DefaultModel
		if !containsString(aliases, defaultModel) {
			defaultModel = aliases[0]
		}
		out = append(out, service.ProviderCatalogEntry{Key: row.Key, Scope: service.ProviderScopeWorkspace, Type: "virtual", DefaultModel: defaultModel, Models: aliases, Shared: row.WorkspaceID != workspaceID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
