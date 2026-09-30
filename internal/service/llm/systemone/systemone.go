// Package systemone implements a provider for System 1 decision services that
// speak the `/v1/systemone` protocol: TypeSafe Jev and self-hosted Laya
// (`laya-serve`). Such a service never generates text — it answers typed
// questions (choice / score / noul) about a state with calibrated
// probabilities — so the provider implements service.DecisionProvider and
// refuses chat.
//
// The service is external by design: AT only needs its base URL and optional
// bearer key, so a decision model running anywhere (a GPU box, a sidecar, the
// hosted Jev API) can be attached without shipping Python into AT.
package systemone

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/common"
	"github.com/rakunlabs/at/internal/service/ratelimit"
)

const (
	defaultBaseURL = "http://localhost:8000"
	// AutoModel lets the upstream router choose a checkpoint.
	AutoModel         = "auto"
	decisionPath      = "/v1/systemone"
	responseMaxBytes  = 16 << 20
	errorBodyMaxBytes = 4 << 10
	defaultTimeout    = 2 * time.Minute
)

// KnownModels are the checkpoint names laya-serve accepts. Jev ignores an
// unknown model and routes itself, so the list is safe for both.
var KnownModels = []string{AutoModel, "english", "multilingual", "typed-decisions"}

// reservedBodyKeys cannot be overridden through DecisionRequest.Options.
// laya-serve refuses hook arguments with 422; dropping them here keeps a
// workflow typo from turning into a failed call.
var reservedBodyKeys = map[string]bool{
	"state": true, "questions": true, "model": true,
	"hooks": true, "on_predict_start": true, "on_predict_end": true, "hooks_raise": true, "hooks_timeout": true,
}

// Provider talks to one System 1 endpoint.
type Provider struct {
	apiKey     string
	model      string
	baseURL    string
	headers    map[string]string
	httpClient *http.Client
	limiter    *ratelimit.Limiter
}

// Option mutates a Provider during construction.
type Option func(*Provider)

// WithRateLimiter attaches a per-provider rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option {
	return func(p *Provider) { p.limiter = l }
}

// WithHTTPClient replaces the HTTP client (tests).
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// New creates a System 1 provider. baseURL is the service root (e.g.
// http://laya:8000); a trailing /v1/systemone is tolerated. apiKey is optional
// and sent as a bearer token.
func New(apiKey, model, baseURL, proxy string, insecureSkipVerify bool, headers map[string]string, opts ...Option) (*Provider, error) {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	baseURL = strings.TrimSpace(baseURL)
	baseURL = strings.TrimSuffix(baseURL, "/")
	baseURL = strings.TrimSuffix(baseURL, decisionPath)
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("systemone: base_url must be an absolute http(s) URL, got %q", baseURL)
	}
	if model == "" {
		model = AutoModel
	}

	httpClient := &http.Client{Timeout: defaultTimeout}
	if proxy != "" || insecureSkipVerify {
		t := http.DefaultTransport.(*http.Transport).Clone()
		if proxy != "" {
			pu, err := url.Parse(proxy)
			if err != nil {
				return nil, fmt.Errorf("parse proxy URL: %w", err)
			}
			t.Proxy = http.ProxyURL(pu)
		}
		if insecureSkipVerify {
			t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // operator opt-in
		}
		httpClient.Transport = t
	}

	p := &Provider{
		apiKey:     apiKey,
		model:      model,
		baseURL:    baseURL,
		headers:    headers,
		httpClient: httpClient,
	}
	for _, o := range opts {
		o(p)
	}
	return p, nil
}

// Chat is not supported: a decision model does not generate text. The error
// is ErrUnsupportedOperation so the gateway answers 501 instead of 502.
func (p *Provider) Chat(context.Context, string, []service.Message, []service.Tool, *service.ChatOptions) (*service.LLMResponse, error) {
	return nil, fmt.Errorf("systemone providers answer decisions only; use /gateway/v1/decisions: %w", service.ErrUnsupportedOperation)
}

