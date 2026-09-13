package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

func TestWorkspaceDashboardAndSharedProviderCatalog(t *testing.T) {
	f := newMachineFixture(t)
	actor, _ := service.AccessPrincipalFromContext(f.ctx)
	globalActor, _, err := f.store.ResolveWorkspaceAccess(t.Context(), "legacy-default", actor.UserID, actor.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	global := service.WithAccessPrincipal(t.Context(), globalActor)
	if _, err := f.store.CreateProvider(f.ctx, service.ProviderRecord{Key: "local", Config: config.LLMConfig{Type: "openai", Model: "local-model", APIKey: "local-secret"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateProvider(global, service.ProviderRecord{Key: "private-global", Config: config.LLMConfig{Type: "openai", Model: "hidden-model", APIKey: "hidden-secret"}}); err != nil {
		t.Fatal(err)
	}
	shared, err := f.store.CreateProvider(global, service.ProviderRecord{Key: "shared", Config: config.LLMConfig{Type: "openai", Model: "m1", Models: []string{"m1", "m2"}, APIKey: "shared-secret", SharedWithAllWorkspaces: true}})
	if err != nil {
		t.Fatal(err)
	}
	info := func() infoResponse {
		t.Helper()
		w := httptest.NewRecorder()
		f.s.InfoAPI(w, httptest.NewRequest(http.MethodGet, "/api/v1/info", nil).WithContext(f.ctx))
		var response infoResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil {
			t.Fatalf("workspace info: %d %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "private-global") {
			t.Fatalf("dashboard leaked credentials or unshared providers: %s", w.Body.String())
		}
		return response
	}
	if got := info(); len(got.Providers) != 2 {
		t.Fatalf("workspace catalog: %+v", got.Providers)
	}
	used, err := f.store.ResolveWorkspaceProviderForUse(f.ctx, "shared", "m2")
	if err != nil || used.ID != shared.ID {
		t.Fatalf("shared provider unavailable: %+v %v", used, err)
	}
	if model, err := f.store.ExecutionProviderDefaultModel(f.ctx, "shared"); err != nil || model != "m1" {
		t.Fatalf("shared default model: %q %v", model, err)
	}
	if _, err := f.store.CreateAgent(f.ctx, service.Agent{Name: "shared-model-agent", Config: service.AgentConfig{Provider: "shared", Model: "m1"}}); err != nil {
		t.Fatalf("shared provider rejected as agent reference: %v", err)
	}
	shared.Config.SharedWithAllWorkspaces = false
	if _, err := f.store.UpdateProvider(global, shared.Key, *shared); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ResolveWorkspaceProviderForUse(f.ctx, "shared", "m2"); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("unshared provider remained usable: %v", err)
	}
	if got := info(); len(got.Providers) != 1 {
		t.Fatal("dashboard retained unshared provider")
	}
	shared.Config.SharedWithAllWorkspaces = true
	if _, err := f.store.UpdateProvider(global, shared.Key, *shared); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateProvider(f.ctx, service.ProviderRecord{Key: "shared", Config: config.LLMConfig{Type: "gemini", Model: "own-model", APIKey: "own-secret"}}); err != nil {
		t.Fatal(err)
	}
	if used, err := f.store.ResolveWorkspaceProviderForUse(f.ctx, "shared", "own-model"); err != nil || used.WorkspaceID != f.workspace {
		t.Fatalf("local provider did not shadow shared: %+v %v", used, err)
	}
	if got := info(); len(got.Providers) != 2 || got.Providers[1].Shared {
		t.Fatalf("duplicate shared/local catalog: %+v", got.Providers)
	}
	if _, err := f.store.CreateProvider(f.ctx, service.ProviderRecord{Key: "wrong-home", Config: config.LLMConfig{Type: "openai", SharedWithAllWorkspaces: true}}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("shared provider created outside global home: %v", err)
	}
	if err := f.store.SetWorkspaceMember(global, service.WorkspaceMembership{WorkspaceID: "legacy-default", UserID: f.member, Role: "owner", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	owner, _, err := f.store.ResolveWorkspaceAccess(t.Context(), "legacy-default", f.member, "")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := service.WithAccessPrincipal(t.Context(), owner)
	if _, err := f.store.CreateProvider(ownerCtx, service.ProviderRecord{Key: "unauthorized-global", Config: config.LLMConfig{Type: "openai", SharedWithAllWorkspaces: true}}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("workspace owner published global credentials: %v", err)
	}
	if _, err := f.store.UpdateProvider(ownerCtx, shared.Key, *shared); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("workspace owner changed shared credentials: %v", err)
	}
	if err := f.store.DeleteProvider(ownerCtx, shared.Key); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("workspace owner deleted shared provider: %v", err)
	}
}

func TestSharedProviderReusesTransportAcrossWorkspaces(t *testing.T) {
	f := newMachineFixture(t)
	actor, _ := service.AccessPrincipalFromContext(f.ctx)
	globalActor, _, err := f.store.ResolveWorkspaceAccess(t.Context(), "legacy-default", actor.UserID, actor.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	global := service.WithAccessPrincipal(t.Context(), globalActor)
	if _, err := f.store.CreateProvider(global, service.ProviderRecord{Key: "shared", Config: config.LLMConfig{Type: "openai", Model: "m", SharedWithAllWorkspaces: true}}); err != nil {
		t.Fatal(err)
	}
	other, err := f.store.CreateWorkspace(global, "future workspace", actor.UserID)
	if err != nil {
		t.Fatal(err)
	}
	otherActor, _, err := f.store.ResolveWorkspaceAccess(t.Context(), other.ID, actor.UserID, actor.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	otherCtx := service.WithAccessPrincipal(t.Context(), otherActor)
	other.ExecutionEnabled = true
	if _, err := f.store.UpdateWorkspace(otherCtx, *other); err != nil {
		t.Fatal(err)
	}
	otherCtx, err = f.s.bindRuntimePrincipal(otherCtx, "test")
	if err != nil {
		t.Fatal(err)
	}
	var builds atomic.Int32
	f.s.providerFactory = func(config.LLMConfig) (service.LLMProvider, error) {
		builds.Add(1)
		return codexAuthTestProvider{}, nil
	}
	for _, ctx := range []context.Context{f.ctx, otherCtx} {
		provider, _, err := f.s.runtimeProviderLookup(ctx, "shared")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := provider.Chat(ctx, "m", nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if builds.Load() != 1 {
		t.Fatalf("shared provider built %d transports", builds.Load())
	}
}

func TestPricingTargetsWorkspaceModelsNotGlobalRegistry(t *testing.T) {
	f := newMachineFixture(t)
	f.s.agentBudgetStore = &pricingTestBudgetStore{}
	f.s.providers = map[string]ProviderInfo{"unrelated-global": {providerType: "openai", defaultModel: "not-my-model"}}
	if _, err := f.store.CreateProvider(f.ctx, service.ProviderRecord{Key: "my-provider", Config: config.LLMConfig{Type: "anthropic", Model: "my-alias", Models: []string{"my-alias", "my-second-model"}}}); err != nil {
		t.Fatal(err)
	}
	catalog := []modelPricingSourceItem{{Provider: "anthropic", Model: "official-model", PromptPricePer1M: 3, CompletionPricePer1M: 15}}
	items, err := f.s.buildCatalogPricingPreview(f.ctx, llmPricesPricingSource, catalog, []modelPricingSyncKeyItem{{ProviderKey: "my-provider", Model: "my-alias", SourceProvider: "anthropic", SourceModel: "official-model"}})
	if err != nil || len(items) != 2 {
		t.Fatalf("pricing targets: %+v %v", items, err)
	}
	for _, item := range items {
		if item.ProviderKey != "my-provider" {
			t.Fatal("global registry model leaked into pricing targets")
		}
		if item.Model == "my-alias" && (!item.Matched || item.SourceModel != "official-model" || item.SourcePromptPricePer1M != 3) {
			t.Fatalf("own model mapping: %+v", item)
		}
	}
}
