package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// TestCallWithGatewayRetry_RetriesOn429 verifies that the gateway
// retry helper retries on a typed *RateLimitError and surfaces the
// successful response from the second attempt — the core behaviour
// that was missing from the gateway before this change. Previously a
// single 429 from upstream Anthropic immediately became a 502 to
// opencode; now the helper transparently retries.
func TestCallWithGatewayRetry_RetriesOn429(t *testing.T) {
	t.Setenv("AT_GATEWAY_MIN_BACKOFF_MS", "10") // keep the test fast

	var calls atomic.Int32
	fn := func(ctx context.Context) (string, error) {
		n := calls.Add(1)
		if n < 2 {
			return "", &service.RateLimitError{
				StatusCode: http.StatusTooManyRequests,
				RetryAfter: 0, // exercise the linear-backoff fallback path
				Provider:   "anthropic",
				Message:    "rate limited",
				Underlying: errors.New("upstream 429"),
			}
		}
		return "ok", nil
	}

	got, err := callWithGatewayRetry(context.Background(), "anthropic", "claude-test", 0, fn)
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if got != "ok" {
		t.Errorf("result = %q, want ok", got)
	}
	if calls.Load() != 2 {
		t.Errorf("attempts = %d, want 2", calls.Load())
	}
}

// TestCallWithGatewayRetry_NoRetryOnNonRateLimit confirms that
// non-rate-limit errors (e.g. a real 401 or 5xx without the typed
// envelope) bubble up immediately without burning the retry budget.
func TestCallWithGatewayRetry_NoRetryOnNonRateLimit(t *testing.T) {
	var calls atomic.Int32
	wantErr := errors.New("auth failed (status 401)")
	fn := func(ctx context.Context) (string, error) {
		calls.Add(1)
		return "", wantErr
	}

	_, err := callWithGatewayRetry(context.Background(), "anthropic", "claude-test", 0, fn)
	if !errors.Is(err, wantErr) {
		t.Fatalf("got error %v, want %v", err, wantErr)
	}
	if calls.Load() != 1 {
		t.Errorf("attempts = %d; should not retry on non-rate-limit error", calls.Load())
	}
}

// TestCallWithGatewayRetry_GivesUpAfterCap exercises the per-call cap
// on upstream Retry-After: when the upstream asks for a long wait we
// only ever sleep up to retryAfterCap before retrying, so the
// synchronous request path doesn't appear hung.
//
// We use a short test cap (200ms) so the test runs fast.
func TestCallWithGatewayRetry_GivesUpAfterCap(t *testing.T) {
	var calls atomic.Int32
	rle := &service.RateLimitError{
		StatusCode: 529,
		RetryAfter: 1 * time.Hour, // ridiculous, must be capped
		Provider:   "anthropic",
		Message:    "overloaded",
		Underlying: errors.New("upstream 529"),
	}
	fn := func(ctx context.Context) (string, error) {
		calls.Add(1)
		return "", rle
	}

	testCap := 200 * time.Millisecond
	start := time.Now()
	_, err := callWithGatewayRetry(context.Background(), "anthropic", "claude-test", testCap, fn)
	elapsed := time.Since(start)

	var rle2 *service.RateLimitError
	if !errors.As(err, &rle2) {
		t.Fatalf("expected *service.RateLimitError after exhaustion, got %T: %v", err, err)
	}
	if calls.Load() != int32(gatewayRetryAttempts) {
		t.Errorf("attempts = %d, want %d", calls.Load(), gatewayRetryAttempts)
	}
	// 3 attempts → 2 sleeps of testCap each ≈ 400ms. Allow some slack.
	maxExpected := 2*testCap + 500*time.Millisecond
	if elapsed > maxExpected {
		t.Errorf("elapsed = %s; expected ≤ %s (2 sleeps × cap + slack)",
			elapsed, maxExpected)
	}
}

// TestCallWithGatewayRetry_NoCapHonoursUpstream confirms the negative
// retryAfterCap sentinel disables capping entirely. Used for paid
// Anthropic accounts where multi-minute quota resets are real and the
// operator wants the gateway to wait verbatim.
func TestCallWithGatewayRetry_NoCapHonoursUpstream(t *testing.T) {
	var calls atomic.Int32
	rle := &service.RateLimitError{
		StatusCode: 429,
		RetryAfter: 50 * time.Millisecond, // small so the test stays quick
		Provider:   "anthropic",
		Message:    "rate limited",
		Underlying: errors.New("upstream 429"),
	}
	fn := func(ctx context.Context) (string, error) {
		n := calls.Add(1)
		if n < 2 {
			return "", rle
		}
		return "ok", nil
	}

	got, err := callWithGatewayRetry(context.Background(),
		"anthropic", "claude-test", -1, fn)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if got != "ok" {
		t.Errorf("result = %q, want ok", got)
	}
}

