package postgres

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

func TestProviderQuotaAtomicReservations(t *testing.T) {
	p, ctx, _, _ := workspaceFixture(t)
	provider, err := p.CreateProvider(ctx, service.ProviderRecord{Key: "quota", Config: config.LLMConfig{
		Type: "openai", Model: "m", Quota: &config.ProviderQuotaConfig{QuotaLimits: config.QuotaLimits{RequestsPerDay: 3, TokensPerDay: 100, TokensPerMonth: 150}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := p.ReserveProviderQuota(ctx, provider.ID, "m", 30)
			if errors.Is(err, service.ErrProviderQuotaExceeded) {
				return
			}
			if err != nil {
				t.Error(err)
				return
			}
			admitted.Add(1)
			if err := p.SettleProviderQuota(ctx, r.ID, 20, true); err != nil {
				t.Error(err)
			}
			// Replaying a receipt cannot double-charge or refund another call.
			if err := p.SettleProviderQuota(ctx, r.ID, 20, true); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := admitted.Load(); got != 3 {
		t.Fatalf("admitted %d, want 3", got)
	}
	usage, err := p.ProviderQuotaUsage(ctx, provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 2 || usage[0].Requests != 3 || usage[0].Tokens != 60 || usage[0].Reserved != 0 {
		t.Fatalf("usage: %+v", usage)
	}
	var quotaErr *service.ProviderQuotaError
	_, err = p.ReserveProviderQuota(ctx, provider.ID, "m", 1)
	if !errors.As(err, &quotaErr) || quotaErr.ResetAt.Hour() != 0 {
		t.Fatalf("expected reset-aware error: %v", err)
	}
}

func TestProviderQuotaPoolsRolloverAndMissingUsage(t *testing.T) {
	p, ctx, _, _ := workspaceFixture(t)
	provider, err := p.CreateProvider(ctx, service.ProviderRecord{Key: "pool", Config: config.LLMConfig{
		Type: "openai", Model: "a", Quota: &config.ProviderQuotaConfig{
			QuotaLimits: config.QuotaLimits{TokensPerDay: 100},
			Models:      map[string]config.QuotaLimits{"a": {TokensPerDay: 40}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := p.ReserveProviderQuota(ctx, provider.ID, "a", 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ReserveProviderQuota(ctx, provider.ID, "a", 20); !errors.Is(err, service.ErrProviderQuotaExceeded) {
		t.Fatalf("model cap: %v", err)
	}
	b, err := p.ReserveProviderQuota(ctx, provider.ID, "b", 60)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ReserveProviderQuota(ctx, provider.ID, "b", 20); !errors.Is(err, service.ErrProviderQuotaExceeded) {
		t.Fatalf("shared cap: %v", err)
	}
	if err := p.SettleProviderQuota(ctx, b.ID, 0, false); err != nil {
		t.Fatal(err)
	}
	if err := p.SettleProviderQuota(ctx, a.ID, 0, true); err != nil {
		t.Fatal(err)
	}
	u, err := p.ProviderQuotaUsage(ctx, provider.ID)
	if err != nil || u[0].Requests != 1 || u[0].Tokens != 30 {
		t.Fatalf("unknown usage retains estimate, cancelled admission refunds: %+v %v", u, err)
	}
	// A late receipt from yesterday cannot write into today's reset counters.
	old, err := p.ReserveProviderQuota(ctx, provider.ID, "b", 20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.goqu.Update(p.workspaceTable("provider_quota_usage")).Set(goqu.Record{"window_start": goqu.L("window_start - interval '1 day'")}).Where(goqu.C("provider_id").Eq(provider.ID)).Executor().ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	current, err := p.ReserveProviderQuota(ctx, provider.ID, "b", 10)
	if err != nil {
		t.Fatal(err)
	}
	// The old receipt has today's timestamp, emulating a completion after reset
	// via a receipt containing an older window instead.
	if _, err := p.goqu.Update(p.workspaceTable("provider_quota_reservations")).Set(goqu.Record{"pools": goqu.L("jsonb_build_array(jsonb_build_object('model','','period','day','start',date_trunc('day',clock_timestamp()) - interval '1 day'))")}).Where(goqu.C("id").Eq(old.ID)).Executor().ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.SettleProviderQuota(ctx, old.ID, 50, true); err != nil {
		t.Fatal(err)
	}
	if err := p.SettleProviderQuota(ctx, current.ID, 8, true); err != nil {
		t.Fatal(err)
	}
	u, err = p.ProviderQuotaUsage(ctx, provider.ID)
	if err != nil || u[0].Tokens != 8 || u[0].Requests != 1 || u[0].Reserved != 0 {
		t.Fatalf("rollover: %+v %v", u, err)
	}
	// Window math is UTC even if the process/caller lives elsewhere.
	pools := quotaPools(&config.ProviderQuotaConfig{QuotaLimits: config.QuotaLimits{RequestsPerDay: 1, RequestsPerMonth: 1}}, "m", time.Date(2026, 1, 31, 22, 0, 0, 0, time.FixedZone("offset", -3*3600)))
	if pools[0].Start.Day() != 1 || pools[1].Start.Month() != time.February {
		t.Fatalf("UTC pools: %+v", pools)
	}
}

func TestProviderQuotaUsageIsOwnerScoped(t *testing.T) {
	p, admin, w, _ := workspaceFixture(t)
	a := workspaceUser(t, p, "quota-alice")
	b := workspaceUser(t, p, "quota-bob")
	ac := workspaceMember(t, p, admin, w.ID, a, "member")
	bc := workspaceMember(t, p, admin, w.ID, b, "member")
	provider, err := p.CreatePersonalProvider(ac, service.ProviderRecord{Key: "personal", Config: config.LLMConfig{Type: "openai", Model: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ProviderQuotaUsage(bc, provider.ID); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("foreign usage: %v", err)
	}
}

func TestProviderQuotaRejectsUnresolvedPassthroughModel(t *testing.T) {
	p, ctx, _, _ := workspaceFixture(t)
	provider, err := p.CreateProvider(ctx, service.ProviderRecord{Key: "opaque", Config: config.LLMConfig{Type: "openai", Model: "m", Quota: &config.ProviderQuotaConfig{Models: map[string]config.QuotaLimits{"m": {RequestsPerDay: 1}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ReserveProviderQuota(ctx, provider.ID, "", 0); !errors.Is(err, service.ErrUnsupportedOperation) {
		t.Fatalf("opaque path bypassed model quotas: %v", err)
	}
}