// Decide sends one request to POST /v1/systemone.
func (p *Provider) Decide(ctx context.Context, req service.DecisionRequest) (*service.DecisionResponse, error) {
	if err := service.ValidateDecisionRequest(req); err != nil {
		return nil, &service.UpstreamError{Provider: "systemone", StatusCode: http.StatusBadRequest, Message: err.Error(), Underlying: err}
	}
	model := req.Model
	if model == "" {
		model = p.model
	}

	body := make(map[string]any, len(req.Options)+3)
	for k, v := range req.Options {
		if !reservedBodyKeys[k] && v != nil {
			body[k] = v
		}
	}
	body["state"] = req.State
	body["questions"] = req.Questions
	// "auto" is AT's name for "let the upstream route"; laya-serve does the
	// same for an absent model, and Jev would reject an unknown one.
	if model != "" && model != AutoModel {
		body["model"] = model
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("systemone: marshal request: %w", err)
	}

	release, err := p.limiter.Acquire(ctx, estimateTokens(data))
	if err != nil {
		return nil, err
	}
	defer release()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+decisionPath, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("systemone: build request: %w", err)
	}
	for k, v := range p.headers {
		httpReq.Header.Set(k, v)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// A decision service that is down must look like an outage so
		// fallback chains and cooldown treat it the way they treat any other
		// unreachable provider.
		return nil, &service.UpstreamError{Provider: "systemone", StatusCode: http.StatusBadGateway, Message: "decision service unreachable: " + err.Error(), Underlying: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyMaxBytes))
		return nil, upstreamError(resp, raw)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, responseMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("systemone: read response: %w", err)
	}
	if len(raw) > responseMaxBytes {
		return nil, fmt.Errorf("systemone: response exceeds %d bytes", responseMaxBytes)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, &service.UpstreamError{Provider: "systemone", StatusCode: http.StatusBadGateway, Message: "decision service returned a non-JSON body", Underlying: err}
	}
	answers, ok := parsed["answers"].(map[string]any)
	if !ok {
		return nil, &service.UpstreamError{Provider: "systemone", StatusCode: http.StatusBadGateway, Message: "decision service response has no answers object"}
	}

	out := &service.DecisionResponse{Answers: answers, Raw: parsed}
	if m, ok := parsed["model"].(string); ok && m != "" {
		out.Model = m
	} else {
		out.Model = model
	}
	if usage, ok := parsed["usage"].(map[string]any); ok {
		out.Usage.PromptTokens = intField(usage, "input_tokens")
		out.Usage.CompletionTokens = intField(usage, "output_tokens")
		out.Usage.TotalTokens = out.Usage.PromptTokens + out.Usage.CompletionTokens
	}
	return out, nil
}

func upstreamError(resp *http.Response, raw []byte) error {
	message := strings.TrimSpace(string(raw))
	var envelope map[string]any
	if json.Unmarshal(raw, &envelope) == nil {
		// FastAPI reports {"detail": "..."} or {"detail": [{msg,...}]}; Jev
		// uses {"error": {"message": ...}}.
		switch d := envelope["detail"].(type) {
		case string:
			message = d
		case []any:
			parts := make([]string, 0, len(d))
			for _, item := range d {
				if m, ok := item.(map[string]any); ok {
					if msg, ok := m["msg"].(string); ok {
						parts = append(parts, msg)
					}
				}
			}
			if len(parts) > 0 {
				message = strings.Join(parts, "; ")
			}
		}
		if e, ok := envelope["error"].(map[string]any); ok {
			if msg, ok := e["message"].(string); ok && msg != "" {
				message = msg
			}
		}
	}
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	underlying := fmt.Errorf("systemone API error (status %d): %s", resp.StatusCode, message)
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		// laya-serve answers 503 + Retry-After when its admission queue is
		// full; that is a capacity signal, not an outage.
		return &service.RateLimitError{
			StatusCode: resp.StatusCode,
			RetryAfter: common.ParseRetryAfter(resp.Header),
			Provider:   "systemone",
			Message:    message,
			Underlying: underlying,
		}
	}
	status := resp.StatusCode
	code := ""
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		// The caller authenticated to AT correctly; a rejected upstream key is
		// a provider configuration problem.
		status = http.StatusBadGateway
		code = "upstream_auth_failed"
		message = "decision service rejected the configured API key: " + message
	case http.StatusUnprocessableEntity:
		// Question validation errors name the question; present them as the
		// caller's bad request.
		status = http.StatusBadRequest
		code = "invalid_question"
	case http.StatusRequestEntityTooLarge:
		status = http.StatusBadRequest
		code = "request_too_large"
	}
	return &service.UpstreamError{Provider: "systemone", StatusCode: status, Code: code, Message: message, Underlying: underlying}
}

func intField(m map[string]any, key string) int {
	if v, ok := m[key].(float64); ok && v > 0 {
		return int(v)
	}
	return 0
}

// estimateTokens is only the limiter weight; it is never recorded as usage.
func estimateTokens(body []byte) int {
	return max(1, len(body)/4)
}
