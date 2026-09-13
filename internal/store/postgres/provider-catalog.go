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
	local := goqu.C("workspace_id").Eq(a.WorkspaceID)
	shared := goqu.And(goqu.C("workspace_id").Eq("legacy-default"), goqu.L("config->>'shared_with_all_workspaces' = 'true'"))
	if len(ids) > 0 {
		shared = goqu.And(goqu.C("workspace_id").Eq("legacy-default"), goqu.Or(goqu.L("config->>'shared_with_all_workspaces' = 'true'"), goqu.C("id").In(ids)))
	}
	var rows []providerRow
	if err := p.goqu.From(p.tableProviders).Where(goqu.Or(local, shared)).Order(goqu.C("key").Asc(), goqu.Case().When(local, 0).Else(1).Asc()).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("load workspace provider catalog: %w", err)
	}
	seen := map[string]bool{}
	out := []service.ProviderCatalogEntry{}
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
		out = append(out, service.ProviderCatalogEntry{Key: row.Key, Type: cfg.Type, DefaultModel: cfg.Model, Models: models, Shared: isShared})
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
