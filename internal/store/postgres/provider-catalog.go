package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"slices"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

func (p *Postgres) ListWorkspaceProviderCatalog(ctx context.Context) ([]service.ProviderCatalogEntry, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	var grants []workspaceProviderGrantRow
	if err := p.goqu.From(p.workspaceTable("workspace_provider_grants")).Where(goqu.Ex{"workspace_id": a.WorkspaceID}).ScanStructsContext(ctx, &grants); err != nil {
		return nil, fmt.Errorf("load provider catalog grants: %w", err)
	}
	patterns := map[string][]string{}
	ids := make([]string, 0, len(grants))
	for _, grant := range grants {
		var models []string
		if err := json.Unmarshal([]byte(grant.Patterns), &models); err != nil {
			return nil, fmt.Errorf("decode catalog model grant: %w", err)
		}
		patterns[grant.ProviderID] = models
		ids = append(ids, grant.ProviderID)
	}
	personalGrants := p.workspaceTable("personal_provider_grants")
	personalVisible := goqu.Or(
		goqu.C("owner_user_id").Eq(a.UserID),
		goqu.C("id").In(p.goqu.From(personalGrants).Select("provider_id").Where(goqu.Or(
			goqu.C("global").Eq(true),
			goqu.C("workspace_id").Eq(a.WorkspaceID),
		))),
	)
	var personalRows []providerRow
	provenance, _, executing := service.ExecutionFromContext(ctx)
	if !executing || provenance.ServiceID == "" {
		if err := p.goqu.From(p.tableProviders).
			Select(personalProviderSelect()...).
			Where(goqu.C("workspace_id").IsNull(), goqu.C("owner_user_id").Neq(""), personalVisible).
			Order(goqu.C("key").Asc()).
			ScanStructsContext(ctx, &personalRows); err != nil {
			return nil, fmt.Errorf("load personal provider catalog: %w", err)
		}
	}
	local := goqu.C("workspace_id").Eq(a.WorkspaceID)
	shared := goqu.And(goqu.C("workspace_id").Eq("legacy-default"), goqu.L("config->>'shared_with_all_workspaces' = 'true'"))
	if len(ids) > 0 {
		shared = goqu.And(goqu.C("workspace_id").Eq("legacy-default"), goqu.Or(goqu.L("config->>'shared_with_all_workspaces' = 'true'"), goqu.C("id").In(ids)))
	}
	var rows []providerRow
	if err := p.goqu.From(p.tableProviders).Where(goqu.Or(local, shared)).Order(goqu.C("key").Asc(), goqu.Case().When(local, 0).Else(1).Asc()).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("load workspace provider catalog: %w", err)
	}
	out := []service.ProviderCatalogEntry{}
	for _, row := range personalRows {
		var cfg config.LLMConfig
		if err := json.Unmarshal(row.Config, &cfg); err != nil {
			return nil, fmt.Errorf("decode personal provider metadata: %w", err)
		}
		if cfg.Disabled {
			continue
		}
		reference := service.PersonalProviderReference(row.ID)
		if !a.Allows("models.use", service.AccessResource{Kind: "models", WorkspaceID: a.WorkspaceID, ID: row.ID, Path: reference + "/" + cfg.Model}) {
			continue
		}
		models := slices.Clone(cfg.Models)
		if cfg.Model != "" && !slices.Contains(models, cfg.Model) {
			models = append(models, cfg.Model)
		}
		if models == nil {
			models = []string{}
		}
		scope := service.ProviderScopePersonal
		shared := row.OwnerUserID != a.UserID
		var global bool
		if _, err := p.goqu.From(personalGrants).Select("global").Where(goqu.Ex{"provider_id": row.ID, "global": true}).Limit(1).ScanValContext(ctx, &global); err != nil {
			return nil, fmt.Errorf("read personal provider global grant: %w", err)
		}
		if global {
			scope = service.ProviderScopeGlobal
			shared = true
		} else {
			var grantID string
			found, grantErr := p.goqu.From(personalGrants).Select("id").Where(goqu.Ex{"provider_id": row.ID, "workspace_id": a.WorkspaceID, "global": false}).Limit(1).ScanValContext(ctx, &grantID)
			if grantErr != nil {
				return nil, fmt.Errorf("read personal provider workspace grant: %w", grantErr)
			}
			if found {
				scope = service.ProviderScopeWorkspace
				shared = true
			}
		}
		out = append(out, service.ProviderCatalogEntry{Key: row.Key, Reference: reference, Scope: scope, Type: cfg.Type, DefaultModel: cfg.Model, Models: models, Shared: shared})
	}

	seen := map[string]bool{}
	for _, row := range rows {
		if seen[row.Key] {
			continue
		}
		seen[row.Key] = true // A local provider shadows a shared provider with the same key.
		if !a.Allows("providers.read", service.AccessResource{WorkspaceID: a.WorkspaceID, ID: row.ID}) {
			continue
		}
		// Metadata is plaintext even when credentials are encrypted. Do not
		// decrypt credentials just to populate a dashboard or model picker.
		var cfg config.LLMConfig
		if err := json.Unmarshal(row.Config, &cfg); err != nil {
			return nil, fmt.Errorf("decode provider metadata: %w", err)
		}
		// A disabled provider is hidden from model pickers rather than listed
		// as a choice that fails on use. It still shadows a same-named shared
		// provider above: the local row is the workspace's decision.
		if cfg.Disabled {
			continue
		}
		models := slices.Clone(cfg.Models)
		if cfg.Model != "" && !slices.Contains(models, cfg.Model) {
			models = append(models, cfg.Model)
		}
		isShared := row.WorkspaceID != a.WorkspaceID
		if isShared {
			if allowed, restricted := patterns[row.ID]; restricted {
				models = slices.DeleteFunc(models, func(model string) bool { return !providerModelMatches(allowed, model) })
				if !providerModelMatches(allowed, cfg.Model) {
					cfg.Model = ""
				}
			}
		}
		if models == nil {
			models = []string{}
		}
		scope := service.ProviderScopeWorkspace
		if isShared {
			scope = service.ProviderScopeGlobal
		}
		out = append(out, service.ProviderCatalogEntry{Key: row.Key, Scope: scope, Type: cfg.Type, DefaultModel: cfg.Model, Models: models, Shared: isShared})
	}
	virtual, err := p.ListGatewayVirtualProviderCatalog(ctx, a.WorkspaceID, a.UserID)
	if err != nil {
		return nil, fmt.Errorf("load virtual provider catalog: %w", err)
	}
	for _, entry := range virtual {
		if !seen[entry.Key] {
			out = append(out, entry)
		}
	}
	return out, nil
}

func providerModelMatches(patterns []string, model string) bool {
	for _, pattern := range patterns {
		if yes, _ := path.Match(pattern, model); yes {
			return true
		}
	}
	return false
}

func (p *Postgres) checkProviderSharingWrite(ctx context.Context, w *businessWrite, key string, nextShared bool) error {
	if nextShared && (!w.actor.PlatformAdmin || w.actor.WorkspaceID != "legacy-default") {
		return service.ErrAccessDenied
	}
	var shared bool
	_, err := w.tx.From(p.tableProviders).Select(goqu.L("COALESCE(config->>'shared_with_all_workspaces' = 'true', false)")).Where(w.predicate, goqu.Ex{"key": key}).ForUpdate(goqu.Wait).ScanValContext(ctx, &shared)
	if err != nil {
		return fmt.Errorf("check shared provider management: %w", err)
	}
	if shared && !w.actor.PlatformAdmin {
		return service.ErrAccessDenied
	}
	return nil
}
