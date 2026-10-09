package postgres

import (
	"context"
	"testing"

	"github.com/doug-martin/goqu/v9"
	"github.com/worldline-go/types"

	"github.com/rakunlabs/at/internal/service"
)

func TestProviderModelPricesPostgres(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := context.Background()

	if _, err := p.goqu.Insert(p.executionTable("workspaces")).Rows(goqu.Record{"id": "team", "name": "Team"}).Executor().ExecContext(ctx); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	insert := func(id, key string, workspace any, owner, cfg string) {
		t.Helper()
		if _, err := p.goqu.Insert(p.tableProviders).Rows(goqu.Record{
			"id": id, "key": key, "workspace_id": workspace, "owner_user_id": owner, "config": types.RawJSON(cfg),
		}).Executor().ExecContext(ctx); err != nil {
			t.Fatalf("insert provider %s: %v", id, err)
		}
	}
	insert("p-default", "openai", service.DefaultWorkspaceID, "", `{"type":"openai","model_pricing":{"gpt":{"input":1,"output":2}}}`)
	insert("p-team-unpriced", "openai", "team", "", `{"type":"openai"}`)
	insert("p-shared", "local", service.DefaultWorkspaceID, "", `{"type":"openai","model_pricing":{"llama":{"input":0,"output":0}}}`)
	insert("p-personal", "openai", nil, "user-1", `{"type":"openai","model_pricing":{"gpt":{"input":5,"output":6,"cache_read":0.5}}}`)

	personal := service.PersonalProviderReference("p-personal")
	got, err := p.ProviderModelPrices(ctx, service.DefaultWorkspaceID, []string{"openai", "local", personal, "missing"})
	if err != nil {
		t.Fatalf("ProviderModelPrices: %v", err)
	}
	if got["openai"]["gpt"].Input != 1 {
		t.Fatalf("default openai = %+v", got["openai"])
	}
	if price, ok := got["local"]["llama"]; !ok || price.Input != 0 {
		t.Fatalf("explicit free price lost: %+v", got["local"])
	}
	if got[personal]["gpt"].Input != 5 || got[personal]["gpt"].CacheRead != 0.5 {
		t.Fatalf("personal = %+v", got[personal])
	}
	if _, ok := got["missing"]; ok {
		t.Fatal("unknown provider reported prices")
	}

	// The team's own openai provider shadows Default's, even without prices.
	got, err = p.ProviderModelPrices(ctx, "team", []string{"openai", "local"})
	if err != nil {
		t.Fatalf("ProviderModelPrices(team): %v", err)
	}
	if _, ok := got["openai"]; ok {
		t.Fatalf("shadowed Default provider priced the team's provider: %+v", got["openai"])
	}
	if _, ok := got["local"]; !ok {
		t.Fatal("Default provider not visible from team")
	}
}
