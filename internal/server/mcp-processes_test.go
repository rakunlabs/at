package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

type stdioTestSetStore struct {
	service.MCPSetStorer
	set *service.MCPSet
}

func (s *stdioTestSetStore) GetMCPSet(_ context.Context, id string) (*service.MCPSet, error) {
	if s.set != nil && s.set.ID == id {
		return s.set, nil
	}
	return nil, nil
}

// writeFakeStdioMCP mirrors the fake server in internal/service tests: it
// answers the initialize handshake and then reads until stdin closes.
func writeFakeStdioMCP(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stdio MCP fake server requires /bin/sh")
	}
	path := filepath.Join(t.TempDir(), "fake-mcp.sh")
	script := `#!/bin/sh
while read line; do
  case "$line" in
    *'"method":"initialize"'*) printf '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","serverInfo":{"name":"fake","version":"1.0"}}}\n' ;;
  esac
done
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake mcp: %v", err)
	}
	return path
}

type stdioStatusResponse struct {
	Name      string              `json:"name"`
	Upstreams []mcpUpstreamStatus `json:"upstreams"`
}

func stdioSetRequest(t *testing.T, s *Server, method, action, id string, body string) stdioStatusResponse {
	t.Helper()
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	} else {
		rd = strings.NewReader("")
	}
	req := httptest.NewRequest(method, "/api/v1/mcp/sets/"+id+"/"+action, rd)
	req.SetPathValue("id", id)
	rr := httptest.NewRecorder()
	switch action {
	case "stdio-status":
		s.MCPSetStdioStatusAPI(rr, req)
	case "stdio-restart":
		s.MCPSetStdioRestartAPI(rr, req)
	case "stdio-stop":
		s.MCPSetStdioStopAPI(rr, req)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("%s %s = %d: %s", method, action, rr.Code, rr.Body.String())
	}
	var out stdioStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse %s response: %v", action, err)
	}
	return out
}

func TestMCPSetStdioLifecycleEndpoints(t *testing.T) {
	script := writeFakeStdioMCP(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	set := &service.MCPSet{
		ID:   "set-1",
		Name: "local tools",
		Config: service.MCPServerConfig{
			MCPUpstreams: []service.MCPUpstream{
				{URL: "https://mcp.example"}, // no local process
				{Command: script, Env: map[string]string{"A": "1"}},
			},
		},
	}
	s := &Server{
		mcpSetStore:  &stdioTestSetStore{set: set},
		stdioManager: service.NewStdioProcessManager(ctx),
	}
	defer s.stdioManager.Close()

	// Before anything runs: the HTTP upstream is skipped, the stdio one is
	// reported as not running.
	out := stdioSetRequest(t, s, http.MethodGet, "stdio-status", "set-1", "")
	if out.Name != "local tools" || len(out.Upstreams) != 1 {
		t.Fatalf("status = %+v, want one stdio upstream", out)
	}
	if got := out.Upstreams[0]; got.Index != 1 || got.Running || got.Command != script {
		t.Fatalf("initial upstream status = %+v", got)
	}

	// Restart spawns it and reports the fresh process.
	out = stdioSetRequest(t, s, http.MethodPost, "stdio-restart", "set-1", "")
	if got := out.Upstreams[0]; !got.Running || got.PID <= 0 || got.Error != "" {
		t.Fatalf("restart status = %+v, want running", got)
	}
	firstPID := out.Upstreams[0].PID

	// Restart again replaces the process.
	out = stdioSetRequest(t, s, http.MethodPost, "stdio-restart", "set-1", `{"index":1}`)
	if got := out.Upstreams[0]; !got.Running || got.PID == firstPID {
		t.Fatalf("second restart = %+v, want new pid (old %d)", got, firstPID)
	}

	// Stop kills it; status then reports not running.
	stdioSetRequest(t, s, http.MethodPost, "stdio-stop", "set-1", "")
	out = stdioSetRequest(t, s, http.MethodGet, "stdio-status", "set-1", "")
	if got := out.Upstreams[0]; got.Running {
		t.Fatalf("status after stop = %+v, want stopped", got)
	}

	// Global process list is empty again after stop.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mcp/stdio-processes", nil)
	rr := httptest.NewRecorder()
	s.ListStdioProcessesAPI(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("stdio-processes = %d: %s", rr.Code, rr.Body.String())
	}
	var procs struct {
		Processes []map[string]any `json:"processes"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &procs); err != nil {
		t.Fatalf("parse processes: %v", err)
	}
	if len(procs.Processes) != 0 {
		t.Fatalf("processes after stop = %+v, want empty", procs.Processes)
	}

	// Unknown record answers 404.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/mcp/sets/missing/stdio-status", nil)
	req.SetPathValue("id", "missing")
	rr = httptest.NewRecorder()
	s.MCPSetStdioStatusAPI(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing set = %d, want 404", rr.Code)
	}

	// A restart that cannot spawn reports the error per upstream, not a 500:
	// the record itself was found and other upstreams may have succeeded.
	set.Config.MCPUpstreams[1].Command = filepath.Join(t.TempDir(), "missing-binary")
	out = stdioSetRequest(t, s, http.MethodPost, "stdio-restart", "set-1", "")
	if got := out.Upstreams[0]; got.Error == "" || got.Running {
		t.Fatalf("broken restart = %+v, want per-upstream error", got)
	}
}