// TestCallWithGatewayRetry_DefaultCapWhenZero confirms the helper
// uses defaultGatewayRetryAfterCap (60s) when retryAfterCap is 0. We
// verify by passing a small upstream Retry-After (50ms) that the
// default cap won't trim — the retry succeeds quickly.
func TestCallWithGatewayRetry_DefaultCapWhenZero(t *testing.T) {
	var calls atomic.Int32
	fn := func(ctx context.Context) (string, error) {
		n := calls.Add(1)
		if n < 2 {
			return "", &service.RateLimitError{
				StatusCode: http.StatusTooManyRequests,
				RetryAfter: 50 * time.Millisecond,
				Provider:   "anthropic",
				Message:    "rate limited",
				Underlying: errors.New("upstream 429"),
			}
		}
		return "ok", nil
	}

	got, err := callWithGatewayRetry(context.Background(),
		"anthropic", "claude-test", 0, fn)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if got != "ok" {
		t.Errorf("result = %q, want ok", got)
	}
}

// TestClassifyGatewayError_PreservesUpstreamStatus confirms the helper
// returns the upstream status (429 → 429, 529 → 529) instead of
// collapsing to a generic 502, so client SDKs can detect rate-limit
// conditions correctly.
func TestClassifyGatewayError_PreservesUpstreamStatus(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantType   string
	}{
		{
			name: "429 maps to 429 + rate_limit_error",
			err: &service.RateLimitError{
				StatusCode: 429,
				Provider:   "anthropic",
				Message:    "rate limited",
				Underlying: errors.New("x"),
			},
			wantStatus: 429,
			wantType:   "rate_limit_error",
		},
		{
			name: "529 maps to 529 + overloaded_error",
			err: &service.RateLimitError{
				StatusCode: 529,
				Provider:   "anthropic",
				Message:    "overloaded",
				Underlying: errors.New("x"),
			},
			wantStatus: 529,
			wantType:   "overloaded_error",
		},
		{
			name:       "non-rate-limit error falls to 502",
			err:        errors.New("network error"),
			wantStatus: http.StatusBadGateway,
			wantType:   "server_error",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := classifyGatewayError(tt.err)
			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}
			errBody, ok := body["error"].(map[string]any)
			if !ok {
				t.Fatalf("body has no 'error' object: %v", body)
			}
			if got, _ := errBody["type"].(string); got != tt.wantType {
				t.Errorf("type = %q, want %q", got, tt.wantType)
			}
		})
	}
}

// TestAddGatewayRateLimitHeaders_SetsRetryAfter verifies that the
// Retry-After header is propagated to the gateway response when the
// upstream provided one. RFC 7231 says the value is integer seconds.
func TestAddGatewayRateLimitHeaders_SetsRetryAfter(t *testing.T) {
	w := httptest.NewRecorder()
	addGatewayRateLimitHeaders(w, &service.RateLimitError{
		StatusCode: 429,
		RetryAfter: 7 * time.Second,
		Provider:   "anthropic",
	})
	got := w.Header().Get("Retry-After")
	if got != "7" {
		t.Errorf("Retry-After = %q, want %q", got, "7")
	}
}

// TestAddGatewayRateLimitHeaders_NoRetryAfterIsNoop ensures we don't
// emit a bogus zero header when the upstream didn't tell us how long
// to wait.
func TestAddGatewayRateLimitHeaders_NoRetryAfterIsNoop(t *testing.T) {
	w := httptest.NewRecorder()
	addGatewayRateLimitHeaders(w, &service.RateLimitError{
		StatusCode: 529,
		Provider:   "anthropic",
	})
	if got := w.Header().Get("Retry-After"); got != "" {
		t.Errorf("Retry-After = %q, want empty", got)
	}
}

// TestAddGatewayRateLimitHeaders_NoopOnNonRateLimitError ensures the
// helper never touches headers when the error isn't a rate limit.
func TestAddGatewayRateLimitHeaders_NoopOnNonRateLimitError(t *testing.T) {
	w := httptest.NewRecorder()
	addGatewayRateLimitHeaders(w, errors.New("plain error"))
	if got := w.Header().Get("Retry-After"); got != "" {
		t.Errorf("Retry-After = %q, want empty", got)
	}
}
