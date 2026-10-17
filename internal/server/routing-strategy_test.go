package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func TestOrderRoutingTargetsRoundRobin(t *testing.T) {
	s := &Server{}
	profile := &service.RoutingProfile{ID: "p1", Strategy: service.RoutingStrategyRoundRobin}

	var firsts []string
	for range 4 {
		got := s.orderRoutingTargets(context.Background(), nil, profile, chainOf("a", "b", "c"))
		if len(got) != 3 {
			t.Fatalf("round robin dropped targets: %s", chainKeys(got))
		}
		firsts = append(firsts, chainKeys(got))
	}
	want := []string{"a,b,c", "b,c,a", "c,a,b", "a,b,c"}
	for i := range want {
		if firsts[i] != want[i] {
			t.Fatalf("rotation %d = %q want %q", i, firsts[i], want[i])
		}
	}

	other := &service.RoutingProfile{ID: "p2", Strategy: service.RoutingStrategyRoundRobin}
	if got := chainKeys(s.orderRoutingTargets(context.Background(), nil, other, chainOf("a", "b"))); got != "a,b" {
		t.Fatalf("profiles must rotate independently, got %q", got)
	}
}

func TestOrderWeighted(t *testing.T) {
	t.Run("zero weight is fallback only", func(t *testing.T) {
		for range 50 {
			got := orderWeighted(chainOf("a", "b", "c"), map[string]int{"a/m": 0}, randFloat())
			if keys := chainKeys(got); !strings.HasSuffix(keys, ",a") || len(got) != 3 {
				t.Fatalf("zero-weight target must be last and kept: %q", keys)
			}
		}
	})

	t.Run("distribution follows weights", func(t *testing.T) {
		counts := map[string]int{}
		random := randFloat()
		for range 4000 {
			got := orderWeighted(chainOf("a", "b"), map[string]int{"a/m": 3, "b/m": 1}, random)
			counts[got[0].providerKey]++
		}
		// Expected 3000 / 1000; generous bounds keep this deterministic enough.
		if counts["a"] < 2700 || counts["a"] > 3300 {
			t.Fatalf("weighted first picks %v, want ~75%% a", counts)
		}
	})
}

func TestOrderSticky(t *testing.T) {
	chain := chainOf("a", "b", "c", "d")

	first := chainKeys(orderSticky(chain, "session:s1"))
	for range 10 {
		if got := chainKeys(orderSticky(chain, "session:s1")); got != first {
			t.Fatalf("same affinity must give the same order: %q vs %q", got, first)
		}
	}

	differs := false
	for i := range 20 {
		if chainKeys(orderSticky(chain, fmt.Sprintf("session:%d", i))) != first {
			differs = true
			break
		}
	}
	if !differs {
		t.Fatal("different sessions should spread across targets")
	}

	if got := chainKeys(orderSticky(chain, "")); got != "a,b,c,d" {
		t.Fatalf("no affinity keeps configured order, got %q", got)
	}

	ctx := withRoutingAffinity(context.Background(), "s1")
	if got := routingAffinity(ctx, &authResult{token: &service.APIToken{ID: "tok"}}); got != "session:s1" {
		t.Fatalf("session wins over token, got %q", got)
	}
	if got := routingAffinity(context.Background(), &authResult{token: &service.APIToken{ID: "tok"}}); got != "token:tok" {
		t.Fatalf("token fallback, got %q", got)
	}
}

func TestRouteDecisionHeader(t *testing.T) {
	chain := []chatCallTarget{
		{fullModel: "a/m", strategy: "weighted"},
		{fullModel: "b/m", strategy: "weighted"},
		{fullModel: "c/m", strategy: "weighted", deferred: "circuit_open"},
	}
	got := routeDecision("stack", chain, "b/m")
	want := "profile=stack; strategy=weighted; served=b/m; attempt=2/3; order=a/m,b/m,c/m; deferred=c/m:circuit_open"
	if got != want {
		t.Fatalf("decision\n got %q\nwant %q", got, want)
	}

	if got := routeDecision("", []chatCallTarget{{fullModel: "a/m"}}, "a/m"); got != "served=a/m; attempt=1/1" {
		t.Fatalf("direct request decision %q", got)
	}
}

