package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/ada"

	"github.com/rakunlabs/at/internal/nativeauth"
	"github.com/rakunlabs/at/internal/nativeauth/nativeauthtest"
	"github.com/rakunlabs/at/internal/service"
)

func TestParseTraceQuery(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
		check   func(service.TraceQuery) bool
	}{
		{"defaults", "", false, func(q service.TraceQuery) bool { return q.Desc && q.Sort == "" }},
		{"lists", "model=a,b&model=c&tag=x", false, func(q service.TraceQuery) bool { return len(q.Models) == 3 && q.Tags[0] == "x" }},
		{"bounds", "min_latency_ms=2000&max_cost_cents=1.5&sort=cost&order=asc", false, func(q service.TraceQuery) bool {
			return *q.MinLatencyMs == 2000 && *q.MaxCostCents == 1.5 && q.Sort == "cost" && !q.Desc
		}},
		{"unknown filter", "modle=a", true, nil},
		{"bad sort", "sort=name", true, nil},
		{"bad status", "status=warn", true, nil},
		{"score bound without name", "min_score=1", true, nil},
		{"bad time", "from=yesterday", true, nil},
		{"negative latency", "min_latency_ms=-1", true, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/traces?"+tt.raw, nil)
			q, err := parseTraceQuery(r.URL.Query())
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.check != nil && !tt.check(q) {
				t.Fatalf("unexpected query: %+v", q)
			}
		})
	}
}

func TestTraceIOFromBodies(t *testing.T) {
	tests := []struct {
		name        string
		req, resp   string
		input, outp string
	}{
		{"openai", `{"messages":[{"role":"system","content":"s"},{"role":"user","content":"first"},{"role":"assistant","content":"a"},{"role":"user","content":[{"type":"text","text":"second"}]}]}`,
			`{"choices":[{"message":{"role":"assistant","content":"answer"}}]}`, "second", "answer"},
		{"anthropic", `{"messages":[{"role":"user","content":[{"type":"tool_result","content":"x"},{"type":"text","text":"hi"}]}]}`,
			`{"content":[{"type":"text","text":"hello"}]}`, "hi", "hello"},
		{"responses", `{"input":"plain question"}`, `{"output":[{"type":"message","content":[{"type":"output_text","text":"resp"}]}]}`, "plain question", "resp"},
		{"canonical loop", `{"messages":[{"role":"user","content":"loop"}]}`, `{"Content":"from loop"}`, "loop", "from loop"},
		{"opaque", `not json`, `{}`, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, out := traceIOFromBodies([]byte(tt.req), []byte(tt.resp))
			if in != tt.input || out != tt.outp {
				t.Fatalf("got %q / %q", in, out)
			}
		})
	}
}

// Gateway header labels name and tag a root generation's trace, and a
// generation recorded under a run span joins that span rather than becoming a
// root of its own.
func TestRecorderTraceLabelsAndParent(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest(http.MethodPost, "/gateway/v1/chat/completions", nil)
	r.Header.Set("x-at-trace-name", "support-bot")
	r.Header.Set("x-at-tags", "prod, beta ,prod")
	r.Header.Set("x-at-environment", "production")
	r.Header.Set("x-at-user", "customer-42")
	r = withTraceLabels(r)
	call := s.buildLLMCall(r.Context(), llmAuditParams{
		source: "gateway", fullModel: "openai/gpt-5", latencyMs: 1500,
		requestBody:  []byte(`{"messages":[{"role":"user","content":"Where is my order?"}]}`),
		responseBody: []byte(`{"choices":[{"message":{"role":"assistant","content":"Shipped."}}]}`),
	})
	if call.Trace == nil || call.Trace.Name != "support-bot" || call.Trace.EndUser != "customer-42" || call.Trace.Input != "Where is my order?" || call.Trace.Output != "Shipped." {
		t.Fatalf("trace update: %+v", call.Trace)
	}
	if call.Environment != "production" || len(call.Trace.Tags) != 3 {
		t.Fatalf("labels: env=%q tags=%v", call.Environment, call.Trace.Tags)
	}
	started, _ := time.Parse(time.RFC3339Nano, call.StartedAt)
	ended, _ := time.Parse(time.RFC3339Nano, call.EndedAt)
	if ended.Sub(started) != 1500*time.Millisecond {
		t.Fatalf("timing: %s..%s", call.StartedAt, call.EndedAt)
	}

	ctx, tp := service.WithTraceParent(context.Background(), "trace-1", "span-1", "sess-1")
	nested := s.buildLLMCall(ctx, llmAuditParams{source: "workflow", fullModel: "openai/gpt-5"})
	if nested.TraceID != "trace-1" || nested.ParentObservationID != "span-1" || nested.SessionID != "sess-1" || nested.Trace != nil || !tp.Used() {
		t.Fatalf("nested: %+v", nested)
	}
}

