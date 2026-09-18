package server

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// Cooldown bounds.
//
// The clamp is not defensive decoration: Retry-After and the *-reset headers are
// attacker- or misconfiguration-controlled strings from upstream, and an
// unbounded value would park a provider for as long as it asked. The floor keeps
// a 0-second reset from being a no-op entry that churns the registry.
const (
	providerCooldownMin = time.Second
	providerCooldownMax = 15 * time.Minute

	// providerCooldownDefault applies when upstream reports exhaustion without
	// saying for how long. Short enough that a stale entry costs little, long
	// enough to skip the burst that produced it.
	providerCooldownDefault = 20 * time.Second

	// providerCooldownDisabled is the in-code rollback switch, mirroring
	// loopgov.Config.Disabled. Flipping it makes the registry inert without
	// reverting the wiring.
	providerCooldownDisabled = false
)

// providerCooldown records, per provider key, the instant at which upstream said
// it would accept traffic again.
//
// State is in-memory and per replica. Sharing it would mean a database write on
// the request path for information that expires in seconds and that each replica
// re-learns from its own next 429 — the cost is permanent and the benefit
// evaporates. A restart clears it, which is correct: the registry is a cache of
// a claim upstream made, not a fact about the provider.
type providerCooldown struct {
	mu    sync.RWMutex
	until map[string]time.Time
}

func newProviderCooldown() *providerCooldown {
	return &providerCooldown{until: make(map[string]time.Time)}
}

// Enter puts a provider in cooldown until the given instant, clamped. A later
// deadline never shortens an earlier one: two signals in the same window should
// not let the second, vaguer one release the provider early.
func (c *providerCooldown) Enter(providerKey string, d time.Duration, reason string) {
	if providerCooldownDisabled || c == nil || providerKey == "" {
		return
	}

	switch {
	case d < providerCooldownMin:
		d = providerCooldownMin
	case d > providerCooldownMax:
		slog.Warn("gateway: clamping upstream cooldown",
			"provider", providerKey, "requested", d, "capped_to", providerCooldownMax, "reason", reason)
		d = providerCooldownMax
	}
	deadline := time.Now().Add(d)

	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.until[providerKey]; ok && existing.After(deadline) {
		return
	}
	c.until[providerKey] = deadline

	slog.Warn("gateway: provider entering cooldown",
		"provider", providerKey, "until", deadline.UTC().Format(time.RFC3339), "for", d, "reason", reason)
}

// Clear releases a provider, used when it serves a request with headroom left.
func (c *providerCooldown) Clear(providerKey string) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.until[providerKey]; !ok {
		return
	}
	delete(c.until, providerKey)

	slog.Info("gateway: provider cooldown cleared", "provider", providerKey)
}

// CooledUntil reports the remaining cooldown, or zero when the provider is
// available. An expired entry is treated as absent without needing a sweeper.
func (c *providerCooldown) CooledUntil(providerKey string) (time.Time, bool) {
	if providerCooldownDisabled || c == nil {
		return time.Time{}, false
	}

	c.mu.RLock()
	deadline, ok := c.until[providerKey]
	c.mu.RUnlock()

	if !ok || !deadline.After(time.Now()) {
		return time.Time{}, false
	}

	return deadline, true
}

// ─── Upstream signal ingestion ───

// rateLimitSnapshot is what AT reads out of an upstream response's rate-limit
// headers. Before this, those headers were captured and forwarded to the client
// verbatim but never read, so a provider that had just told us it was empty was
// still first in line on the next request.
type rateLimitSnapshot struct {
	// Exhausted is true when any bucket reported zero remaining.
	Exhausted bool
	// ResetIn is the time until the exhausted bucket refills, when stated.
	ResetIn time.Duration
	// HasHeadroom is true when at least one bucket reported a positive
	// remaining count and none reported zero.
	HasHeadroom bool
}

// anthropicRateLimitBuckets are the per-bucket header families Anthropic emits.
var anthropicRateLimitBuckets = []string{"requests", "tokens", "input-tokens", "output-tokens"}