func TestProviderBreaker(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	b := newProviderBreaker()
	b.now = func() time.Time { return now }
	outage := &service.UpstreamError{Provider: "a", StatusCode: 503}

	for i := range providerBreakerThreshold - 1 {
		b.Failure("a", outage)
		if _, open := b.OpenUntil("a"); open {
			t.Fatalf("opened after %d failures", i+1)
		}
	}
	b.Failure("a", outage)
	until, open := b.OpenUntil("a")
	if !open || until.Sub(now) != providerBreakerOpenBase {
		t.Fatalf("expected open for %s, got %v %v", providerBreakerOpenBase, open, until.Sub(now))
	}

	// Failures while open are not new evidence.
	b.Failure("a", outage)
	if again, _ := b.OpenUntil("a"); !again.Equal(until) {
		t.Fatal("a failure while open must not extend the window")
	}

	// Half-open: the first failed probe reopens immediately with a longer window.
	now = until.Add(time.Second)
	if _, open := b.OpenUntil("a"); open {
		t.Fatal("window elapsed: the next request is the probe")
	}
	b.Failure("a", outage)
	until2, open := b.OpenUntil("a")
	if !open || until2.Sub(now) != 2*providerBreakerOpenBase {
		t.Fatalf("failed probe should reopen for %s, got %v", 2*providerBreakerOpenBase, until2.Sub(now))
	}

	// A success closes and forgets.
	now = until2.Add(time.Second)
	b.Success("a")
	b.Failure("a", outage)
	if _, open := b.OpenUntil("a"); open {
		t.Fatal("after a success the streak restarts from zero")
	}
}

func TestIsProviderOutage(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"5xx", &service.UpstreamError{StatusCode: 502}, true},
		{"408", &service.UpstreamError{StatusCode: http.StatusRequestTimeout}, true},
		{"4xx", &service.UpstreamError{StatusCode: 400}, false},
		{"transport", errors.New("dial tcp: connection refused"), true},
		{"client cancel", context.Canceled, false},
		{"client deadline", fmt.Errorf("call: %w", context.DeadlineExceeded), false},
		{"budget", service.ErrProviderBudgetExceeded, false},
		{"unsupported", service.ErrUnsupportedOperation, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isProviderOutage(tt.err); got != tt.want {
				t.Fatalf("isProviderOutage(%v) = %v want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestPartitionDefersCircuitOpenTargets(t *testing.T) {
	s := &Server{cooldown: newProviderCooldown(), breaker: newProviderBreaker()}
	for range providerBreakerThreshold {
		s.noteProviderError("a", &service.UpstreamError{StatusCode: 500})
	}
	// A rate limit must not count toward the breaker.
	for range providerBreakerThreshold {
		s.noteProviderError("b", &service.RateLimitError{StatusCode: 429})
	}
	s.cooldown.Clear("b")

	got := s.partitionCooledTargets(chainOf("a", "b"))
	if keys := chainKeys(got); keys != "b,a" {
		t.Fatalf("order %q want b,a", keys)
	}
	if got[1].deferred != "circuit_open" {
		t.Fatalf("deferred reason %q", got[1].deferred)
	}

	s.noteProviderResponse("a", nil)
	if keys := chainKeys(s.partitionCooledTargets(chainOf("a", "b"))); keys != "a,b" {
		t.Fatalf("a success must close the breaker, order %q", keys)
	}
}

// randFloat is a tiny deterministic PCG-free generator so the weighted tests
// do not depend on global randomness.
func randFloat() func() float64 {
	var x uint64 = 88172645463325252
	return func() float64 {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		return float64(x>>11) / float64(1<<53)
	}
}
