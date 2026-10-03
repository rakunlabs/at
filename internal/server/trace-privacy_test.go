package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

type tracePrivacyFakeStore struct {
	service.ProviderStorer
	mu       sync.Mutex
	policy   service.TracePrivacyPolicy
	loads    int
	applied  []string
	rules    []service.TracePrivacyRule
	settings service.TracePrivacySettings
}

func (f *tracePrivacyFakeStore) LoadTracePrivacyPolicy(context.Context) (service.TracePrivacyPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loads++
	return f.policy, nil
}
func (f *tracePrivacyFakeStore) ListTracePrivacyRules(context.Context) ([]service.TracePrivacyRule, error) {
	return f.rules, nil
}
func (f *tracePrivacyFakeStore) SaveTracePrivacyRule(_ context.Context, r service.TracePrivacyRule) (*service.TracePrivacyRule, error) {
	r, err := service.NormalizeTracePrivacyRule(r)
	if err != nil {
		return nil, err
	}
	if r.ID == "" {
		r.ID = "new"
	}
	return &r, nil
}
func (f *tracePrivacyFakeStore) DeleteTracePrivacyRule(_ context.Context, id string) error {
	if id != "known" {
		return service.ErrTracePrivacyRuleNotFound
	}
	return nil
}
func (f *tracePrivacyFakeStore) ApplyTracePrivacyRule(_ context.Context, id string, dry bool) (*service.TracePrivacyApplyResult, error) {
	return &service.TracePrivacyApplyResult{DryRun: dry, Action: "skip", Traces: 2, Observations: 5}, nil
}
func (f *tracePrivacyFakeStore) GetTracePrivacySettings(context.Context) (service.TracePrivacySettings, error) {
	return f.settings, nil
}
func (f *tracePrivacyFakeStore) SaveTracePrivacySettings(_ context.Context, s service.TracePrivacySettings) (service.TracePrivacySettings, error) {
	f.settings = s
	return s, nil
}
func (f *tracePrivacyFakeStore) ApplyTracePrivacyToTrace(_ context.Context, ws, trace, action string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, ws+"/"+trace+"/"+action)
	return nil, nil
}
func (f *tracePrivacyFakeStore) appliedSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.applied...)
}

func tracePrivacyTestServer(t *testing.T, rules ...service.TracePrivacyRule) (*Server, *tracePrivacyFakeStore, *fakeLLMCallStore) {
	t.Helper()
	store := &tracePrivacyFakeStore{policy: service.TracePrivacyPolicy{Rules: rules}}
	obs := &fakeLLMCallStore{}
	s := &Server{store: store, llmCallStore: obs, featureStore: &fakeFeatureStore{key: service.FeatureLLMAudit, enabled: true}}
	return s, store, obs
}

func gatewayAuth(tokenID, workspace string) *authResult {
	return &authResult{token: &service.APIToken{ID: tokenID, WorkspaceID: workspace}}
}

func TestTracePrivacySkipAndRedact(t *testing.T) {
	s, _, obs := tracePrivacyTestServer(t,
		service.TracePrivacyRule{ID: "skip-token", Enabled: true, Action: service.TracePrivacySkip, TokenID: "secret-token"},
		service.TracePrivacyRule{ID: "redact-model", Enabled: true, Action: service.TracePrivacyRedact, Model: "gpt-*"},
	)
	ctx := t.Context()
	s.recordLLMCallAsync(ctx, llmAuditParams{auth: gatewayAuth("secret-token", "ws"), source: "gateway", traceID: "t-skip", fullModel: "openai/claude", requestBody: []byte("private"), usage: service.Usage{PromptTokens: 5}})
	s.recordLLMCallAsync(ctx, llmAuditParams{auth: gatewayAuth("other", "ws"), source: "gateway", traceID: "t-redact", fullModel: "openai/gpt-5", requestBody: []byte("private-prompt"), responseBody: []byte("private-answer"), usage: service.Usage{PromptTokens: 7},
		trace: &service.TraceUpdate{Name: "n", Input: "private-in", Output: "private-out"}})
	s.recordLLMCallAsync(ctx, llmAuditParams{auth: gatewayAuth("other", "ws"), source: "gateway", traceID: "t-keep", fullModel: "anthropic/claude", requestBody: []byte("kept")})

	got := waitForObservations(t, obs, 2)
	time.Sleep(50 * time.Millisecond)
	got = obs.snapshot()
	if len(got) != 2 {
		t.Fatalf("skip rule did not suppress the write: %v", obsNames(got))
	}
	byTrace := map[string]service.LLMCall{}
	for _, c := range got {
		byTrace[c.TraceID] = c
	}
	if _, ok := byTrace["t-skip"]; ok {
		t.Fatal("skipped trace recorded")
	}
	red := byTrace["t-redact"]
	if red.RequestBody != "" || red.ResponseBody != "" || red.Metadata["redacted"] != true || red.InputTokens != 7 {
		t.Fatalf("redacted observation kept content or lost metrics: %+v", red)
	}
	if red.Trace == nil || red.Trace.Input != "" || red.Trace.Output != "" || red.Trace.Name != "n" {
		t.Fatalf("trace attributes not redacted: %+v", red.Trace)
	}
	if byTrace["t-keep"].RequestBody != "kept" {
		t.Fatal("unmatched observation lost its body")
	}
}

