package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestMCPSetInspectUpstreams(t *testing.T) {
	working := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req service.MCPRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode MCP request: %v", err)
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"protocolVersion": "2025-03-26",
				"serverInfo":      map[string]any{"name": "example", "version": "2.1"},
			}})
		case "notifications/initialized", "notifications/cancelled":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "text/event-stream")
			response := map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"tools": []map[string]any{
				{"name": "alpha", "description": "first", "inputSchema": map[string]any{"type": "object"}},
				{"name": "beta", "description": "second", "inputSchema": map[string]any{"type": "object"}},
			}}}
			payload, _ := json.Marshal(response)
			w.Write([]byte("data: "))
			w.Write(payload)
			w.Write([]byte("\n\n"))
		}
	}))
	defer working.Close()

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not an MCP endpoint", http.StatusMethodNotAllowed)
	}))
	defer broken.Close()

	set := &service.MCPSet{ID: "set-1", Name: "remote tools", Config: service.MCPServerConfig{MCPUpstreams: []service.MCPUpstream{
		{URL: working.URL},
		{URL: broken.URL},
	}}}
	s := &Server{mcpSetStore: &stdioTestSetStore{set: set}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/sets/set-1/inspect-upstreams", nil)
	req.SetPathValue("id", "set-1")
	rr := httptest.NewRecorder()
	s.MCPSetInspectUpstreamsAPI(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("inspect status = %d: %s", rr.Code, rr.Body.String())
	}

	var out struct {
		Name      string                  `json:"name"`
		Upstreams []mcpUpstreamInspection `json:"upstreams"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode inspection: %v", err)
	}
	if out.Name != "remote tools" || len(out.Upstreams) != 2 {
		t.Fatalf("inspection = %+v", out)
	}
	got := out.Upstreams[0]
	if got.Transport != "streamable_http" || got.ResponseMode != "sse" || got.ToolCount != 2 || got.ServerName != "example" {
		t.Fatalf("working upstream = %+v", got)
	}
	if out.Upstreams[1].Error == "" {
		t.Fatalf("broken upstream = %+v, want an error", out.Upstreams[1])
	}
}