// parseRateLimitHeaders reads the Anthropic and OpenAI-family conventions.
// Unparseable values are ignored rather than guessed: a malformed header says
// nothing, and acting on a misread one would take a healthy provider out of
// rotation.
func parseRateLimitHeaders(h http.Header) rateLimitSnapshot {
	var snap rateLimitSnapshot
	if h == nil {
		return snap
	}

	consider := func(remainingKey, resetKey string) {
		raw := h.Get(remainingKey)
		if raw == "" {
			return
		}
		remaining, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			slog.Debug("gateway: unparseable rate-limit remaining header", "header", remainingKey, "value", raw)
			return
		}
		if remaining > 0 {
			snap.HasHeadroom = true
			return
		}

		snap.Exhausted = true
		if reset := parseRateLimitReset(h.Get(resetKey)); reset > 0 && (snap.ResetIn == 0 || reset < snap.ResetIn) {
			// The soonest refill among exhausted buckets is when the provider
			// can serve anything at all.
			snap.ResetIn = reset
		}
	}

	for _, bucket := range anthropicRateLimitBuckets {
		consider("anthropic-ratelimit-"+bucket+"-remaining", "anthropic-ratelimit-"+bucket+"-reset")
	}
	for _, bucket := range []string{"requests", "tokens"} {
		consider("x-ratelimit-remaining-"+bucket, "x-ratelimit-reset-"+bucket)
	}

	// Exhaustion wins: a response reporting one empty bucket and one with room
	// is still a response that cannot be repeated.
	if snap.Exhausted {
		snap.HasHeadroom = false
	}

	return snap
}

// parseRateLimitReset accepts the three shapes these headers use in the wild: an
// RFC 3339 instant (Anthropic), a Go-style duration such as "30s" or "1m30s"
// (OpenAI), and a bare number of seconds.
func parseRateLimitReset(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}

	if ts, err := time.Parse(time.RFC3339, v); err == nil {
		if d := time.Until(ts); d > 0 {
			return d
		}

		return 0
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil && secs > 0 {
		return time.Duration(secs * float64(time.Second))
	}

	slog.Debug("gateway: unparseable rate-limit reset header", "value", v)

	return 0
}

// noteProviderResponse folds a successful upstream response's headers into the
// cooldown registry.
func (s *Server) noteProviderResponse(providerKey string, h http.Header) {
	if s.cooldown == nil || providerKey == "" {
		return
	}

	snap := parseRateLimitHeaders(h)
	switch {
	case snap.Exhausted:
		d := snap.ResetIn
		if d <= 0 {
			d = providerCooldownDefault
		}
		s.cooldown.Enter(providerKey, d, "bucket exhausted on a successful response")
	case snap.HasHeadroom:
		s.cooldown.Clear(providerKey)
	}
}

// noteProviderError folds a failed upstream call into the cooldown registry. A
// typed rate-limit error is the strongest signal available; anything else is
// left alone, because a 500 says nothing about quota.
func (s *Server) noteProviderError(providerKey string, err error) {
	if s.cooldown == nil || providerKey == "" || err == nil {
		return
	}

	var rle *service.RateLimitError
	if !errors.As(err, &rle) {
		// A 500 or a timeout says nothing about quota, so it must not park the
		// provider. Only an explicit rate-limit signal does.
		return
	}

	// RetryAfter is already parsed by the adapter that raised this
	// (common.ParseRetryAfter); the error carries no headers of its own.
	d := rle.RetryAfter
	if d <= 0 {
		d = providerCooldownDefault
	}

	s.cooldown.Enter(providerKey, d, "upstream returned "+strconv.Itoa(rle.StatusCode))
}

// partitionCooledTargets is a stable partition that moves cooled targets to the
// back of the chain, preserving relative order within each group.
//
// Cooled targets are moved, never removed. If every candidate is cooled — or the
// chain has one entry — the request is still attempted, so a stale or wrong
// cooldown costs one wasted attempt rather than turning into a hard failure for
// a provider that is actually healthy. Removal would convert bad cache state
// into an outage, which is strictly worse than the status quo.
func (s *Server) partitionCooledTargets(chain []chatCallTarget) []chatCallTarget {
	if s.cooldown == nil || len(chain) < 2 {
		return chain
	}

	ready := make([]chatCallTarget, 0, len(chain))
	cooled := make([]chatCallTarget, 0, len(chain))
	for _, target := range chain {
		if _, cooling := s.cooldown.CooledUntil(target.providerKey); cooling && target.err == nil {
			cooled = append(cooled, target)

			continue
		}
		ready = append(ready, target)
	}
	if len(cooled) == 0 {
		return chain
	}

	return append(ready, cooled...)
}
