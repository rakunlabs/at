package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// Stdio MCP process introspection and lifecycle control.
//
// The StdioProcessManager pools subprocesses keyed by resolved command+args
// and starts them lazily on first use, so "did my server pick up the new
// config" and "is it even running" were previously unanswerable without a
// server restart. These endpoints answer both:
//
//   GET  /api/v1/mcp/stdio-processes           — every managed subprocess
//   GET  /api/v1/mcp/{servers|sets}/{id}/stdio-status  — per-record, per-upstream
//   POST /api/v1/mcp/{servers|sets}/{id}/stdio-restart — kill + respawn (optional {"index": N})
//   POST /api/v1/mcp/{servers|sets}/{id}/stdio-stop    — kill (next use respawns)
//
// Status/restart resolve {{var:...}} references the same way tool execution
// does, so they address the same process a tool call would reach. Responses
// echo the *stored* command/args (unresolved), never resolved values, because
// args may resolve secrets.

// mcpUpstreamStatus is the per-upstream entry of a stdio-status response.
type mcpUpstreamStatus struct {
	Index     int      `json:"index"`
	Command   string   `json:"command"`
	Args      []string `json:"args,omitempty"`
	Running   bool     `json:"running"`
	PID       int      `json:"pid,omitempty"`
	StartedAt string   `json:"started_at,omitempty"`
	UptimeSec int64    `json:"uptime_seconds,omitempty"`
	ExitError string   `json:"exit_error,omitempty"`
	Error     string   `json:"error,omitempty"` // restart failure
}

// ListStdioProcessesAPI handles GET /api/v1/mcp/stdio-processes.
// Args/env are omitted deliberately: they can carry resolved secrets.
func (s *Server) ListStdioProcessesAPI(w http.ResponseWriter, r *http.Request) {
	if s.stdioManager == nil {
		httpResponseJSON(w, map[string]any{"processes": []any{}}, http.StatusOK)
		return
	}
	statuses := s.stdioManager.Statuses()
	out := make([]map[string]any, 0, len(statuses))
	for _, st := range statuses {
		entry := map[string]any{
			"command":        st.Command,
			"pid":            st.PID,
			"alive":          st.Alive,
			"started_at":     st.StartedAt.UTC().Format(time.RFC3339),
			"uptime_seconds": int64(time.Since(st.StartedAt).Seconds()),
		}
		if st.ExitError != "" {
			entry["exit_error"] = st.ExitError
		}
		out = append(out, entry)
	}
	httpResponseJSON(w, map[string]any{"processes": out}, http.StatusOK)
}

// mcpRecordConfig resolves the MCPServerConfig for either kind of record.
func (s *Server) mcpRecordConfig(r *http.Request, kind string) (*service.MCPServerConfig, string, error) {
	id := r.PathValue("id")
	if id == "" {
		return nil, "", fmt.Errorf("id is required")
	}
	switch kind {
	case "servers":
		if s.mcpServerStore == nil {
			return nil, "", fmt.Errorf("store not configured")
		}
		rec, err := s.mcpServerStore.GetMCPServer(r.Context(), id)
		if err != nil {
			return nil, "", err
		}
		if rec == nil {
			return nil, "", nil
		}
		return &rec.Config, rec.Name, nil
	default: // sets
		if s.mcpSetStore == nil {
			return nil, "", fmt.Errorf("store not configured")
		}
		rec, err := s.mcpSetStore.GetMCPSet(r.Context(), id)
		if err != nil {
			return nil, "", err
		}
		if rec == nil {
			return nil, "", nil
		}
		return &rec.Config, rec.Name, nil
	}
}

// stdioUpstreamAction implements status/restart/stop for one record's stdio
// upstreams. action is "status", "restart" or "stop"; only-index < 0 means all.
func (s *Server) stdioUpstreamAction(w http.ResponseWriter, r *http.Request, kind, action string) {
	cfg, name, err := s.mcpRecordConfig(r, kind)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if cfg == nil {
		httpResponse(w, "record not found", http.StatusNotFound)
		return
	}

	onlyIndex := -1
	if action != "status" && r.Body != nil {
		var req struct {
			Index *int `json:"index"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil && req.Index != nil {
			onlyIndex = *req.Index
		}
	}

	results := make([]mcpUpstreamStatus, 0, len(cfg.MCPUpstreams))
	for i, upstream := range cfg.MCPUpstreams {
		if upstream.Command == "" {
			continue // HTTP upstreams have no local process
		}
		if onlyIndex >= 0 && i != onlyIndex {
			continue
		}
		entry := mcpUpstreamStatus{Index: i, Command: upstream.Command, Args: upstream.Args}

		if s.stdioManager == nil {
			results = append(results, entry)
			continue
		}
		// Match the process a tool call would reach: same var resolution.
		resolved := upstream
		if s.variableStore != nil {
			resolved = s.resolveUpstreamVarsContext(r.Context(), upstream)
		}

		var st *service.StdioProcessStatus
		switch action {
		case "restart":
			st, err = s.stdioManager.Restart(resolved)
			if err != nil {
				entry.Error = err.Error()
			}
		case "stop":
			s.stdioManager.Stop(resolved)
		default:
			st = s.stdioManager.StatusFor(resolved)
		}

		if st != nil {
			entry.Running = st.Alive
			entry.PID = st.PID
			entry.StartedAt = st.StartedAt.UTC().Format(time.RFC3339)
			entry.UptimeSec = int64(time.Since(st.StartedAt).Seconds())
			entry.ExitError = st.ExitError
		}
		results = append(results, entry)
	}

	httpResponseJSON(w, map[string]any{"name": name, "upstreams": results}, http.StatusOK)
}

// MCPServerStdioStatusAPI handles GET /api/v1/mcp/servers/{id}/stdio-status.
func (s *Server) MCPServerStdioStatusAPI(w http.ResponseWriter, r *http.Request) {
	s.stdioUpstreamAction(w, r, "servers", "status")
}

// MCPServerStdioRestartAPI handles POST /api/v1/mcp/servers/{id}/stdio-restart.
func (s *Server) MCPServerStdioRestartAPI(w http.ResponseWriter, r *http.Request) {
	s.stdioUpstreamAction(w, r, "servers", "restart")
}

// MCPServerStdioStopAPI handles POST /api/v1/mcp/servers/{id}/stdio-stop.
func (s *Server) MCPServerStdioStopAPI(w http.ResponseWriter, r *http.Request) {
	s.stdioUpstreamAction(w, r, "servers", "stop")
}

// MCPSetStdioStatusAPI handles GET /api/v1/mcp/sets/{id}/stdio-status.
func (s *Server) MCPSetStdioStatusAPI(w http.ResponseWriter, r *http.Request) {
	s.stdioUpstreamAction(w, r, "sets", "status")
}

// MCPSetStdioRestartAPI handles POST /api/v1/mcp/sets/{id}/stdio-restart.
func (s *Server) MCPSetStdioRestartAPI(w http.ResponseWriter, r *http.Request) {
	s.stdioUpstreamAction(w, r, "sets", "restart")
}

// MCPSetStdioStopAPI handles POST /api/v1/mcp/sets/{id}/stdio-stop.
func (s *Server) MCPSetStdioStopAPI(w http.ResponseWriter, r *http.Request) {
	s.stdioUpstreamAction(w, r, "sets", "stop")
}
