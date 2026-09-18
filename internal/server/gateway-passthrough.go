package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/common"
)

// proxyCallRecord carries what a completed passthrough request reports.
type proxyCallRecord struct {
	providerKey string
	fullModel   string
	proxyPath   string
	traceID     string
	sessionID   string
	latencyMs   int64
	obs         service.ProxyObservation
	err         error
}

// recordProxyCall writes the cost event and observation for one native
// passthrough request.
//
// A row is written even when no usage could be read. Recording nothing — the
// previous behaviour — is what made the accounting gap invisible; a zero-token
// row with an explicit usage_source keeps the request visible in Traces, counts
// it in the request-side of token limits, and makes the coverage gap queryable
// instead of silent. Token counts are never estimated here: an estimate written
// into the same column real counts use would corrupt budgets and the Usage
// dashboard. The estimate belongs to the rate limiter, which is a throttle.
func (s *Server) recordProxyCall(ctx context.Context, auth *authResult, rec proxyCallRecord) {
	status := "ok"
	errCode := ""
	errMsg := ""

	switch {
	case rec.err != nil:
		status = "error"
		errCode = classifyHTTPError(rec.err)
		errMsg = rec.err.Error()
	case rec.obs.StatusCode >= 400:
		status = "error"
		errCode = classifyProxyStatus(rec.obs.StatusCode)
		errMsg = fmt.Sprintf("upstream returned %d", rec.obs.StatusCode)
	}

	usageSource := rec.obs.UsageSource
	if usageSource == "" {
		usageSource = service.UsageSourceUnavailable
	}

	// Passthrough sees the same upstream quota as the OpenAI-shape endpoint, so
	// it feeds the same availability registry.
	if rec.obs.StatusCode == http.StatusTooManyRequests {
		s.cooldown.Enter(rec.providerKey, passthroughRetryAfter(rec.obs.Header), "passthrough received 429")
	} else if rec.obs.StatusCode > 0 {
		s.noteProviderResponse(rec.providerKey, rec.obs.Header)
	}

	// recordEmpty: a successful passthrough whose usage could not be read is
	// exactly the case this change exists to make visible, so it must not take
	// the nothing-to-record shortcut.
	s.recordUsage(ctx, auth, rec.fullModel, rec.obs.Usage, rec.latencyMs, status, errCode, errMsg, true)

	metadata := map[string]any{
		"passthrough":  true,
		"proxy_path":   rec.proxyPath,
		"usage_source": usageSource,
	}
	if rec.obs.StatusCode > 0 {
		metadata["upstream_status"] = rec.obs.StatusCode
	}

	s.recordLLMCallAsync(ctx, llmAuditParams{
		auth:           auth,
		source:         "gateway_passthrough",
		endpoint:       "/gateway/v1/providers/" + rec.providerKey + rec.proxyPath,
		traceID:        rec.traceID,
		sessionID:      rec.sessionID,
		name:           "passthrough",
		metadata:       metadata,
		requestedModel: rec.fullModel,
		fullModel:      rec.fullModel,
		usage:          rec.obs.Usage,
		latencyMs:      rec.latencyMs,
		status:         status,
		errCode:        errCode,
		errMsg:         errMsg,
	})
}

// passthroughRetryAfter derives a cooldown from a 429's headers, preferring the
// explicit Retry-After and falling back to the bucket reset it reported.
func passthroughRetryAfter(h http.Header) time.Duration {
	if d := common.ParseRetryAfter(h); d > 0 {
		return d
	}
	if snap := parseRateLimitHeaders(h); snap.ResetIn > 0 {
		return snap.ResetIn
	}

	return providerCooldownDefault
}

// classifyProxyStatus maps an upstream status to the same error vocabulary the
// OpenAI-shape endpoint records, so the two are filterable together.
func classifyProxyStatus(code int) string {
	switch {
	case code == http.StatusTooManyRequests:
		return "rate_limit"
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return "auth"
	case code == http.StatusRequestTimeout:
		return "timeout"
	case code >= 500:
		return "upstream"
	case code >= 400:
		return "invalid_request"
	}

	return ""
}
