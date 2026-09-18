package server

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func headers(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}

	return h
}

func TestParseRateLimitHeaders(t *testing.T) {
	future := time.Now().Add(90 * time.Second).UTC().Format(time.RFC3339)

	cases := []struct {
		name          string
		header        http.Header
		wantExhausted bool
		wantHeadroom  bool
		minReset      time.Duration
		maxReset      time.Duration
	}{
		{
			name:          "anthropic exhausted with RFC3339 reset",
			header:        headers("anthropic-ratelimit-tokens-remaining", "0", "anthropic-ratelimit-tokens-reset", future),
			wantExhausted: true,
			minReset:      80 * time.Second,
			maxReset:      95 * time.Second,
		},
		{
			name:          "openai exhausted with duration reset",
			header:        headers("x-ratelimit-remaining-requests", "0", "x-ratelimit-reset-requests", "30s"),
			wantExhausted: true,
			minReset:      29 * time.Second,
			maxReset:      31 * time.Second,
		},
		{
			name:          "bare seconds reset",
			header:        headers("x-ratelimit-remaining-tokens", "0", "x-ratelimit-reset-tokens", "45"),
			wantExhausted: true,
			minReset:      44 * time.Second,
			maxReset:      46 * time.Second,
		},
		{
			name:         "headroom reported",
			header:       headers("anthropic-ratelimit-tokens-remaining", "12000"),
			wantHeadroom: true,
		},
		{
			// A response with one empty bucket cannot be repeated, whatever the
			// other buckets say.
			name:          "exhaustion wins over headroom",
			header:        headers("anthropic-ratelimit-requests-remaining", "5", "anthropic-ratelimit-tokens-remaining", "0"),
			wantExhausted: true,
		},
		{
			name:          "malformed reset is ignored but exhaustion stands",
			header:        headers("anthropic-ratelimit-tokens-remaining", "0", "anthropic-ratelimit-tokens-reset", "not-a-timestamp"),
			wantExhausted: true,
		},
		{
			name:   "malformed remaining says nothing",
			header: headers("anthropic-ratelimit-tokens-remaining", "many"),
		},
		{
			name:   "no rate-limit headers at all",
			header: headers("Content-Type", "application/json"),
		},
		{
			name:   "nil header",
			header: nil,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRateLimitHeaders(tt.header)
			if got.Exhausted != tt.wantExhausted {
				t.Fatalf("exhausted=%v want %v", got.Exhausted, tt.wantExhausted)
			}
			if got.HasHeadroom != tt.wantHeadroom {
				t.Fatalf("headroom=%v want %v", got.HasHeadroom, tt.wantHeadroom)
			}
			if tt.minReset > 0 && (got.ResetIn < tt.minReset || got.ResetIn > tt.maxReset) {
				t.Fatalf("reset=%s want between %s and %s", got.ResetIn, tt.minReset, tt.maxReset)
			}
		})
	}
}

func TestProviderCooldownLifecycle(t *testing.T) {
	c := newProviderCooldown()

	if _, cooling := c.CooledUntil("anthropic"); cooling {
		t.Fatal("a fresh registry must report nothing in cooldown")
	}

	c.Enter("anthropic", 40*time.Second, "test")
	until, cooling := c.CooledUntil("anthropic")
	if !cooling {
		t.Fatal("provider should be cooling")
	}
	if remaining := time.Until(until); remaining < 35*time.Second || remaining > 41*time.Second {
		t.Fatalf("remaining %s", remaining)
	}

	// A later, vaguer signal must not release the provider early.
	c.Enter("anthropic", time.Second, "shorter")
	if until2, _ := c.CooledUntil("anthropic"); until2.Before(until) {
		t.Fatalf("a shorter signal shortened an existing cooldown: %s < %s", until2, until)
	}

	c.Clear("anthropic")
	if _, cooling := c.CooledUntil("anthropic"); cooling {
		t.Fatal("cooldown should be cleared")
	}
}

// Retry-After and *-reset are upstream-controlled strings, so they are clamped
// at both ends rather than trusted.
func TestProviderCooldownClampsBothEnds(t *testing.T) {
	c := newProviderCooldown()

	c.Enter("greedy", 24*time.Hour, "hostile header")
	until, cooling := c.CooledUntil("greedy")
	if !cooling {
		t.Fatal("provider should be cooling")
	}
	if remaining := time.Until(until); remaining > providerCooldownMax+time.Second {
		t.Fatalf("remaining %s exceeds the %s clamp", remaining, providerCooldownMax)
	}

	c.Enter("instant", 0, "zero reset")
	if _, cooling := c.CooledUntil("instant"); !cooling {
		t.Fatal("a zero-duration signal should still register the minimum cooldown")
	}
}

func TestProviderCooldownExpiresWithoutIntervention(t *testing.T) {
	c := newProviderCooldown()

	// Write an already-elapsed deadline directly: the floor clamp prevents
	// expressing this through Enter, and the property under test is that a past
	// deadline reads as available without a sweeper.
	c.mu.Lock()
	c.until["stale"] = time.Now().Add(-time.Second)
	c.mu.Unlock()

	if _, cooling := c.CooledUntil("stale"); cooling {
		t.Fatal("an elapsed cooldown must read as available")
	}
}

