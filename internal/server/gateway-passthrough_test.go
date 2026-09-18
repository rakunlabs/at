package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/worldline-go/types"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/common"
)

// ─── Recording spies ───

// Both spies embed their interface: only the recording method matters here, and
// an unimplemented call panics loudly rather than being quietly stubbed.
type recordingCostStore struct {
	service.CostEventStorer

	mu     sync.Mutex
	events []service.CostEvent
}

func (m *recordingCostStore) RecordCostEvent(_ context.Context, e service.CostEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)

	return nil
}

func (m *recordingCostStore) snapshot() []service.CostEvent {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]service.CostEvent(nil), m.events...)
}

type recordingLLMCallStore struct {
	service.LLMCallStorer

	mu    sync.Mutex
	calls []service.LLMCall
}

func (m *recordingLLMCallStore) RecordLLMCall(_ context.Context, c service.LLMCall) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, c)

	return nil
}

func (m *recordingLLMCallStore) snapshot() []service.LLMCall {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]service.LLMCall(nil), m.calls...)
}

// ─── Provider doubles ───

// meteredProxyProvider answers through the same observer seam a real adapter
// uses, so the test exercises the production path rather than a shortcut.
type meteredProxyProvider struct {
	contentType string
	body        string
	status      int
	failWith    error
}

func (p *meteredProxyProvider) Chat(context.Context, string, []service.Message, []service.Tool, *service.ChatOptions) (*service.LLMResponse, error) {
	return nil, nil
}

func (p *meteredProxyProvider) Proxy(w http.ResponseWriter, r *http.Request, _ string) error {
	if p.failWith != nil {
		return p.failWith
	}

	resp := &http.Response{
		StatusCode: p.status,
		Header:     http.Header{"Content-Type": []string{p.contentType}},
		Body:       io.NopCloser(strings.NewReader(p.body)),
	}
	if observe := common.ProxyResponseObserver(r.Context()); observe != nil {
		if err := observe(resp); err != nil {
			return err
		}
	}

	w.Header().Set("Content-Type", p.contentType)
	w.WriteHeader(p.status)
	_, _ = io.Copy(w, resp.Body)

	return resp.Body.Close()
}

// silentProxyProvider ignores the observer seam entirely, standing in for an
// adapter that has not adopted it.
type silentProxyProvider struct{}

func (p *silentProxyProvider) Chat(context.Context, string, []service.Message, []service.Tool, *service.ChatOptions) (*service.LLMResponse, error) {
	return nil, nil
}

func (p *silentProxyProvider) Proxy(w http.ResponseWriter, _ *http.Request, _ string) error {
	w.WriteHeader(http.StatusOK)

	return nil
}

// ─── Harness ───

func meteredProxyServer(t *testing.T, provider service.LLMProvider, tokenID string) (*Server, *recordingCostStore, *recordingLLMCallStore) {
	t.Helper()

	costs := &recordingCostStore{}
	calls := &recordingLLMCallStore{}
	s := newProxyTestServer("anthropic", provider, gatewayTestToken("test-token", service.APIToken{
		ID:                   tokenID,
		Name:                 "passthrough",
		AllowedProvidersMode: service.AccessModeList,
		AllowedProviders:     types.Slice[string]{"anthropic"},
		AllowedModelsMode:    service.AccessModeList,
		AllowedModels:        types.Slice[string]{"anthropic/claude-3-5-sonnet"},
	}))
	s.costEventStore = costs
	s.llmCallStore = calls

	return s, costs, calls
}

func doProxy(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/gateway/v1/providers/anthropic/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "test-token")
	req.SetPathValue("provider", "anthropic")
	req.SetPathValue("*", "v1/messages")
	rec := httptest.NewRecorder()

	s.ProxyRequest(rec, req)

	return rec
}

// waitForRecords lets the fire-and-forget recorders land. They are deliberately
// asynchronous so they cannot slow or fail a proxied response.
func waitForRecords(t *testing.T, costs *recordingCostStore, calls *recordingLLMCallStore, wantCosts, wantCalls int) ([]service.CostEvent, []service.LLMCall) {
	t.Helper()

	for range 200 {
		c, l := costs.snapshot(), calls.snapshot()
		if len(c) >= wantCosts && len(l) >= wantCalls {
			return c, l
		}
		time.Sleep(5 * time.Millisecond)
	}

	c, l := costs.snapshot(), calls.snapshot()
	t.Fatalf("records did not land: %d cost events (want %d), %d observations (want %d)", len(c), wantCosts, len(l), wantCalls)

	return c, l
}

// ─── Tests ───

