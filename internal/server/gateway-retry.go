package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// gatewayRetryAttempts is the number of upstream calls we'll attempt
// before surfacing an error to the gateway client. Default: 1 try +
// 2 retries on 429/529. The opencode-claude-auth reference plugin uses
// the same shape — bounded retries with `retry-after` honoured, capped
// so the client doesn't appear to hang on hour-long quota resets.
const gatewayRetryAttempts = 3

// defaultGatewayRetryAfterCap is the FALLBACK cap when the provider
// has no rate-limit config. It mirrors the org-delegation default (60s)
// so the gateway and agent paths behave identically out of the box —
// before this match, the gateway used a tighter 10s cap and could give
// up on transient 429/529 windows that the agent loop would patiently
// wait through, causing intermittent "rate_limit" errors in opencode
// even though AT's own chat (which uses the agent loop) succeeded
// against the same upstream account.
//
// Per-provider override via LLMConfig.RateLimit.RetryAfterCapMs:
//
//	0   → use this default (60s)
//	-1  → no cap (honour whatever upstream returns)
//	>0  → cap in milliseconds
//
// Operators who want a tighter gateway-side bound (e.g. to keep a
// user-facing CLI snappy) can set retry_after_cap_ms on the provider.
const defaultGatewayRetryAfterCap = 60 * time.Second

// defaultGatewayMinBackoff is the floor for the retry sleep when the
// upstream did NOT return a useful `Retry-After` header. The previous
// value (500ms) was tuned for transient burst limits but proved too
// short in practice: Anthropic 429s with no Retry-After commonly come
// from org/account-level limits whose actual recovery window is 30–60s.
// With a 500ms floor and 3 attempts the gateway burned through retries
// in ~3 seconds and surfaced an error to opencode while the user's
// upstream account was still rate-limited.
//
// 10s × linear (10s, 20s on the 2 in-loop sleeps) = ~30s of total
// backoff across 3 attempts, which empirically clears most Anthropic
// org-level burst limits without keeping the request hung past the
// caller's typical timeout.
const defaultGatewayMinBackoff = 10 * time.Second

