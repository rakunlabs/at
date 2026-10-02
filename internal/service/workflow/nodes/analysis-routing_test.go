package nodes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// Several endpoints are fetched, combined by a Script, analysed by an LLM in
// JSON mode, and a Switch on the parsed answer picks which Email template is
// sent. Before JSON mode the Switch saw the answer as a string, found no
// /severity and silently took the fallback branch.
func TestAnalysisRoutesToEmailTemplate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"service": strings.TrimPrefix(r.URL.Path, "/"), "errors": len(r.URL.Path)})
	}))
	defer srv.Close()

	rule := func(id, value string) map[string]any {
		return map[string]any{"id": id, "label": id, "path": "/severity", "operator": "eq", "value_type": "string", "value": value}
	}
	mail := func(id, subject string) service.WorkflowNode {
		return service.WorkflowNode{ID: id, Type: "email", Data: map[string]any{
			"config_id": "smtp", "to": "ops@example.com", "subject": subject, "body": "{{.summary}} ({{.score}})",
		}}
	}
	graph := service.WorkflowGraph{Nodes: []service.WorkflowNode{
		{ID: "in", Type: "input"},
		{ID: "a", Type: "http_request", Data: map[string]any{"url": srv.URL + "/billing"}},
		{ID: "b", Type: "http_request", Data: map[string]any{"url": srv.URL + "/auth"}},
		{ID: "c", Type: "http_request", Data: map[string]any{"url": srv.URL + "/search"}},
		{ID: "collect", Type: "script", Data: map[string]any{"input_count": float64(3), "code": "return {billing: data1.response, auth: data2.response, search: data3.response}"}},
		{ID: "llm", Type: "llm_call", Data: map[string]any{"provider": "p", "output_format": "json"}},
		{ID: "route", Type: "switch", Data: map[string]any{"match_mode": "first", "rules": []any{rule("case_critical", "critical"), rule("case_warning", "warning")}}},
		mail("critical", "CRITICAL"), mail("warning", "WARNING"), mail("normal", "NORMAL"),
	}, Edges: []service.WorkflowEdge{
		{Source: "in", SourceHandle: "data", Target: "a", TargetHandle: "data"},
		{Source: "in", SourceHandle: "data", Target: "b", TargetHandle: "data"},
		{Source: "in", SourceHandle: "data", Target: "c", TargetHandle: "data"},
		{Source: "a", SourceHandle: "success", Target: "collect", TargetHandle: "data1"},
		{Source: "b", SourceHandle: "success", Target: "collect", TargetHandle: "data2"},
		{Source: "c", SourceHandle: "success", Target: "collect", TargetHandle: "data3"},
		{Source: "collect", SourceHandle: "result", Target: "llm", TargetHandle: "prompt"},
		{Source: "llm", SourceHandle: "json", Target: "route", TargetHandle: "data"},
		{Source: "route", SourceHandle: "case_critical", Target: "critical", TargetHandle: "data"},
		{Source: "route", SourceHandle: "case_warning", Target: "warning", TargetHandle: "data"},
		{Source: "route", SourceHandle: "fallback", Target: "normal", TargetHandle: "data"},
	}}

	for _, tt := range []struct {
		name, answer, subject, body string
	}{
		{"critical", `{"severity":"critical","summary":"billing is down","score":9}`, "CRITICAL", "billing is down (9)"},
		{"warning in a fence", "```json\n{\"severity\":\"warning\",\"summary\":\"auth slow\",\"score\":5}\n```", "WARNING", "auth slow (5)"},
		{"unmatched", `{"severity":"ok","summary":"all good","score":1}`, "NORMAL", "all good (1)"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			host, port, messages := fakeSMTP(t)
			var prompt string
			var format map[string]any
			mp := &mockProvider{chatFunc: func(_ context.Context, _ string, m []service.Message, _ []service.Tool, opts *service.ChatOptions) (*service.LLMResponse, error) {
				prompt, _ = m[len(m)-1].Content.(string)
				if opts != nil {
					format = opts.ResponseFormat
				}
				return &service.LLMResponse{Content: tt.answer, Finished: true}, nil
			}}
			e := workflow.NewEngineWithDependencies(workflow.Dependencies{
				NodeConfigLookup: smtpNodeConfig(t, host, port),
				ProviderLookup:   func(string) (service.LLMProvider, string, error) { return mp, "m", nil },
			})
			if _, err := e.Run(executiontest.Context(t), graph, map[string]any{}, []string{"in"}, nil); err != nil {
				t.Fatal(err)
			}

			// The prompt is exactly the collected data as JSON — no HTTP
			// headers, no Go map[...] formatting.
			var collected map[string]any
			if err := json.Unmarshal([]byte(prompt), &collected); err != nil || len(collected) != 3 || strings.Contains(prompt, "Content-Type") {
				t.Fatalf("prompt = %q (%v)", prompt, err)
			}
			if format["type"] != "json_object" {
				t.Fatalf("response_format = %v", format)
			}

			raw := <-messages
			if !strings.Contains(raw, "Subject: "+tt.subject) || !strings.Contains(raw, tt.body) {
				t.Fatalf("mail = %q", raw)
			}
			select {
			case extra := <-messages:
				t.Fatalf("more than one branch sent mail: %q", extra)
			default:
			}
		})
	}
}