func TestNoteProviderErrorOnlyActsOnRateLimits(t *testing.T) {
	s := &Server{cooldown: newProviderCooldown()}

	// A 500 says nothing about quota and must not park the provider.
	s.noteProviderError("openai", &service.UpstreamError{StatusCode: 500, Message: "boom"})
	if _, cooling := s.cooldown.CooledUntil("openai"); cooling {
		t.Fatal("a 5xx must not enter cooldown")
	}

	s.noteProviderError("openai", errors.New("dial tcp: connection refused"))
	if _, cooling := s.cooldown.CooledUntil("openai"); cooling {
		t.Fatal("an untyped error must not enter cooldown")
	}

	s.noteProviderError("openai", &service.RateLimitError{StatusCode: 429, RetryAfter: 45 * time.Second})
	until, cooling := s.cooldown.CooledUntil("openai")
	if !cooling {
		t.Fatal("a 429 must enter cooldown")
	}
	if remaining := time.Until(until); remaining < 40*time.Second || remaining > 46*time.Second {
		t.Fatalf("remaining %s want ~45s from Retry-After", remaining)
	}

	// Without upstream guidance the bounded default applies.
	s.noteProviderError("anthropic", &service.RateLimitError{StatusCode: 529})
	until, cooling = s.cooldown.CooledUntil("anthropic")
	if !cooling {
		t.Fatal("a 529 must enter cooldown")
	}
	if remaining := time.Until(until); remaining > providerCooldownDefault+time.Second {
		t.Fatalf("remaining %s want the %s default", remaining, providerCooldownDefault)
	}
}

func TestNoteProviderResponseEntersAndClears(t *testing.T) {
	s := &Server{cooldown: newProviderCooldown()}

	s.noteProviderResponse("anthropic", headers(
		"anthropic-ratelimit-tokens-remaining", "0",
		"anthropic-ratelimit-tokens-reset", time.Now().Add(60*time.Second).UTC().Format(time.RFC3339),
	))
	if _, cooling := s.cooldown.CooledUntil("anthropic"); !cooling {
		t.Fatal("an exhausted bucket on a 200 must enter cooldown")
	}

	s.noteProviderResponse("anthropic", headers("anthropic-ratelimit-tokens-remaining", "50000"))
	if _, cooling := s.cooldown.CooledUntil("anthropic"); cooling {
		t.Fatal("a successful call with headroom must clear the cooldown")
	}

	// Silence is not headroom: a provider without rate-limit headers must not
	// have an unrelated cooldown wiped by an unrelated success.
	s.cooldown.Enter("quiet", 30*time.Second, "test")
	s.noteProviderResponse("quiet", headers("Content-Type", "application/json"))
	if _, cooling := s.cooldown.CooledUntil("quiet"); !cooling {
		t.Fatal("a response with no rate-limit headers must leave cooldown untouched")
	}
}

func chainOf(keys ...string) []chatCallTarget {
	out := make([]chatCallTarget, 0, len(keys))
	for _, k := range keys {
		out = append(out, chatCallTarget{fullModel: k + "/m", providerKey: k})
	}

	return out
}

func chainKeys(chain []chatCallTarget) string {
	keys := make([]string, 0, len(chain))
	for _, t := range chain {
		keys = append(keys, t.providerKey)
	}

	return strings.Join(keys, ",")
}

func TestPartitionCooledTargetsIsStableAndFailsOpen(t *testing.T) {
	s := &Server{cooldown: newProviderCooldown()}
	s.cooldown.Enter("b", 60*time.Second, "test")
	s.cooldown.Enter("c", 60*time.Second, "test")

	t.Run("cooled targets move to the back preserving order", func(t *testing.T) {
		got := s.partitionCooledTargets(chainOf("a", "b", "c", "d"))
		if keys := chainKeys(got); keys != "a,d,b,c" {
			t.Fatalf("order %q want a,d,b,c", keys)
		}
	})

	t.Run("nothing is dropped", func(t *testing.T) {
		got := s.partitionCooledTargets(chainOf("a", "b", "c", "d"))
		if len(got) != 4 {
			t.Fatalf("chain length %d want 4 — cooled targets are moved, never removed", len(got))
		}
	})

	t.Run("a single cooled target is still attempted", func(t *testing.T) {
		got := s.partitionCooledTargets(chainOf("b"))
		if len(got) != 1 || got[0].providerKey != "b" {
			t.Fatalf("a one-entry chain must be returned untouched: %+v", got)
		}
	})

	t.Run("an all-cooled chain keeps its original order", func(t *testing.T) {
		got := s.partitionCooledTargets(chainOf("b", "c"))
		if keys := chainKeys(got); keys != "b,c" {
			t.Fatalf("order %q want b,c", keys)
		}
	})

	t.Run("no cooled targets leaves the chain identical", func(t *testing.T) {
		got := s.partitionCooledTargets(chainOf("a", "d"))
		if keys := chainKeys(got); keys != "a,d" {
			t.Fatalf("order %q want a,d", keys)
		}
	})
}

// An invalid primary carries the error the caller reports; reordering must not
// bury it behind a cooled target and change which error surfaces.
func TestPartitionCooledTargetsKeepsErrorTargetsInPlace(t *testing.T) {
	s := &Server{cooldown: newProviderCooldown()}
	s.cooldown.Enter("a", 60*time.Second, "test")

	chain := []chatCallTarget{
		{fullModel: "a/m", providerKey: "a", err: errors.New("invalid primary")},
		{fullModel: "b/m", providerKey: "b"},
	}
	got := s.partitionCooledTargets(chain)
	if got[0].err == nil {
		t.Fatalf("an error target must not be reordered: %+v", got)
	}
}
