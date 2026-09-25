package postgres

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

func budgetEstimate(v float64) *float64 { return &v }

func TestProviderBudgetReservationLimitsAndOverrides(t *testing.T) {
	p, ctx, _, _ := workspaceFixture(t)
	provider, err := p.CreateProvider(ctx, service.ProviderRecord{Key: "budgeted", Config: config.LLMConfig{Type: "openai", APIKey: "secret", Model: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := p.SaveProviderBudgetPolicy(ctx, service.ProviderBudgetPolicy{
		ResourceKind: service.BudgetResourceProvider, ResourceID: provider.ID,
		TotalLimitCents: 1000, DefaultUserLimitCents: 100, EnforceUnpriced: true,
	})
	if err != nil || policy == nil {
		t.Fatalf("save policy: %+v %v", policy, err)
	}
	resources := []service.BudgetResource{{Kind: service.BudgetResourceProvider, ID: provider.ID}}
	alice := workspaceUser(t, p, "budget-alice")

	first, err := p.ReserveProviderBudget(t.Context(), resources, alice.ID, budgetEstimate(60))
	if err != nil || first == nil {
		t.Fatalf("first reservation: %+v %v", first, err)
	}
	if _, err = p.ReserveProviderBudget(t.Context(), resources, alice.ID, budgetEstimate(60)); !errors.Is(err, service.ErrProviderUserLimit) {
		t.Fatalf("default user limit not enforced: %v", err)
	}
	if err = p.SettleProviderBudget(t.Context(), first.ID, 50); err != nil {
		t.Fatal(err)
	}
	status, err := p.GetProviderBudgetStatus(ctx, service.BudgetResourceProvider, provider.ID, alice.ID)
	if err != nil || status.UserSpentCents != 50 || status.UserReservedCents != 0 || status.SpentCents != 50 || status.ReservedCents != 0 || status.EffectiveUserLimit != 100 {
		t.Fatalf("settled status: %+v %v", status, err)
	}
	second, err := p.ReserveProviderBudget(t.Context(), resources, alice.ID, budgetEstimate(40))
	if err != nil {
		t.Fatalf("reservation within remaining allowance: %v", err)
	}
	if err = p.SettleProviderBudget(t.Context(), second.ID, 40); err != nil {
		t.Fatal(err)
	}

	// Pricing is required while the policy enforces it.
	if _, err = p.ReserveProviderBudget(t.Context(), resources, alice.ID, nil); !errors.Is(err, service.ErrProviderPricingRequired) {
		t.Fatalf("unpriced call admitted: %v", err)
	}

	// Custom override raises the allowance.
	if err = p.SaveProviderBudgetOverride(ctx, service.ProviderBudgetOverride{PolicyID: policy.ID, UserID: alice.ID, Mode: service.BudgetOverrideCustom, LimitCents: 500}); err != nil {
		t.Fatal(err)
	}
	custom, err := p.ReserveProviderBudget(t.Context(), resources, alice.ID, budgetEstimate(300))
	if err != nil {
		t.Fatalf("custom override: %v", err)
	}
	_ = p.SettleProviderBudget(t.Context(), custom.ID, 300)

	// Blocked refuses regardless of remaining allowance.
	if err = p.SaveProviderBudgetOverride(ctx, service.ProviderBudgetOverride{PolicyID: policy.ID, UserID: alice.ID, Mode: service.BudgetOverrideBlocked}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ReserveProviderBudget(t.Context(), resources, alice.ID, budgetEstimate(1)); !errors.Is(err, service.ErrProviderUserBlocked) {
		t.Fatalf("blocked override: %v", err)
	}

	// Unlimited exempts the user allowance but not the provider total (390 spent of 1000).
	if err = p.SaveProviderBudgetOverride(ctx, service.ProviderBudgetOverride{PolicyID: policy.ID, UserID: alice.ID, Mode: service.BudgetOverrideUnlimited}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ReserveProviderBudget(t.Context(), resources, alice.ID, budgetEstimate(700)); !errors.Is(err, service.ErrProviderBudgetExceeded) {
		t.Fatalf("unlimited user exceeded provider total: %v", err)
	}
	unlimited, err := p.ReserveProviderBudget(t.Context(), resources, alice.ID, budgetEstimate(500))
	if err != nil {
		t.Fatalf("unlimited override: %v", err)
	}

	// Abandoned reservations are released by the next reservation.
	if _, err = p.goqu.Update(p.tableProviderBudgetReservations).Set(goqu.Record{"created_at": goqu.L("NOW() - INTERVAL '2 hours'")}).Where(goqu.Ex{"reservation_id": unlimited.ID}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ReserveProviderBudget(t.Context(), resources, "", budgetEstimate(1)); err != nil {
		t.Fatal(err)
	}
	status, err = p.GetProviderBudgetStatus(ctx, service.BudgetResourceProvider, provider.ID, alice.ID)
	if err != nil || status.UserReservedCents != 0 || status.ReservedCents != 1 || status.EffectiveUserLimit != 0 {
		t.Fatalf("stale reservation not released: %+v %v", status, err)
	}

	// A policy without a total limit and no user ID still admits unpriced calls when not enforced.
	if _, err = p.SaveProviderBudgetPolicy(ctx, service.ProviderBudgetPolicy{ResourceKind: service.BudgetResourceProvider, ResourceID: provider.ID, TotalLimitCents: 1000}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ReserveProviderBudget(t.Context(), resources, "", nil); err != nil {
		t.Fatalf("unenforced unpriced call: %v", err)
	}
}

func TestProviderBudgetConcurrentReservationsNeverOvershoot(t *testing.T) {
	p, ctx, _, _ := workspaceFixture(t)
	provider, err := p.CreateProvider(ctx, service.ProviderRecord{Key: "race", Config: config.LLMConfig{Type: "openai", APIKey: "secret", Model: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.SaveProviderBudgetPolicy(ctx, service.ProviderBudgetPolicy{ResourceKind: service.BudgetResourceProvider, ResourceID: provider.ID, TotalLimitCents: 100}); err != nil {
		t.Fatal(err)
	}
	resources := []service.BudgetResource{{Kind: service.BudgetResourceProvider, ID: provider.ID}}
	var admitted, refused atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, reserveErr := p.ReserveProviderBudget(t.Context(), resources, "", budgetEstimate(10))
			switch {
			case reserveErr == nil:
				admitted.Add(1)
			case errors.Is(reserveErr, service.ErrProviderBudgetExceeded):
				refused.Add(1)
			default:
				t.Errorf("reservation: %v", reserveErr)
			}
		}()
	}
	wg.Wait()
	if admitted.Load() != 10 || refused.Load() != 10 {
		t.Fatalf("admitted=%d refused=%d, want 10/10", admitted.Load(), refused.Load())
	}
}

func TestVirtualProviderValidationGrantsAndRoutes(t *testing.T) {
	p, _, owner, platformAdmin := workspaceFixture(t)
	if _, err := p.goqu.Update(p.workspaceTable("workspaces")).Set(goqu.Record{"execution_enabled": true}).Where(goqu.Ex{"id": owner.ID}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	resolved, _, err := p.ResolveWorkspaceAccess(service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: platformAdmin.ID}), owner.ID, platformAdmin.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := service.WithAccessPrincipal(t.Context(), resolved)
	if _, err := p.CreateProvider(ctx, service.ProviderRecord{Key: "openai", Config: config.LLMConfig{Type: "openai", APIKey: "owner-secret", Model: "gpt-small", Models: []string{"gpt-small", "gpt-large"}}}); err != nil {
		t.Fatal(err)
	}
	models := []service.VirtualProviderModel{
		{Alias: "fast", ProviderRef: "openai", Model: "gpt-small"},
		{Alias: "smart", ProviderRef: "openai", Model: "gpt-large"},
	}
	if _, err := p.CreateVirtualProvider(ctx, service.VirtualProvider{Key: "openai", Name: "Collides", DefaultModel: "fast", Models: models}); err == nil {
		t.Fatal("virtual key collided with a physical provider")
	}
	if _, err := p.CreateVirtualProvider(ctx, service.VirtualProvider{Key: "bad", Name: "Bad", DefaultModel: "x", Models: []service.VirtualProviderModel{{Alias: "x", ProviderRef: "missing", Model: "m"}}}); err == nil {
		t.Fatal("virtual provider referenced a provider outside the workspace")
	}
	virtual, err := p.CreateVirtualProvider(ctx, service.VirtualProvider{Key: "team", Name: "Team", DefaultModel: "fast", Models: models})
	if err != nil {
		t.Fatalf("create virtual provider: %v", err)
	}
	if _, err = p.CreateVirtualProvider(ctx, service.VirtualProvider{Key: "team", Name: "Dup", DefaultModel: "fast", Models: models}); err == nil {
		t.Fatal("duplicate virtual key accepted")
	}

	route, err := p.ResolveWorkspaceProviderRoute(ctx, "team", "smart")
	if err != nil || route.ActualModel != "gpt-large" || route.VirtualProviderID != virtual.ID || route.Record.Key != "openai" {
		t.Fatalf("owner route: %+v %v", route, err)
	}

	adminCtx := service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: platformAdmin.ID})
	recipient, err := p.CreateWorkspace(adminCtx, "Recipient", platformAdmin.ID)
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := p.CreateWorkspace(adminCtx, "Outsider", platformAdmin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.SaveVirtualProviderGrant(ctx, service.VirtualProviderGrant{VirtualProviderID: virtual.ID, WorkspaceID: recipient.ID, ModelPatterns: []string{"fast"}}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	granted, err := p.ResolveGatewayProviderRoute(t.Context(), recipient.ID, "", "team", "fast")
	if err != nil || granted.ActualModel != "gpt-small" || granted.Record.WorkspaceID != owner.ID {
		t.Fatalf("granted route: %+v %v", granted, err)
	}
	if _, err = p.ResolveGatewayProviderRoute(t.Context(), recipient.ID, "", "team", "smart"); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("pattern filter not applied: %v", err)
	}
	if _, err = p.ResolveGatewayProviderRoute(t.Context(), outsider.ID, "", "team", "fast"); !errors.Is(err, errVirtualProviderNotFound) {
		t.Fatalf("ungranted workspace resolved: %v", err)
	}

	catalog, err := p.ListGatewayVirtualProviderCatalog(t.Context(), recipient.ID, "")
	if err != nil || len(catalog) != 1 || catalog[0].Key != "team" || !catalog[0].Shared || len(catalog[0].Models) != 1 || catalog[0].Models[0] != "fast" || catalog[0].DefaultModel != "fast" {
		t.Fatalf("recipient catalog: %+v %v", catalog, err)
	}
	if catalog, err = p.ListGatewayVirtualProviderCatalog(t.Context(), outsider.ID, ""); err != nil || len(catalog) != 0 {
		t.Fatalf("outsider catalog: %+v %v", catalog, err)
	}

	// Recipient-managed overrides require the grant's delegation, are limited
	// to the recipient's own members and are capped by the grant ceiling.
	policy, err := p.SaveProviderBudgetPolicy(ctx, service.ProviderBudgetPolicy{ResourceKind: service.BudgetResourceVirtualProvider, ResourceID: virtual.ID, DefaultUserLimitCents: 100})
	if err != nil {
		t.Fatal(err)
	}
	recipientAccess, _, err := p.ResolveWorkspaceAccess(adminCtx, recipient.ID, platformAdmin.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	recipientAdminCtx := service.WithAccessPrincipal(t.Context(), recipientAccess)
	carol := workspaceUser(t, p, "virtual-carol")
	carolCtx := workspaceMember(t, p, recipientAdminCtx, recipient.ID, carol, "admin")
	dave := workspaceUser(t, p, "virtual-dave")
	workspaceMember(t, p, recipientAdminCtx, recipient.ID, dave, "member")
	stranger := workspaceUser(t, p, "virtual-stranger")
	override := service.ProviderBudgetOverride{PolicyID: policy.ID, UserID: dave.ID, Mode: service.BudgetOverrideCustom, LimitCents: 300}
	if err = p.SaveProviderBudgetOverride(carolCtx, override); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("override without delegation: %v", err)
	}
	if _, err = p.SaveVirtualProviderGrant(ctx, service.VirtualProviderGrant{VirtualProviderID: virtual.ID, WorkspaceID: recipient.ID, ModelPatterns: []string{"fast"}, AllowUserOverrides: true, MaxUserLimitCents: 500}); err != nil {
		t.Fatal(err)
	}
	if got, err := p.GetProviderBudgetPolicy(carolCtx, service.BudgetResourceVirtualProvider, virtual.ID); err != nil || got == nil || got.ID != policy.ID {
		t.Fatalf("recipient policy read: %+v %v", got, err)
	}
	if _, err = p.SaveProviderBudgetPolicy(carolCtx, service.ProviderBudgetPolicy{ResourceKind: service.BudgetResourceVirtualProvider, ResourceID: virtual.ID, DefaultUserLimitCents: 9999}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("recipient rewrote the owner policy: %v", err)
	}
	if err = p.SaveProviderBudgetOverride(carolCtx, override); err != nil {
		t.Fatalf("delegated override: %v", err)
	}
	if err = p.SaveProviderBudgetOverride(carolCtx, service.ProviderBudgetOverride{PolicyID: policy.ID, UserID: dave.ID, Mode: service.BudgetOverrideCustom, LimitCents: 501}); err == nil {
		t.Fatal("override above the grant ceiling accepted")
	}
	if err = p.SaveProviderBudgetOverride(carolCtx, service.ProviderBudgetOverride{PolicyID: policy.ID, UserID: dave.ID, Mode: service.BudgetOverrideUnlimited}); err == nil {
		t.Fatal("unlimited override accepted under a ceiling")
	}
	if err = p.SaveProviderBudgetOverride(carolCtx, service.ProviderBudgetOverride{PolicyID: policy.ID, UserID: stranger.ID, Mode: service.BudgetOverrideBlocked}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("override for a non-member: %v", err)
	}
	if err = p.SaveProviderBudgetOverride(ctx, service.ProviderBudgetOverride{PolicyID: policy.ID, UserID: stranger.ID, Mode: service.BudgetOverrideBlocked}); err != nil {
		t.Fatalf("owner override: %v", err)
	}
	if list, err := p.ListProviderBudgetOverrides(carolCtx, policy.ID); err != nil || len(list) != 1 || list[0].UserID != dave.ID {
		t.Fatalf("recipient override list leaked other workspaces: %+v %v", list, err)
	}
	if list, err := p.ListProviderBudgetOverrides(ctx, policy.ID); err != nil || len(list) != 2 {
		t.Fatalf("owner override list: %+v %v", list, err)
	}

	// Disabling the virtual provider hides it everywhere.
	virtual.Disabled = true
	if _, err = p.UpdateVirtualProvider(ctx, virtual.ID, *virtual); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ResolveGatewayProviderRoute(t.Context(), recipient.ID, "", "team", "fast"); !errors.Is(err, errVirtualProviderNotFound) {
		t.Fatalf("disabled virtual provider resolved: %v", err)
	}

	if err = p.DeleteVirtualProvider(ctx, virtual.ID); err != nil {
		t.Fatal(err)
	}
	var grants int
	if _, err = p.goqu.From(p.tableVirtualProviderGrants).Select(goqu.COUNT("*")).Where(goqu.Ex{"virtual_provider_id": virtual.ID}).ScanValContext(t.Context(), &grants); err != nil || grants != 0 {
		t.Fatalf("grants survived deletion: %d %v", grants, err)
	}
}
