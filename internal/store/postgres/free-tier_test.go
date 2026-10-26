package postgres

import (
	"errors"
	"testing"

	"github.com/doug-martin/goqu/v9"
	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/types"
)

func TestPersonalRoutingProfileIsolation(t *testing.T) {
	p, admin, w, _ := workspaceFixture(t)
	a := workspaceUser(t, p, "profile-alice")
	b := workspaceUser(t, p, "profile-bob")
	ac := workspaceMember(t, p, admin, w.ID, a, "member")
	bc := workspaceMember(t, p, admin, w.ID, b, "member")
	created, err := p.CreateRoutingProfile(ac, service.RoutingProfile{Name: "my-free", Scope: "personal", OwnerUserID: b.ID, Targets: types.Slice[string]{"provider:a/model"}})
	if err != nil {
		t.Fatal(err)
	}
	if created.OwnerUserID != a.ID {
		t.Fatal("owner was not stamped from account")
	}
	if got, err := p.GetRoutingProfile(bc, created.ID); err != nil || got != nil {
		t.Fatalf("foreign profile: %+v %v", got, err)
	}
	if got, err := p.ListRoutingProfiles(bc, nil); err != nil || len(got.Data) != 0 {
		t.Fatalf("foreign list: %+v %v", got, err)
	}
	for _, owner := range []string{"", b.ID} {
		if got, err := p.GetGatewayRoutingProfile(t.Context(), created.Name, w.ID, owner); err != nil || got != nil {
			t.Fatalf("gateway foreign owner %q: %+v %v", owner, got, err)
		}
	}
	if got, err := p.GetGatewayRoutingProfile(t.Context(), created.Name, w.ID, a.ID); err != nil || got == nil {
		t.Fatalf("gateway own: %+v %v", got, err)
	}
	if _, err := p.UpdateRoutingProfile(bc, created.ID, *created); err != nil && !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatal(err)
	}
	got, err := p.GetRoutingProfile(ac, created.ID)
	if err != nil || got == nil || got.OwnerUserID != a.ID {
		t.Fatal("foreign write affected owner")
	}
	if _, err := p.CreateRoutingProfile(ac, service.RoutingProfile{Name: "workspace-escalation", Targets: created.Targets}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("member created workspace profile: %v", err)
	}
}

func TestProviderUsePolicyLiveAdmission(t *testing.T) {
	p, admin, w, _ := workspaceFixture(t)
	if _, err := p.goqu.Update(p.workspaceTable("workspaces")).Set(goqu.Record{"execution_enabled": true}).Where(goqu.C("id").Eq(w.ID)).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	a := workspaceUser(t, p, "free-tier-alice")
	ac := workspaceMember(t, p, admin, w.ID, a, "member")
	cfg := config.LLMConfig{Type: "openai", Model: "m", BaseURL: "https://api.groq.com/openai/v1/chat/completions"}
	provider, err := p.CreatePersonalProvider(ac, service.ProviderRecord{Key: "groq", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := p.SaveProviderUsePolicy(admin, service.ProviderUsePolicy{BlockFreeTier: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.SaveProviderUsePolicy(admin, service.ProviderUsePolicy{Version: 0}); !errors.Is(err, service.ErrWorkspaceConflict) {
		t.Fatalf("stale save: %v", err)
	}
	if _, err := p.ResolveWorkspaceProviderForUse(ac, provider.Reference, "m"); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("existing provider not blocked: %v", err)
	}
	if _, err := p.CreatePersonalProvider(ac, service.ProviderRecord{Key: "bypass", Config: cfg}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("unlabelled host bypass: %v", err)
	}
	catalog, err := p.ListWorkspaceProviderCatalog(ac)
	if err != nil || len(catalog) != 0 {
		t.Fatalf("blocked catalog: %+v %v", catalog, err)
	}
	policy.BlockFreeTier = false
	policy.AllowedPersonalHosts = []string{}
	policy, err = p.SaveProviderUsePolicy(admin, *policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ResolveWorkspaceProviderForUse(ac, provider.Reference, "m"); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("empty allowlist admits: %v", err)
	}
	policy.AllowedPersonalHosts = []string{"api.groq.com"}
	_, err = p.SaveProviderUsePolicy(admin, *policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ResolveWorkspaceProviderForUse(ac, provider.Reference, "m"); err != nil {
		t.Fatalf("approved host: %v", err)
	}
	if _, err := p.SaveProviderUsePolicy(ac, service.ProviderUsePolicy{}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("member changed policy: %v", err)
	}
}
