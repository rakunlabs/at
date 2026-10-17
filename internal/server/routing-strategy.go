package server

import (
	"context"
	"hash/fnv"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Routing strategies ───
//
// A routing profile's strategy only decides the order its targets are tried
// in. Every strategy keeps the full chain, so a request still falls back
// through the remaining targets, and partitionCooledTargets still moves cooled
// or circuit-open targets to the back afterwards. Ordering therefore never
// changes which targets a token may reach, only which one is tried first.

type routingAffinityKey struct{}

// withRoutingAffinity names the conversation a request belongs to, which the
// sticky strategy hashes to keep one conversation on one target.
func withRoutingAffinity(ctx context.Context, key string) context.Context {
	if key == "" {
		return ctx
	}

	return context.WithValue(ctx, routingAffinityKey{}, key)
}

func routingAffinity(ctx context.Context, auth *authResult) string {
	if key, ok := ctx.Value(routingAffinityKey{}).(string); ok && key != "" {
		return "session:" + key
	}
	if auth != nil && auth.token != nil && auth.token.ID != "" {
		return "token:" + auth.token.ID
	}

	return ""
}

// routingRoundRobin holds one counter per profile. It is in-memory and per
// replica: rotation spreads load, it is not a fairness guarantee, so sharing
// it would buy nothing worth a database write per request.
type routingRoundRobin struct {
	counters sync.Map // workspace/profile ID → *atomic.Uint64
}

func (r *routingRoundRobin) next(key string) uint64 {
	v, _ := r.counters.LoadOrStore(key, new(atomic.Uint64))

	return v.(*atomic.Uint64).Add(1) - 1
}

// orderRoutingTargets applies the profile's strategy to the resolved targets.
func (s *Server) orderRoutingTargets(ctx context.Context, auth *authResult, profile *service.RoutingProfile, targets []chatCallTarget) []chatCallTarget {
	if len(targets) < 2 {
		return targets
	}

	switch profile.Strategy {
	case service.RoutingStrategyRoundRobin:
		start := int(s.routingRR.next(profile.WorkspaceID+"/"+profile.ID) % uint64(len(targets)))

		return append(slices.Clone(targets[start:]), targets[:start]...)
	case service.RoutingStrategyWeighted:
		return orderWeighted(targets, profile.Weights, rand.Float64)
	case service.RoutingStrategyLeastCost:
		return s.orderLeastCost(ctx, auth, targets)
	case service.RoutingStrategySticky:
		return orderSticky(targets, routingAffinity(ctx, auth))
	default:
		return targets
	}
}

// orderWeighted draws an order without replacement with probability
// proportional to weight (Efraimidis–Spirakis keys). A missing weight is 1; a
// zero weight keeps the target as a fallback after every weighted target, in
// configured order.
func orderWeighted(targets []chatCallTarget, weights map[string]int, random func() float64) []chatCallTarget {
	type keyed struct {
		target chatCallTarget
		key    float64
	}

	weighted := make([]keyed, 0, len(targets))
	zero := make([]chatCallTarget, 0)
	for _, target := range targets {
		w := 1
		if v, ok := weights[target.fullModel]; ok {
			w = v
		}
		if w <= 0 {
			zero = append(zero, target)

			continue
		}
		u := random()
		if u <= 0 {
			u = math.SmallestNonzeroFloat64
		}
		weighted = append(weighted, keyed{target: target, key: math.Pow(u, 1/float64(w))})
	}
	sort.SliceStable(weighted, func(i, j int) bool { return weighted[i].key > weighted[j].key })

	out := make([]chatCallTarget, 0, len(targets))
	for _, k := range weighted {
		out = append(out, k.target)
	}

	return append(out, zero...)
}

// orderSticky ranks targets by rendezvous hash of the affinity key, so a
// conversation keeps landing on the same target (and its prompt cache) and
// adding or removing a target only moves the conversations that hashed to it.
// Without a key the configured order is kept.
func orderSticky(targets []chatCallTarget, affinity string) []chatCallTarget {
	if affinity == "" {
		return targets
	}

	score := func(model string) uint64 {
		h := fnv.New64a()
		_, _ = h.Write([]byte(affinity))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(model))

		return h.Sum64()
	}

	out := slices.Clone(targets)
	sort.SliceStable(out, func(i, j int) bool { return score(out[i].fullModel) > score(out[j].fullModel) })

	return out
}

