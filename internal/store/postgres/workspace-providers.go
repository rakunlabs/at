package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"path"

	"github.com/doug-martin/goqu/v9"
	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

var _ service.WorkspaceProviderStorer = (*Postgres)(nil)

type workspaceProviderGrantRow struct {
	WorkspaceID string `db:"workspace_id"`
	ProviderID  string `db:"provider_id"`
	Patterns    string `db:"model_patterns"`
}

func (p *Postgres) ListWorkspaceProviderGrants(ctx context.Context) ([]service.WorkspaceProviderGrant, error) {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin provider grants: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "providers.read")
	if err != nil {
		return nil, err
	}
	var rows []workspaceProviderGrantRow
	if err = tx.From(p.workspaceTable("workspace_provider_grants")).Where(goqu.Ex{"workspace_id": a.WorkspaceID}).Order(goqu.C("provider_id").Asc()).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list provider grants: %w", err)
	}
	out := make([]service.WorkspaceProviderGrant, 0, len(rows))
	for _, r := range rows {
		var patterns []string
		if err = json.Unmarshal([]byte(r.Patterns), &patterns); err != nil {
			return nil, fmt.Errorf("decode provider grant: %w", err)
		}
		out = append(out, service.WorkspaceProviderGrant{WorkspaceID: r.WorkspaceID, ProviderID: r.ProviderID, ModelPatterns: patterns})
	}
	return out, nil
}
func (p *Postgres) SaveWorkspaceProviderGrant(ctx context.Context, v service.WorkspaceProviderGrant) error {
	if len(v.ModelPatterns) == 0 {
		return service.ErrAccessDenied
	}
	if err := service.ValidateAccessGrant(service.AccessGrant{Capability: "models.use", PathPatterns: v.ModelPatterns}); err != nil {
		return err
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin provider grant: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "credentials.manage")
	if err != nil {
		return err
	}
	if !a.PlatformAdmin || v.WorkspaceID != "" && v.WorkspaceID != a.WorkspaceID {
		return service.ErrAccessDenied
	}
	var provider string
	found, err := tx.From(p.tableProviders).Select("id").Where(goqu.Ex{"id": v.ProviderID, "workspace_id": "legacy-default"}).ForKeyShare(goqu.Wait).ScanValContext(ctx, &provider)
	if err != nil {
		return fmt.Errorf("validate platform provider: %w", err)
	}
	if !found {
		return service.ErrAccessResourceNotFound
	}
	patterns, _ := json.Marshal(v.ModelPatterns)
	if _, err = tx.Insert(p.workspaceTable("workspace_provider_grants")).Rows(goqu.Record{"workspace_id": a.WorkspaceID, "provider_id": v.ProviderID, "model_patterns": string(patterns)}).OnConflict(goqu.DoUpdate("workspace_id,provider_id", goqu.Record{"model_patterns": string(patterns)})).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("save provider grant: %w", err)
	}
	if err = p.bumpWorkspacePolicy(ctx, tx, a.WorkspaceID); err != nil {
		return err
	}
	return tx.Commit()
}
func (p *Postgres) DeleteWorkspaceProviderGrant(ctx context.Context, id string) error {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete provider grant: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "credentials.manage")
	if err != nil {
		return err
	}
	if !a.PlatformAdmin {
		return service.ErrAccessDenied
	}
	if _, err = tx.Delete(p.workspaceTable("workspace_provider_grants")).Where(goqu.Ex{"workspace_id": a.WorkspaceID, "provider_id": id}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("delete provider grant: %w", err)
	}
	if err = p.bumpWorkspacePolicy(ctx, tx, a.WorkspaceID); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *Postgres) ResolveWorkspaceProviderForUse(ctx context.Context, key, model string) (*service.ProviderRecord, error) {
	if key == "" || model == "" {
		return nil, service.ErrAccessDenied
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin provider use: %w", err)
	}
	defer tx.Rollback()
	var a service.AccessPrincipal
	if _, ok := service.AccessPrincipalFromContext(ctx); !ok && service.LegacyWorkspaceAccessFromContext(ctx) {
		a, err = p.legacyBusinessPrincipal(ctx, tx, true)
	} else {
		a, err = p.workspaceActor(ctx, tx, "workspace.read")
	}
	if err != nil {
		return nil, err
	}
	var row providerRow
	found, err := tx.From(p.tableProviders).Where(goqu.Ex{"workspace_id": a.WorkspaceID, "key": key}).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace provider: %w", err)
	}
	if !found {
		found, err = tx.From(p.tableProviders).Where(goqu.Ex{"workspace_id": "legacy-default", "key": key}, goqu.C("id").In(tx.From(p.workspaceTable("workspace_provider_grants")).Select("provider_id").Where(goqu.Ex{"workspace_id": a.WorkspaceID}))).ScanStructContext(ctx, &row)
		if err != nil {
			return nil, fmt.Errorf("resolve platform provider grant: %w", err)
		}
		if !found {
			return nil, service.ErrAccessResourceNotFound
		}
		var raw string
		if _, err = tx.From(p.workspaceTable("workspace_provider_grants")).Select("model_patterns").Where(goqu.Ex{"workspace_id": a.WorkspaceID, "provider_id": row.ID}).ScanValContext(ctx, &raw); err != nil {
			return nil, fmt.Errorf("read model grant: %w", err)
		}
		var patterns []string
		if json.Unmarshal([]byte(raw), &patterns) != nil || len(patterns) == 0 || service.ValidateAccessGrant(service.AccessGrant{Capability: "models.use", PathPatterns: patterns}) != nil {
			return nil, service.ErrAccessDenied
		}
		matched := false
		for _, pattern := range patterns {
			if yes, _ := path.Match(pattern, model); yes {
				matched = true
				break
			}
		}
		if !matched {
			return nil, service.ErrAccessDenied
		}
	}
	if !a.Allows("models.use", service.AccessResource{Kind: "models", WorkspaceID: a.WorkspaceID, ID: row.ID, Path: key + "/" + model}) {
		return nil, service.ErrAccessDenied
	}
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	return rowToRecord(row, p.encKey)
}

func providerReadDTO(a service.AccessPrincipal, record *service.ProviderRecord) {
	if !a.Allows("credentials.manage", service.AccessResource{WorkspaceID: record.WorkspaceID, ID: record.ID}) {
		c := record.Config
		record.Config = config.LLMConfig{Type: c.Type, AuthType: c.AuthType, Model: c.Model, Models: c.Models, EmbeddingModels: c.EmbeddingModels}
	}
}
