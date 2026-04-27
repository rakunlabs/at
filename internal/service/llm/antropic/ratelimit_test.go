package antropic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/ratelimit"
)

// TestChat_429MapsToRateLimitError confirms that an upstream 429 with a
// Retry-After header is surfaced as a *service.RateLimitError so callers
// (org-delegation retry loop) can honour it.
func TestChat_429MapsToRateLimitError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "42")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "rate_limit_error",
				"message": "Number of requests has exceeded your rate limit.",
			},
		})
	}))
	defer srv.Close()

	p, err := New("test-key", "claude-test", srv.URL, "", false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = p.Chat(context.Background(), "claude-test",
		[]service.Message{{Role: "user", Content: "hi"}}, nil, nil)
	if err == nil {
		t.Fatal("expected error from 429, got nil")
	}

	var rle *service.RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("expected *service.RateLimitError, got %T: %v", err, err)
	}
	if rle.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, want 429", rle.StatusCode)
	}
	if rle.RetryAfter != 42*time.Second {
		t.Errorf("RetryAfter = %s, want 42s", rle.RetryAfter)
	}
	if rle.Provider != "anthropic" {
		t.Errorf("Provider = %q, want anthropic", rle.Provider)
	}
	if !service.IsRateLimitError(err) {
		t.Error("IsRateLimitError returned false for *RateLimitError")
	}
}

// TestChat_RateLimiterMaxConcurrentSerializes verifies that with
// MaxConcurrent=1 the limiter forces sequential calls even under
// goroutine fan-out.
func TestChat_RateLimiterMaxConcurrentSerializes(t *testing.T) {
	var inflight atomic.Int32
	var maxSeen atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inflight.Add(1)
		defer inflight.Add(-1)
		for {
			cur := maxSeen.Load()
			if n <= cur || maxSeen.CompareAndSwap(cur, n) {
				break
			}
		}
		// Hold the request briefly so concurrent attempts can pile up.
		time.Sleep(20 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_1",
			"type":        "message",
			"role":        "assistant",
			"model":       "claude-test",
			"content":     []map[string]any{{"type": "text", "text": "ok"}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer srv.Close()

	limiter := ratelimit.New(ratelimit.Config{
		MaxConcurrent: 1,
		WaitTimeout:   5 * time.Second,
	})
	p, err := New("test-key", "claude-test", srv.URL, "", false, WithRateLimiter(limiter))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := p.Chat(context.Background(), "claude-test",
				[]service.Message{{Role: "user", Content: "hi"}}, nil, nil)
			if err != nil {
				t.Errorf("Chat: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := maxSeen.Load(); got > 1 {
		t.Fatalf("max in-flight requests = %d; expected 1 with MaxConcurrent=1", got)
	}
}

// TestChat_NilLimiterUnchanged ensures providers without a limiter
// behave exactly like before this change (no extra blocking, no extra
// allocations).
func TestChat_NilLimiterUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_1",
			"type":        "message",
			"role":        "assistant",
			"model":       "claude-test",
			"content":     []map[string]any{{"type": "text", "text": "ok"}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer srv.Close()

	p, err := New("test-key", "claude-test", srv.URL, "", false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.limiter != nil {
		t.Fatal("expected nil limiter when WithRateLimiter not used")
	}

	resp, err := p.Chat(context.Background(), "claude-test",
		[]service.Message{{Role: "user", Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("Content = %q, want ok", resp.Content)
	}
}
