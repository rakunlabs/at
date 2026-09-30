package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Decisions (POST /gateway/v1/decisions) — System 1 ───

// decisionsRequest is the /v1/systemone request shape plus an AT-qualified
// model. Controls the upstream understands (max_len, lang, min_confidence, …)
// travel in the remaining fields and are forwarded unchanged.
type decisionsRequest struct {
	Model     string         `json:"model"`
	State     any            `json:"state"`
	Questions map[string]any `json:"questions"`
}

// decisionsBodyMaxBytes matches laya-serve's own request cap, so an oversized
// body is refused here rather than after a round-trip.
const decisionsBodyMaxBytes = 2 << 20

// Decisions handles POST /gateway/v1/decisions.
func (s *Server) Decisions(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, decisionsBodyMaxBytes)
	auth, providerKey, actualModel, fullModel, info, ok := s.resolveMediaProvider(w, r)
	if !ok {
		return
	}

	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		gatewayBadRequest(w, fmt.Sprintf("invalid request body: %v", err), "", "")
		return
	}
	var req decisionsRequest
	if encoded, err := json.Marshal(raw); err == nil {
		_ = json.Unmarshal(encoded, &req)
	}
	options := make(map[string]any, len(raw))
	for k, v := range raw {
		switch k {
		case "model", "state", "questions":
		default:
			options[k] = v
		}
	}
	decision := service.DecisionRequest{Model: actualModel, State: req.State, Questions: req.Questions, Options: options}
	if err := service.ValidateDecisionRequest(decision); err != nil {
		param := "questions"
		if req.State == nil {
			param = "state"
		}
		gatewayBadRequest(w, err.Error(), param, "")
		return
	}

	decider, ok := info.provider.(service.DecisionProvider)
	if !ok {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("provider %q does not support decisions; configure a provider of type systemone", providerKey),
				"type":    "invalid_request_error",
				"code":    "unsupported_operation",
			},
		}, http.StatusNotImplemented)
		return
	}

	traceID, sessionID := auditTraceInfo(r)
	requestBody, _ := json.Marshal(raw)
	callStart := time.Now()
	resp, err := decider.Decide(r.Context(), decision)
	latencyMs := time.Since(callStart).Milliseconds()
	if err != nil {
		s.noteProviderError(providerKey, err)
		slog.Error("decision provider call failed", "provider", providerKey, "error", err)
		s.recordUsageAsync(r.Context(), auth, fullModel, service.Usage{}, latencyMs, "error", classifyHTTPError(err), err.Error())
		s.recordLLMCallAsync(r.Context(), llmAuditParams{
			auth: auth, source: "gateway", endpoint: r.URL.Path,
			traceID: traceID, sessionID: sessionID,
			requestBody: requestBody, requestedModel: fullModel, fullModel: fullModel,
			latencyMs: latencyMs, status: "error", errCode: classifyHTTPError(err), errMsg: err.Error(),
			name: "decisions", metadata: map[string]any{"question_count": len(req.Questions)},
		})
		status, body := classifyGatewayError(err)
		addGatewayRateLimitHeaders(w, err)
		httpResponseJSON(w, body, status)
		return
	}

	out := resp.Raw
	if out == nil {
		out = map[string]any{"answers": resp.Answers}
	}
	responseBody, _ := json.Marshal(out)
	w.Header().Set("x-at-model-used", fullModel)
	if costCents := s.estimateGatewayUsageCostCents(r.Context(), providerKey, actualModel, fullModel, resp.Usage); costCents > 0 {
		w.Header().Set("x-at-response-cost-cents", fmt.Sprintf("%.6f", costCents))
	}
	s.recordUsage(r.Context(), auth, fullModel, resp.Usage, latencyMs, "ok", "", "", true)
	s.recordLLMCallAsync(r.Context(), llmAuditParams{
		auth: auth, source: "gateway", endpoint: r.URL.Path,
		traceID: traceID, sessionID: sessionID,
		requestBody: requestBody, responseBody: responseBody,
		requestedModel: fullModel, fullModel: fullModel,
		usage: resp.Usage, latencyMs: latencyMs, status: "ok", finishReason: "stop",
		name: "decisions", metadata: map[string]any{"question_count": len(req.Questions), "upstream_model": resp.Model},
	})
	httpResponseJSON(w, out, http.StatusOK)
}
