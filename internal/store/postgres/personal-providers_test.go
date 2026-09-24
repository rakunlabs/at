package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

func TestPersonalProviderOwnershipPublicationAndResolution(t *testing.T) {
	p, adminCtx, workspace, platformAdmin := workspaceFixture(t)
	if _, err := p.goqu.Update(p.workspaceTable("workspaces")).Set(goqu.Record{"execution_enabled": true}).Where(goqu.Ex{"id": workspace.ID}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	alice := workspaceUser(t, p, "personal-provider-alice")
	bob := workspaceUser(t, p, "personal-provider-bob")
	aliceCtx := workspaceMember(t, p, adminCtx, workspace.ID, alice, "admin")
	bobCtx := workspaceMember(t, p, adminCtx, workspace.ID, bob, "member")

	custom := config.LLMConfig{
		Type: "openai", Model: "alice-model", APIKey: "alice-secret",
		BaseURL: "https://llm.internal.example/v1/chat/completions",
		Proxy:   "http://proxy.internal.example:8080", InsecureSkipVerify: true,
		ExtraHeaders: map[string]string{"X-Tenant": "alice"},
	}
	aliceProvider, err := p.CreatePersonalProvider(aliceCtx, service.ProviderRecord{Key: "openai", Config: custom})
	if err != nil {
		t.Fatalf("create Alice provider: %v", err)
	}
	if aliceProvider.OwnerUserID != alice.ID || aliceProvider.WorkspaceID != "" || aliceProvider.Scope != service.ProviderScopePersonal {
		t.Fatalf("Alice provider ownership: %+v", aliceProvider)
	}
	if aliceProvider.Reference != service.PersonalProviderReference(aliceProvider.ID) {
		t.Fatalf("stable reference = %q", aliceProvider.Reference)
	}
	if aliceProvider.Config.BaseURL != custom.BaseURL || aliceProvider.Config.Proxy != custom.Proxy || !aliceProvider.Config.InsecureSkipVerify {
		t.Fatalf("personal transport options were not preserved: %+v", aliceProvider.Config)
	}
	liveAlice, err := p.businessPrincipal(aliceCtx)
	if err != nil || !liveAlice.Allows("models.use", service.AccessResource{Kind: "models", WorkspaceID: workspace.ID, ID: aliceProvider.ID, Path: aliceProvider.Reference + "/alice-model"}) {
		t.Fatalf("Alice model-use principal: %+v err=%v", liveAlice, err)
	}

	// Identical display keys are independent per owner.
	bobProvider, err := p.CreatePersonalProvider(bobCtx, service.ProviderRecord{Key: "openai", Config: config.LLMConfig{Type: "openai", Model: "bob-model", APIKey: "bob-secret"}})
	if err != nil {
		t.Fatalf("create Bob provider: %v", err)
	}
	if bobProvider.ID == aliceProvider.ID {
		t.Fatal("personal provider IDs collided")
	}
	if got, err := p.GetPersonalProvider(bobCtx, aliceProvider.ID); err != nil || got != nil {
		t.Fatalf("Bob read Alice provider: %+v %v", got, err)
	}
	if list, err := p.ListPersonalProviders(bobCtx, nil); err != nil || len(list.Data) != 1 || list.Data[0].ID != bobProvider.ID {
		t.Fatalf("Bob personal list: %+v %v", list, err)
	}

	// A stable personal reference never falls back to Bob's same-named row.
	resolved, err := p.ResolveWorkspaceProviderForUse(aliceCtx, aliceProvider.Reference, "alice-model")
	if err != nil || resolved.ID != aliceProvider.ID || resolved.Config.APIKey != "alice-secret" {
		t.Fatalf("owner resolution: %+v %v", resolved, err)
	}
	if _, err := p.ResolveWorkspaceProviderForUse(bobCtx, aliceProvider.Reference, "alice-model"); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("private provider resolved for Bob: %v", err)
	}
	second, err := p.CreateWorkspace(service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: platformAdmin.ID}), "Beta", platformAdmin.ID)
	if err != nil {
		t.Fatalf("create second workspace: %v", err)
	}
	if _, err = p.goqu.Update(p.workspaceTable("workspaces")).Set(goqu.Record{"execution_enabled": true}).Where(goqu.Ex{"id": second.ID}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	secondAdmin, _, err := p.ResolveWorkspaceAccess(service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: platformAdmin.ID}), second.ID, platformAdmin.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	secondAdminCtx := service.WithAccessPrincipal(t.Context(), secondAdmin)
	aliceSecond := workspaceMember(t, p, secondAdminCtx, second.ID, alice, "member")
	bobSecond := workspaceMember(t, p, secondAdminCtx, second.ID, bob, "member")
	if resolved, err = p.ResolveWorkspaceProviderForUse(aliceSecond, aliceProvider.Reference, "alice-model"); err != nil || resolved.ID != aliceProvider.ID {
		t.Fatalf("owner resolution after workspace switch: %+v %v", resolved, err)
	}

	// Publishing needs workspace provider authority. Bob is only a member.
	if _, err := p.SetPersonalProviderScope(bobCtx, bobProvider.ID, service.ProviderScopeWorkspace, bob.Username); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("member workspace publication: %v", err)
	}
	published, err := p.SetPersonalProviderScope(aliceCtx, aliceProvider.ID, service.ProviderScopeWorkspace, alice.Username)
	if err != nil || published.Scope != service.ProviderScopeWorkspace {
		t.Fatalf("workspace publication: %+v %v", published, err)
	}
	resolved, err = p.ResolveWorkspaceProviderForUse(bobCtx, aliceProvider.Reference, "alice-model")
	if err != nil || resolved.ID != aliceProvider.ID {
		t.Fatalf("published provider resolution: %+v %v", resolved, err)
	}
	if _, err = p.ResolveWorkspaceProviderForUse(bobSecond, aliceProvider.Reference, "alice-model"); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("workspace publication leaked into another workspace: %v", err)
	}
	if _, err = p.SetPersonalProviderScope(aliceCtx, aliceProvider.ID, service.ProviderScopePersonal, alice.Username); err != nil {
		t.Fatalf("revoke publication: %v", err)
	}
	if _, err = p.ResolveWorkspaceProviderForUse(bobCtx, aliceProvider.Reference, "alice-model"); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("revoked provider remained available: %v", err)
	}

	// Global publication additionally requires installation administration and
	// still cannot be performed for somebody else's credential.
	if _, err = p.SetPersonalProviderScope(aliceCtx, aliceProvider.ID, service.ProviderScopeGlobal, alice.Username); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("non-platform global publication: %v", err)
	}
	if _, err = p.SetPersonalProviderScope(adminCtx, aliceProvider.ID, service.ProviderScopeGlobal, platformAdmin.Username); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("platform administrator changed another owner's provider: %v", err)
	}

	global, err := p.CreatePersonalProvider(adminCtx, service.ProviderRecord{
		Key: "admin-global", Scope: service.ProviderScopeGlobal,
		Config: config.LLMConfig{Type: "anthropic", AuthType: "claude-code", Model: "global-model", APIKey: "access-old", RefreshToken: "refresh-old"},
	})
	if err != nil || global.Scope != service.ProviderScopeGlobal {
		t.Fatalf("platform global personal provider: %+v %v", global, err)
	}
	if resolved, err = p.ResolveWorkspaceProviderForUse(bobSecond, global.Reference, "global-model"); err != nil || resolved.ID != global.ID {
		t.Fatalf("global personal provider resolution: %+v %v", resolved, err)
	}

	interactive, err := service.BindExecution(aliceSecond, service.ExecutionProvenance{
		RunID: "interactive-run", UserID: alice.ID, WorkspaceID: second.ID, Source: "session",
	}, t.TempDir(), func(context.Context, service.ExecutionProvenance, service.ExecutionAction) (service.ExecutionValidation, error) {
		return service.ExecutionValidation{Allowed: true, Policy: service.ExecutionPolicy{WorkspaceID: second.ID, Mode: service.ExecutionRestricted}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err = p.ResolveWorkspaceProviderForUse(interactive, aliceProvider.Reference, "alice-model"); err != nil || resolved.ID != aliceProvider.ID {
		t.Fatalf("interactive agent personal resolution: %+v %v", resolved, err)
	}
	machine, err := service.BindExecution(aliceSecond, service.ExecutionProvenance{
		RunID: "machine-run", UserID: alice.ID, WorkspaceID: second.ID, Source: "bot", ServiceID: "bot-1", ServiceVersion: 1,
	}, t.TempDir(), func(context.Context, service.ExecutionProvenance, service.ExecutionAction) (service.ExecutionValidation, error) {
		return service.ExecutionValidation{Allowed: true, Policy: service.ExecutionPolicy{WorkspaceID: second.ID, Mode: service.ExecutionRestricted}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.ResolveWorkspaceProviderForUse(machine, global.Reference, "global-model"); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("machine resolved a personal provider: %v", err)
	}
	catalog, err := p.ListWorkspaceProviderCatalog(machine)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range catalog {
		if entry.Reference != "" {
			t.Fatalf("machine catalog exposed personal provider: %+v", entry)
		}
	}

	rotated, err := p.WithClaudeOAuthTokens(adminCtx, "", global.Reference, func(tokens *service.ClaudeOAuthTokens) error {
		if tokens.RefreshToken != "refresh-old" {
			t.Fatalf("loaded refresh token = %q", tokens.RefreshToken)
		}
		tokens.AccessToken = "access-new"
		tokens.RefreshToken = "refresh-new"
		tokens.ExpiresAt = time.Now().UTC().Add(time.Hour)
		return nil
	})
	if err != nil || rotated.RefreshToken != "refresh-new" {
		t.Fatalf("personal OAuth rotation: %+v %v", rotated, err)
	}
	storedGlobal, err := p.GetPersonalProvider(adminCtx, global.ID)
	if err != nil || storedGlobal.Config.RefreshToken != "refresh-new" {
		t.Fatalf("personal OAuth persistence: %+v %v", storedGlobal, err)
	}
	if _, err = p.WithClaudeOAuthTokens(machine, "", global.Reference, func(*service.ClaudeOAuthTokens) error { return nil }); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("machine refreshed personal OAuth credential: %v", err)
	}
}

func TestPersonalProviderSurvivesWorkspaceDeletionAndDiesWithOwner(t *testing.T) {
	p, adminCtx, workspace, _ := workspaceFixture(t)
	alice := workspaceUser(t, p, "personal-provider-lifecycle")
	aliceCtx := workspaceMember(t, p, adminCtx, workspace.ID, alice, "admin")
	record, err := p.CreatePersonalProvider(aliceCtx, service.ProviderRecord{Key: "mine", Config: config.LLMConfig{Type: "openai", Model: "m", APIKey: "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.SetPersonalProviderScope(aliceCtx, record.ID, service.ProviderScopeWorkspace, alice.Username); err != nil {
		t.Fatal(err)
	}

	if _, err = p.DeleteWorkspace(adminCtx, workspace.Name); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}
	var count uint64
	if _, err = p.goqu.From(p.tableProviders).Select(goqu.COUNT("*")).Where(goqu.Ex{"id": record.ID}).ScanValContext(t.Context(), &count); err != nil || count != 1 {
		t.Fatalf("personal provider did not survive workspace deletion: count=%d err=%v", count, err)
	}
	if deleted, err := p.DeleteAuthUser(adminCtx, alice.ID); err != nil || !deleted {
		t.Fatalf("delete owner: deleted=%v err=%v", deleted, err)
	}
	if _, err = p.goqu.From(p.tableProviders).Select(goqu.COUNT("*")).Where(goqu.Ex{"id": record.ID}).ScanValContext(t.Context(), &count); err != nil || count != 0 {
		t.Fatalf("owner deletion left personal provider: count=%d err=%v", count, err)
	}
}
