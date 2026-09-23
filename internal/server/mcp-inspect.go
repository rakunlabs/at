package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

const mcpInspectTimeout = 15 * time.Second

type mcpUpstreamInspection struct {
	Index           int            `json:"index"`
	Transport       string         `json:"transport"`
	ResponseMode    string         `json:"response_mode,omitempty"`
	ProtocolVersion string         `json:"protocol_version,omitempty"`
	ServerName      string         `json:"server_name,omitempty"`
	ServerVersion   string         `json:"server_version,omitempty"`
	ToolCount       int            `json:"tool_count"`
	Tools           []service.Tool `json:"tools"`
	DurationMS      int64          `json:"duration_ms"`
	Error           string         `json:"error,omitempty"`
}

// MCPSetInspectUpstreamsAPI actively connects to the saved upstreams and runs
// tools/list. Failures are returned per upstream so one unavailable server does
// not hide the tools discovered from the others.
func (s *Server) MCPSetInspectUpstreamsAPI(w http.ResponseWriter, r *http.Request) {
	cfg, name, err := s.mcpRecordConfig(r, "sets")
	if err != nil {
		httpResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if cfg == nil {
		httpResponse(w, "MCP set not found", http.StatusNotFound)
		return
	}
	var req struct {
		Index *int `json:"index"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			httpResponse(w, "invalid inspection request", http.StatusBadRequest)
			return
		}
	}
	if req.Index != nil && (*req.Index < 0 || *req.Index >= len(cfg.MCPUpstreams)) {
		httpResponse(w, "upstream index is out of range", http.StatusBadRequest)
		return
	}

	results := make([]mcpUpstreamInspection, 0, len(cfg.MCPUpstreams))
	for i, upstream := range cfg.MCPUpstreams {
		if req.Index != nil && i != *req.Index {
			continue
		}
		entry := mcpUpstreamInspection{Index: i, Transport: "stdio", Tools: []service.Tool{}}
		if upstream.Command == "" {
			entry.Transport = "streamable_http"
		}
		started := time.Now()
		ctx, cancel := context.WithTimeout(r.Context(), mcpInspectTimeout)
		lease, acquireErr := s.acquireMCPClient(ctx, upstream)
		if acquireErr != nil {
			entry.Error = acquireErr.Error()
			entry.DurationMS = time.Since(started).Milliseconds()
			cancel()
			results = append(results, entry)
			continue
		}

		tools, listErr := lease.client.ListTools(ctx)
		if listErr != nil {
			entry.Error = listErr.Error()
		} else {
			entry.Tools = tools
			entry.ToolCount = len(tools)
		}
		if infoClient, ok := lease.client.(interface {
			ConnectionInfo() service.MCPConnectionInfo
		}); ok {
			info := infoClient.ConnectionInfo()
			entry.Transport = info.Transport
			entry.ResponseMode = info.ResponseMode
			entry.ProtocolVersion = info.ProtocolVersion
			entry.ServerName = info.ServerName
			entry.ServerVersion = info.ServerVersion
		}
		entry.DurationMS = time.Since(started).Milliseconds()
		if lease.owned {
			closeMCPClient(ctx, lease.client) // best-effort diagnostic cleanup
		}
		cancel()
		results = append(results, entry)
	}

	httpResponseJSON(w, map[string]any{"name": name, "upstreams": results}, http.StatusOK)
}