func TestPassthroughRecordsParsedUsage(t *testing.T) {
	provider := &meteredProxyProvider{
		contentType: "application/json",
		status:      http.StatusOK,
		body:        `{"id":"msg_1","usage":{"input_tokens":120,"output_tokens":45}}`,
	}
	s, costs, calls := meteredProxyServer(t, provider, "tok_parsed")

	rec := doProxy(t, s, `{"model":"claude-3-5-sonnet"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "msg_1") {
		t.Fatalf("upstream body was not relayed: %s", rec.Body)
	}

	events, observations := waitForRecords(t, costs, calls, 1, 1)

	if events[0].InputTokens != 120 || events[0].OutputTokens != 45 {
		t.Fatalf("cost event usage %+v", events[0])
	}
	if events[0].Provider != "anthropic" || events[0].Model != "claude-3-5-sonnet" {
		t.Fatalf("cost event attribution %+v", events[0])
	}
	if events[0].AgentID != "gateway:tok_parsed" {
		t.Fatalf("cost event not attributed to the token: %q", events[0].AgentID)
	}

	obs := observations[0]
	if obs.Source != "gateway_passthrough" {
		t.Fatalf("observation source %q", obs.Source)
	}
	if obs.Metadata["usage_source"] != service.UsageSourceParsed {
		t.Fatalf("usage_source %v want parsed", obs.Metadata["usage_source"])
	}
	if obs.Status != "ok" {
		t.Fatalf("observation status %q", obs.Status)
	}
}

// The case the change exists for: an opaque response still produces a row, with
// zero counts and an explicit marker rather than silence.
func TestPassthroughRecordsUnreadableUsage(t *testing.T) {
	provider := &meteredProxyProvider{
		contentType: "application/octet-stream",
		status:      http.StatusOK,
		body:        "\x00\x01binary-file",
	}
	s, costs, calls := meteredProxyServer(t, provider, "tok_opaque")

	if rec := doProxy(t, s, `{"model":"claude-3-5-sonnet"}`); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}

	events, observations := waitForRecords(t, costs, calls, 1, 1)

	if events[0].InputTokens != 0 || events[0].OutputTokens != 0 {
		t.Fatalf("counts must be zero, never estimated: %+v", events[0])
	}
	if events[0].CostCents != 0 {
		t.Fatalf("cost must be zero when usage is unknown: %+v", events[0])
	}
	if observations[0].Metadata["usage_source"] != service.UsageSourceUnavailable {
		t.Fatalf("usage_source %v want unavailable", observations[0].Metadata["usage_source"])
	}
}

func TestPassthroughRecordsUpstreamError(t *testing.T) {
	provider := &meteredProxyProvider{
		contentType: "application/json",
		status:      http.StatusTooManyRequests,
		body:        `{"type":"error","error":{"type":"rate_limit_error"}}`,
	}
	s, costs, calls := meteredProxyServer(t, provider, "tok_429")

	rec := doProxy(t, s, `{"model":"claude-3-5-sonnet"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("upstream status must reach the client unchanged, got %d", rec.Code)
	}

	events, observations := waitForRecords(t, costs, calls, 1, 1)

	if events[0].Status != "error" {
		t.Fatalf("cost event status %q want error", events[0].Status)
	}
	if observations[0].Status != "error" || observations[0].ErrorCode != "rate_limit" {
		t.Fatalf("observation %q/%q want error/rate_limit", observations[0].Status, observations[0].ErrorCode)
	}
	if observations[0].Metadata["upstream_status"] == nil {
		t.Fatal("observation should carry the upstream status")
	}
}

func TestPassthroughRecordsTransportFailureOnce(t *testing.T) {
	provider := &meteredProxyProvider{failWith: errors.New("dial upstream: connection refused")}
	s, costs, calls := meteredProxyServer(t, provider, "tok_fail")

	rec := doProxy(t, s, `{"model":"claude-3-5-sonnet"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d want 502", rec.Code)
	}

	events, observations := waitForRecords(t, costs, calls, 1, 1)

	if len(events) != 1 || len(observations) != 1 {
		t.Fatalf("a failed passthrough must record exactly one row each: %d/%d", len(events), len(observations))
	}
	if events[0].Status != "error" {
		t.Fatalf("cost event status %q", events[0].Status)
	}
	if !strings.Contains(observations[0].ErrorMessage, "connection refused") {
		t.Fatalf("observation error %q", observations[0].ErrorMessage)
	}
}

// An adapter that never adopted the observer seam must still be metered, rather
// than silently reverting to the old invisible behaviour.
func TestPassthroughRecordsWhenAdapterIgnoresObserver(t *testing.T) {
	s, costs, calls := meteredProxyServer(t, &silentProxyProvider{}, "tok_silent")

	if rec := doProxy(t, s, `{"model":"claude-3-5-sonnet"}`); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}

	events, observations := waitForRecords(t, costs, calls, 1, 1)

	if observations[0].Metadata["usage_source"] != service.UsageSourceUnavailable {
		t.Fatalf("usage_source %v want unavailable", observations[0].Metadata["usage_source"])
	}
	if events[0].InputTokens != 0 {
		t.Fatalf("counts %+v", events[0])
	}
}

// A token with no database ID is a config token; it is untracked on every other
// gateway surface and must stay untracked here.
func TestPassthroughSkipsConfigTokens(t *testing.T) {
	provider := &meteredProxyProvider{
		contentType: "application/json",
		status:      http.StatusOK,
		body:        `{"usage":{"input_tokens":5,"output_tokens":5}}`,
	}
	s, costs, calls := meteredProxyServer(t, provider, "")

	if rec := doProxy(t, s, `{"model":"claude-3-5-sonnet"}`); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}

	// The observation is still recorded (it is not token-gated); the cost event
	// is not, matching recordUsageAsync on every other surface.
	waitForRecords(t, costs, calls, 0, 1)
	if got := costs.snapshot(); len(got) != 0 {
		t.Fatalf("config token produced %d cost events, want 0", len(got))
	}
}