// orderLeastCost puts the cheapest priced target first, by input plus output
// price per 1M tokens. Unpriced targets keep their configured order after the
// priced ones: an unknown price is not a zero price.
func (s *Server) orderLeastCost(ctx context.Context, auth *authResult, targets []chatCallTarget) []chatCallTarget {
	if s.agentBudgetStore == nil {
		return targets
	}

	keys := make([]string, 0, len(targets))
	for _, target := range targets {
		if !slices.Contains(keys, target.providerKey) {
			keys = append(keys, target.providerKey)
		}
	}

	workspaceID := pricingWorkspaceID(ctx)
	if auth != nil && auth.token != nil && auth.token.WorkspaceID != "" {
		workspaceID = auth.token.WorkspaceID
	}
	pricing, err := s.modelPricingFor(ctx, workspaceID, keys...)
	if err != nil {
		slog.Warn("routing profile: least_cost pricing unavailable, keeping configured order", "error", err)

		return targets
	}

	type priced struct {
		target chatCallTarget
		cost   float64
	}
	known := make([]priced, 0, len(targets))
	unknown := make([]chatCallTarget, 0)
	for _, target := range targets {
		p, ok := findModelPricing(pricing, target.providerKey, target.actualModel, target.fullModel)
		if !ok {
			unknown = append(unknown, target)

			continue
		}
		known = append(known, priced{target: target, cost: p.PromptPricePer1M + p.CompletionPricePer1M})
	}
	sort.SliceStable(known, func(i, j int) bool { return known[i].cost < known[j].cost })

	out := make([]chatCallTarget, 0, len(targets))
	for _, k := range known {
		out = append(out, k.target)
	}

	return append(out, unknown...)
}

// ─── Route decision header ───

// routeDecisionHeader is `x-at-route-decision`. It explains why the serving
// target was chosen: the profile and strategy (when a profile routed the
// request), the attempted order, the 1-based attempt that answered, and any
// target that was moved back and why. Failed attempts are the entries of
// `order` before `attempt`.
const routeDecisionHeader = "x-at-route-decision"

func routeDecision(profile string, chain []chatCallTarget, served string) string {
	parts := make([]string, 0, 6)
	if profile != "" {
		parts = append(parts, "profile="+profile)
	}

	strategy := ""
	order := make([]string, 0, len(chain))
	deferred := make([]string, 0)
	attempt := 0
	for _, target := range chain {
		if target.err != nil {
			continue
		}
		order = append(order, target.fullModel)
		if target.fullModel == served && attempt == 0 {
			attempt = len(order)
		}
		if target.strategy != "" {
			strategy = target.strategy
		}
		if target.deferred != "" {
			deferred = append(deferred, target.fullModel+":"+target.deferred)
		}
	}

	if strategy != "" {
		parts = append(parts, "strategy="+strategy)
	}
	parts = append(parts, "served="+served)
	if attempt > 0 {
		parts = append(parts, "attempt="+strconv.Itoa(attempt)+"/"+strconv.Itoa(len(order)))
	}
	if len(order) > 1 {
		parts = append(parts, "order="+strings.Join(order, ","))
	}
	if len(deferred) > 0 {
		parts = append(parts, "deferred="+strings.Join(deferred, ","))
	}

	return strings.Join(parts, "; ")
}

func setRouteDecision(h http.Header, profile string, chain []chatCallTarget, served string) {
	h.Set(routeDecisionHeader, routeDecision(profile, chain, served))
}