// The trace explorer API is admitted on traces.read in the selected
// workspace, and a gateway token may only score traces it produced.
func TestTraceExplorerAPIPostgres(t *testing.T) {
	f := newMachineFixture(t)
	actor, _ := service.AccessPrincipalFromContext(f.ctx)
	if _, err := f.store.SetAuthUserPassword(t.Context(), actor.UserID, nativeauthtest.PasswordHash, nil); err != nil {
		t.Fatal(err)
	}
	a, err := nativeauth.New(nativeauthtest.Config(), f.store)
	if err != nil {
		t.Fatal(err)
	}
	f.s.nativeAuth = a
	f.s.llmCallStore = f.store
	f.s.config.BasePath = "/at"
	mux := ada.New()
	a.Register(mux, "/at")
	api := mux.Group("/at/api")
	api.Use(f.s.workspaceBusinessAuthentication())
	api.GET("/v1/traces", f.s.ListTracesAPI)
	api.GET("/v1/traces/{id}", f.s.GetTraceAPI)
	api.POST("/v1/traces/{id}/scores", f.s.CreateTraceScoreAPI)
	api.PUT("/v1/traces/{id}/bookmark", f.s.SetTraceBookmarkAPI)
	api.POST("/v1/api-tokens", f.s.CreateAPITokenAPI)
	cookie := nativeauthtest.LoginCookie(t, mux, "machine-admin")
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/at/api/v1"+path, strings.NewReader(body))
		r.AddCookie(cookie)
		r.Header.Set("Origin", a.Origin())
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-AT-Workspace-ID", f.workspace)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}

	createToken := func(name string) createTokenResponse {
		t.Helper()
		w := call(http.MethodPost, "/api-tokens", `{"name":"`+name+`"}`)
		var resp createTokenResponse
		if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &resp) != nil {
			t.Fatalf("create token: %d %s", w.Code, w.Body.String())
		}
		return resp
	}
	client, other := createToken("client"), createToken("other")
	secret, otherSecret := client.Token, other.Token
	token := client.Info
	if err := f.store.RecordLLMCall(f.ctx, service.LLMCall{TraceID: "trace-gw", TokenID: token.ID, Source: "gateway", Model: "gpt-5", LatencyMs: 2500, WorkspaceID: f.workspace,
		Trace: &service.TraceUpdate{Name: "support-bot"}}); err != nil {
		t.Fatal(err)
	}

	w := call(http.MethodGet, "/traces?min_latency_ms=2000&model=gpt-5", "")
	var list service.ListResult[service.TraceSummary]
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &list) != nil || list.Meta.Total != 1 || list.Data[0].Name != "support-bot" {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	if w := call(http.MethodGet, "/traces?bogus=1", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown filter: %d", w.Code)
	}
	if w := call(http.MethodGet, "/traces/trace-gw", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"observations":[{`) {
		t.Fatalf("detail: %d %s", w.Code, w.Body.String())
	}
	if w := call(http.MethodGet, "/traces/missing", ""); w.Code != http.StatusNotFound {
		t.Fatalf("missing detail: %d", w.Code)
	}
	if w := call(http.MethodPost, "/traces/trace-gw/scores", `{"name":"helpful","bool_value":true,"comment":"good"}`); w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"data_type":"boolean"`) {
		t.Fatalf("score: %d %s", w.Code, w.Body.String())
	}
	if w := call(http.MethodPost, "/traces/trace-gw/scores", `{"name":"x"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid score: %d %s", w.Code, w.Body.String())
	}
	if w := call(http.MethodPut, "/traces/trace-gw/bookmark", `{"bookmarked":true}`); w.Code != http.StatusOK {
		t.Fatalf("bookmark: %d %s", w.Code, w.Body.String())
	}

	// Gateway scores: only the token that produced the trace may score it.
	gatewayScore := func(secret string, body string) int {
		r := httptest.NewRequest(http.MethodPost, "/gateway/v1/scores", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+secret)
		rec := httptest.NewRecorder()
		f.s.GatewayScoresAPI(rec, r)
		return rec.Code
	}
	if code := gatewayScore("", `{"trace_id":"trace-gw","name":"thumbs","value":1}`); code != http.StatusUnauthorized {
		t.Fatalf("anonymous gateway score: %d", code)
	}
	if code := gatewayScore(otherSecret, `{"trace_id":"trace-gw","name":"thumbs","value":1}`); code != http.StatusNotFound {
		t.Fatalf("foreign token scored trace: %d", code)
	}
	if code := gatewayScore(secret, `{"trace_id":"trace-gw","name":"thumbs","value":1}`); code != http.StatusCreated {
		t.Fatalf("own token score: %d", code)
	}

	w = call(http.MethodGet, "/traces/trace-gw", "")
	var detail service.TraceDetail
	err = json.Unmarshal(w.Body.Bytes(), &detail)
	if err != nil || len(detail.Scores) != 2 || !detail.Trace.Bookmarked {
		t.Fatalf("stored annotations: %+v %v", detail, err)
	}
}