// gatewayMinBackoff returns the current floor for the retry sleep.
// Read each call (not init-once) so tests and operators can override
// via the AT_GATEWAY_MIN_BACKOFF_MS env var without rebuilding. The
// var sits at the call site so `t.Setenv` works in unit tests.
func gatewayMinBackoff() time.Duration {
	if v := os.Getenv("AT_GATEWAY_MIN_BACKOFF_MS"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return defaultGatewayMinBackoff
}

// callWithGatewayRetry calls fn() and retries on transient upstream
// errors (HTTP 429 rate_limit, HTTP 529 overloaded — both surfaced as
// *service.RateLimitError by the providers). Honors `Retry-After`
// up to retryAfterCap (per-provider, see ProviderInfo.RetryAfterCap).
//
// fn is called up to gatewayRetryAttempts times. The first non-retryable
// error or first success ends the loop. If every attempt fails with a
// rate-limit error, the LAST error is returned so the caller can map it
// to an HTTP status code.
//
// retryAfterCap semantics:
//
//	0   → use defaultGatewayRetryAfterCap (60s)
//	<0  → no cap (honour whatever upstream returns; typical for paid
//	      Anthropic accounts with multi-minute quota resets)
//	>0  → cap upstream Retry-After to this duration
//
// The sleep happens with the request context, so a client disconnect
// during the wait aborts cleanly without consuming further attempts.
func callWithGatewayRetry[T any](
	ctx context.Context,
	provider, model string,
	retryAfterCap time.Duration,
	fn func(ctx context.Context) (T, error),
) (T, error) {
	if retryAfterCap == 0 {
		retryAfterCap = defaultGatewayRetryAfterCap
	}
	var (
		zero    T
		lastErr error
		result  T
	)
	for attempt := 0; attempt < gatewayRetryAttempts; attempt++ {
		var err error
		result, err = fn(ctx)
		if err == nil {
			return result, nil
		}

		// Only retry typed RateLimitError. Plain transport errors and
		// non-rate-limit upstream errors bubble up immediately so we
		// don't burn retries on, e.g., a 401 from a stale token.
		var rle *service.RateLimitError
		if !errors.As(err, &rle) {
			return zero, err
		}
		lastErr = err

		// Last attempt — don't sleep, just surface the error.
		if attempt == gatewayRetryAttempts-1 {
			break
		}

		sleep := rle.RetryAfter
		if sleep <= 0 {
			// No upstream guidance — backoff linearly: minBackoff,
			// 2×minBackoff, 3×minBackoff. Default is 10s (so 10s and
			// 20s across the 2 in-loop sleeps), tuned to clear typical
			// Anthropic org-level rate limits.
			sleep = gatewayMinBackoff() * time.Duration(attempt+1)
		}
		// retryAfterCap < 0 means "no cap"; honour upstream verbatim.
		if retryAfterCap > 0 && sleep > retryAfterCap {
			slog.Warn("gateway: capping upstream Retry-After",
				"provider", provider, "model", model, "attempt", attempt+1,
				"requested", rle.RetryAfter, "capped_to", retryAfterCap,
				"upstream_status", rle.StatusCode)
			sleep = retryAfterCap
		}

		slog.Warn("gateway: upstream rate-limit, retrying",
			"provider", provider, "model", model, "attempt", attempt+1,
			"sleep", sleep, "upstream_status", rle.StatusCode,
			"retry_after", rle.RetryAfter)

		select {
		case <-ctx.Done():
			// Client gave up; return the last rate-limit error so the
			// caller still sees the upstream signal in logs.
			if lastErr != nil {
				return zero, lastErr
			}
			return zero, ctx.Err()
		case <-time.After(sleep):
		}
	}
	return zero, lastErr
}

// classifyGatewayError returns the HTTP status code and OpenAI-style
// error envelope to send back to the client when an upstream call
// fails. Rate-limit errors are mapped to the upstream status (429/529)
// so the client SDK (opencode, openai-python, etc.) can detect and
// surface them as a normal rate-limit condition rather than a generic
// 502 "provider error".
//
// Returns (httpStatus, errorBody).
func classifyGatewayError(err error) (int, map[string]any) {
	var rle *service.RateLimitError
	if errors.As(err, &rle) {
		// Default to 429 — the OpenAI client SDK and most consumers
		// already know how to handle it. We pass the upstream status
		// through verbatim if we have one, so 529 is preserved as 529
		// (clients that special-case Anthropic's overloaded code can
		// still detect it; everyone else treats 429 as the generic
		// rate-limit).
		status := rle.StatusCode
		if status == 0 {
			status = http.StatusTooManyRequests
		}
		errType := "rate_limit_error"
		if rle.StatusCode == 529 {
			errType = "overloaded_error"
		}
		body := map[string]any{
			"error": map[string]any{
				"message":  fmt.Sprintf("upstream %s rate-limited (status %d): %s", rle.Provider, rle.StatusCode, rle.Message),
				"type":     errType,
				"code":     errType,
				"provider": rle.Provider,
			},
		}
		if rle.RetryAfter > 0 {
			body["error"].(map[string]any)["retry_after_seconds"] = int(rle.RetryAfter.Seconds())
		}
		return status, body
	}

	return http.StatusBadGateway, map[string]any{
		"error": map[string]any{
			"message": fmt.Sprintf("provider error: %v", err),
			"type":    "server_error",
		},
	}
}

// addGatewayRateLimitHeaders writes the standard RFC `Retry-After`
// header on a response so HTTP clients that follow it can honour the
// upstream's pacing without parsing the JSON body.
func addGatewayRateLimitHeaders(w http.ResponseWriter, err error) {
	var rle *service.RateLimitError
	if !errors.As(err, &rle) {
		return
	}
	if rle.RetryAfter > 0 {
		// `Retry-After` is in seconds (integer) per RFC 7231.
		secs := int(rle.RetryAfter.Seconds())
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", fmt.Sprintf("%d", secs))
	}
}
