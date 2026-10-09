package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

var _ service.ProviderPriceStorer = (*Postgres)(nil)

type providerPriceRow struct {
	ID          string  `db:"id"`
	Key         string  `db:"key"`
	WorkspaceID *string `db:"workspace_id"`
	Pricing     []byte  `db:"pricing"`
}

// ProviderModelPrices reads only config->'model_pricing', which is stored in
// plain JSON (it is not a credential), so no decryption is involved.
func (p *Postgres) ProviderModelPrices(ctx context.Context, workspaceID string, keys []string) (map[string]map[string]config.ModelPrice, error) {
	out := map[string]map[string]config.ModelPrice{}
	var ids, bare []string
	for _, key := range keys {
		if id, ok := service.ParsePersonalProviderReference(key); ok {
			ids = append(ids, id)
		} else if key != "" {
			bare = append(bare, key)
		}
	}
	if len(ids) == 0 && len(bare) == 0 {
		return out, nil
	}

	workspaces := []string{service.DefaultWorkspaceID}
	if workspaceID != "" && workspaceID != service.DefaultWorkspaceID {
		workspaces = append(workspaces, workspaceID)
	}
	var conds []goqu.Expression
	if len(ids) > 0 {
		conds = append(conds, goqu.And(goqu.C("id").In(ids), goqu.C("owner_user_id").Neq(""), goqu.C("workspace_id").IsNull()))
	}
	if len(bare) > 0 {
		conds = append(conds, goqu.And(goqu.C("key").In(bare), goqu.C("owner_user_id").Eq(""), goqu.C("workspace_id").In(workspaces)))
	}

	// No model_pricing filter here: a selected-workspace provider without
	// prices must still shadow a priced Default provider with the same key.
	var rows []providerPriceRow
	if err := p.goqu.From(p.tableProviders).
		Select("id", "key", "workspace_id", goqu.L("COALESCE(config->'model_pricing', 'null'::jsonb)").As("pricing")).
		Where(goqu.Or(conds...)).
		ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("load provider model prices: %w", err)
	}

	type candidate struct {
		prices   map[string]config.ModelPrice
		selected bool
	}
	bareRows := map[string]candidate{}
	for _, row := range rows {
		var prices map[string]config.ModelPrice
		if len(row.Pricing) > 0 {
			if err := json.Unmarshal(row.Pricing, &prices); err != nil {
				prices = nil
			}
		}
		if row.WorkspaceID == nil {
			if len(prices) > 0 {
				out[service.PersonalProviderReference(row.ID)] = prices
			}
			continue
		}
		selected := *row.WorkspaceID == workspaceID
		if existing, seen := bareRows[row.Key]; seen && existing.selected && !selected {
			continue
		}
		bareRows[row.Key] = candidate{prices: prices, selected: selected}
	}
	for key, c := range bareRows {
		if len(c.prices) > 0 {
			out[key] = c.prices
		}
	}
	return out, nil
}
