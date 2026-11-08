package postgres

import (
	"errors"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestGuardrailPoliciesPostgres(t *testing.T) {
	p, ctx, ws, _ := workspaceFixture(t)
	wsAdmin := workspaceMember(t, p, ctx, ws.ID, workspaceUser(t, p, "guard-admin"), "admin")
	member := workspaceMember(t, p, ctx, ws.ID, workspaceUser(t, p, "guard-member"), "member")

	if _, err := p.SaveGuardrailPolicy(member, service.GuardrailPolicy{Name: "x", Enabled: true}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("member created a policy: %v", err)
	}
	if _, err := p.SaveGuardrailPolicy(wsAdmin, service.GuardrailPolicy{Name: "x", Scope: "installation", Enabled: true}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("workspace admin created an installation policy: %v", err)
	}
	if _, err := p.SaveGuardrailPolicy(wsAdmin, service.GuardrailPolicy{Name: "bad", Enabled: true, Config: service.GuardrailConfig{Input: service.GuardrailInputFilter{Deny: []service.GuardrailPattern{{Pattern: "("}}}}}); !errors.Is(err, service.ErrGuardrailInvalid) {
		t.Fatalf("invalid pattern saved: %v", err)
	}

	wsPolicy, err := p.SaveGuardrailPolicy(wsAdmin, service.GuardrailPolicy{
		Name: "site", Enabled: true, Mode: "enforce", TokenID: "tok",
		Config: service.GuardrailConfig{
			Injection: service.GuardrailInjection{Enabled: true},
			Masking:   service.GuardrailMasking{Entities: []string{"email", "tr_id"}, Restore: true},
		},
	})
	if err != nil || wsPolicy.WorkspaceID != ws.ID || wsPolicy.Scope != "workspace" || wsPolicy.CreatedBy == "" {
		t.Fatalf("workspace policy: %+v %v", wsPolicy, err)
	}
	if !wsPolicy.Config.Masking.Restore || len(wsPolicy.Config.Masking.Entities) != 2 || wsPolicy.Config.Injection.ToolResults != "withhold" {
		t.Fatalf("config round trip: %+v", wsPolicy.Config)
	}
	inst, err := p.SaveGuardrailPolicy(ctx, service.GuardrailPolicy{Name: "global", Scope: "installation", Enabled: false, Mode: "monitor"})
	if err != nil || inst.WorkspaceID != "" || inst.Scope != "installation" {
		t.Fatalf("installation policy: %+v %v", inst, err)
	}

	if list, err := p.ListGuardrailPolicies(wsAdmin); err != nil || len(list) != 1 {
		t.Fatalf("workspace admin list: %d %v", len(list), err)
	}
	if list, err := p.ListGuardrailPolicies(ctx); err != nil || len(list) != 2 {
		t.Fatalf("platform admin list: %d %v", len(list), err)
	}
	if _, err := p.ListGuardrailPolicies(member); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("member listed policies: %v", err)
	}
	if err := p.DeleteGuardrailPolicy(wsAdmin, inst.ID); !errors.Is(err, service.ErrGuardrailPolicyNotFound) {
		t.Fatalf("workspace admin deleted installation policy: %v", err)
	}

	// Only enabled policies are loaded for enforcement.
	loaded, err := p.LoadGuardrailPolicies(t.Context())
	if err != nil || len(loaded) != 1 || loaded[0].ID != wsPolicy.ID {
		t.Fatalf("load: %+v %v", loaded, err)
	}

	// Updates keep the scope and the creator.
	wsPolicy.Name = "site v2"
	wsPolicy.Scope = "installation"
	updated, err := p.SaveGuardrailPolicy(wsAdmin, *wsPolicy)
	if err != nil || updated.Name != "site v2" || updated.WorkspaceID != ws.ID || updated.CreatedBy != wsPolicy.CreatedBy {
		t.Fatalf("update: %+v %v", updated, err)
	}
	if err := p.DeleteGuardrailPolicy(wsAdmin, wsPolicy.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := p.ListGuardrailPolicies(wsAdmin); len(list) != 0 {
		t.Fatalf("not deleted: %d", len(list))
	}
}