// An answer that is not a JSON object fails the node rather than flowing to
// the Switch as text and silently taking the fallback branch.
func TestLLMCallJSONOutputRefusesNonJSON(t *testing.T) {
	for _, answer := range []string{"Severity is critical.", `["critical"]`, `{"a":1} trailing`, ""} {
		mp := &mockProvider{chatFunc: func(context.Context, string, []service.Message, []service.Tool, *service.ChatOptions) (*service.LLMResponse, error) {
			return &service.LLMResponse{Content: answer, Finished: true}, nil
		}}
		node := makeNode(t, "llm_call", map[string]any{"provider": "test-provider", "output_format": "json"})
		if _, err := node.Run(t.Context(), newTestRegistryWithProvider(mp), map[string]any{"prompt": "p"}); err == nil || !strings.Contains(err.Error(), "JSON object") {
			t.Fatalf("answer %q: error = %v", answer, err)
		}
	}
}

func TestLLMCallJSONSchema(t *testing.T) {
	var format map[string]any
	mp := &mockProvider{chatFunc: func(_ context.Context, _ string, _ []service.Message, _ []service.Tool, opts *service.ChatOptions) (*service.LLMResponse, error) {
		format = opts.ResponseFormat
		return &service.LLMResponse{Content: `{"severity":"warning"}`, Finished: true}, nil
	}}
	schema := `{"type":"object","properties":{"severity":{"type":"string","enum":["critical","warning","ok"]}},"required":["severity"]}`
	node := makeNode(t, "llm_call", map[string]any{"provider": "test-provider", "output_format": "json", "json_schema": schema})
	res, err := node.Run(t.Context(), newTestRegistryWithProvider(mp), map[string]any{"prompt": "p"})
	if err != nil {
		t.Fatal(err)
	}
	wrap, _ := format["json_schema"].(map[string]any)
	if format["type"] != "json_schema" || wrap["schema"] == nil {
		t.Fatalf("response_format = %v", format)
	}
	if got := res.Data()["json"].(map[string]any)["severity"]; got != "warning" {
		t.Fatalf("json = %v", res.Data()["json"])
	}

	reg := newTestRegistryWithProvider(mp)
	for _, data := range []map[string]any{
		{"provider": "test-provider", "output_format": "yaml"},
		{"provider": "test-provider", "json_schema": schema},
	} {
		n, err := workflow.GetNodeFactory("llm_call")(service.WorkflowNode{Type: "llm_call", Data: data})
		if err != nil {
			t.Fatal(err)
		}
		if err := n.Validate(t.Context(), reg); err == nil {
			t.Fatalf("config %v accepted", data)
		}
	}
	if _, err := workflow.GetNodeFactory("llm_call")(service.WorkflowNode{Type: "llm_call", Data: map[string]any{"provider": "p", "output_format": "json", "json_schema": "{not json"}}); err == nil {
		t.Fatal("invalid schema accepted")
	}
}
