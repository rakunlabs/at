package server

import (
	"net/http"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// localToolObservationMaxIO bounds what a client may report per side. The
// stored preview is capped at service.ObservationPreviewBytes anyway; this
// stops a page from posting a payload that large repeatedly.
const localToolObservationMaxIO = 16 << 10

// localToolObservationRequest is what the browser reports after running a
// tool on the user's own machine.
//
// Every field here is an assertion by the client about work the server did not
// perform and cannot verify. The fields the server owns — workspace, owner,
// source, timestamps, cost — are stamped server-side and deliberately absent
// from this struct.
type localToolObservationRequest struct {
	TraceID   string `json:"trace_id"`
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
	Server    string `json:"server"`
	Status    string `json:"status"`
	LatencyMs int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
	Input     string `json:"input,omitempty"`
	Output    string `json:"output,omitempty"`
}

// ChatToolObservationAPI handles POST /api/v1/chats/tool-observations.
//
// A local MCP tool runs in the browser, so without this the Traces view would
// show a conversation's generations with the tool steps between them missing
// entirely — not as an absence, but as an unexplained gap in the reasoning.
//
// The observation is marked `origin: browser_local` and `client_asserted:
// true`. That is not decoration: every other observation is written by the
// process that did the work, and a reader comparing a local tool's reported
// duration against a provider's has to know which is which.
//
// Failures are never surfaced to the conversation. A trace is bookkeeping; a
// turn must not fail because bookkeeping did.
func (s *Server) ChatToolObservationAPI(w http.ResponseWriter, r *http.Request) {
	_, owner := s.playgroundAccess(w, r)
	if owner == "" {
		return
	}

	var req localToolObservationRequest
	if !decodePlaygroundBody(w, r, &req) {
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		nativeError(w, http.StatusBadRequest, "tool name is required")
		return
	}
	if req.TraceID == "" {
		// Without a trace the row would be an orphan nobody can find. The
		// client always has one: it mints the turn's trace ID itself.
		nativeError(w, http.StatusBadRequest, "trace_id is required")
		return
	}

	status := "ok"
	level := service.ObservationLevelDefault
	if req.Status != "" && req.Status != "ok" {
		status = "error"
		level = service.ObservationLevelError
	}

	// Content is reported only when installation body capture is on. This is
	// deliberately stricter than server-executed tools, whose previews are
	// recorded unconditionally: their input and output already passed through
	// this process, while a local tool's did not, and uploading it is a new
	// flow of data off the user's machine.
	input, output := "", ""
	if s.llmAuditEnabled(r.Context()) {
		input = clipLocalToolIO(req.Input)
		output = clipLocalToolIO(req.Output)
	}

	s.recordLLMCallAsync(r.Context(), llmAuditParams{
		source:    "chat",
		endpoint:  r.URL.Path,
		obsType:   service.ObservationTool,
		name:      req.Name,
		traceID:   req.TraceID,
		sessionID: req.SessionID,
		input:     input,
		output:    output,
		level:     level,
		status:    status,
		errMsg:    clipLocalToolIO(req.Error),
		latencyMs: req.LatencyMs,
		metadata: map[string]any{
			"origin":          "browser_local",
			"client_asserted": true,
			"local_server":    strings.TrimSpace(req.Server),
		},
	})

	w.WriteHeader(http.StatusAccepted)
}

func clipLocalToolIO(s string) string {
	if len(s) <= localToolObservationMaxIO {
		return s
	}

	return s[:localToolObservationMaxIO]
}