// A trace is the unit of privacy: once its generation matches a model rule,
// the tool observations and run span of that trace (which carry no model) are
// suppressed too, and anything recorded before the match is cleaned up.
func TestTracePrivacyPropagatesAcrossTrace(t *testing.T) {
	s, store, obs := tracePrivacyTestServer(t,
		service.TracePrivacyRule{ID: "m", Enabled: true, Action: service.TracePrivacySkip, Model: "secret-*"})
	ctx := t.Context()
	// Recorded before the trace is known to be private (e.g. an earlier tool).
	s.recordLLMCallAsync(ctx, llmAuditParams{auth: gatewayAuth("tok", "ws"), traceID: "agent-run", obsType: service.ObservationTool, name: "early", input: "x"})
	waitForObservations(t, obs, 1)
	s.recordLLMCallAsync(ctx, llmAuditParams{auth: gatewayAuth("tok", "ws"), traceID: "agent-run", fullModel: "p/secret-model"})
	s.recordLLMCallAsync(ctx, llmAuditParams{auth: gatewayAuth("tok", "ws"), traceID: "agent-run", obsType: service.ObservationTool, name: "after", input: "private"})
	s.recordLLMCallAsync(ctx, llmAuditParams{auth: gatewayAuth("tok", "ws"), traceID: "agent-run", obsType: service.ObservationAgent, name: "root"})
	// A different trace is unaffected.
	s.recordLLMCallAsync(ctx, llmAuditParams{auth: gatewayAuth("tok", "ws"), traceID: "other", obsType: service.ObservationTool, name: "other"})
	waitForObservations(t, obs, 2)
	time.Sleep(50 * time.Millisecond)
	if names := obsNames(obs.snapshot()); len(names) != 2 {
		t.Fatalf("suppressed trace kept observations: %v", names)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(store.appliedSnapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := store.appliedSnapshot(); len(got) != 1 || got[0] != "ws/agent-run/skip" {
		t.Fatalf("retroactive cleanup: %v", got)
	}
}

func TestTracePrivacyPolicyCache(t *testing.T) {
	s, store, _ := tracePrivacyTestServer(t)
	for range 5 {
		s.tracePrivacyPolicy(t.Context())
	}
	if store.loads != 1 {
		t.Fatalf("policy loaded %d times", store.loads)
	}
	s.invalidateTracePrivacy()
	s.tracePrivacyPolicy(t.Context())
	if store.loads != 2 {
		t.Fatal("invalidation did not reload")
	}
}

func TestTracePrivacyRulesAPI(t *testing.T) {
	s, store, _ := tracePrivacyTestServer(t)
	store.rules = []service.TracePrivacyRule{{ID: "a"}}
	ctx := service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{WorkspaceID: "ws", UserID: "admin", Grants: service.WorkspaceRoleGrants("admin")})
	do := func(h http.HandlerFunc, method, target, body string, id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, strings.NewReader(body)).WithContext(ctx)
		if id != "" {
			r.SetPathValue("id", id)
		}
		w := httptest.NewRecorder()
		h(w, r)
		return w
	}
	if w := do(s.ListTracePrivacyRulesAPI, "GET", "/", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"a"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if w := do(s.CreateTracePrivacyRuleAPI, "POST", "/", `{"action":"hide"}`, ""); w.Code != 400 {
		t.Fatalf("invalid rule: %d", w.Code)
	}
	w := do(s.CreateTracePrivacyRuleAPI, "POST", "/", `{"model":"gpt-*","enabled":true}`, "")
	var created service.TracePrivacyRule
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if w.Code != 201 || created.Action != service.TracePrivacySkip {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if w := do(s.DeleteTracePrivacyRuleAPI, "DELETE", "/", "", "missing"); w.Code != 404 {
		t.Fatalf("delete unknown: %d", w.Code)
	}
	if w := do(s.ApplyTracePrivacyRuleAPI, "POST", "/?dry_run=true", "", "known"); w.Code != 200 || !strings.Contains(w.Body.String(), `"dry_run":true`) {
		t.Fatalf("dry run: %d %s", w.Code, w.Body)
	}
}
