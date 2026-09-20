package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/traceexport"
)

// The queue is process-bounded rather than one queue/goroutine per workspace.
// Workers read configuration at delivery time: saves, disable and deletions
// apply across replicas without retaining credential-bearing exporters.
func (s *Server) startTraceExport(ctx context.Context) {
	store, ok := s.store.(service.TraceExportStorer)
	if !ok {
		return
	}
	s.traceExportQueue = make(chan service.LLMCall, 256)
	for i := 0; i < 4; i++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case first := <-s.traceExportQueue:
					batches := map[string][]service.LLMCall{first.WorkspaceID: {first}}
					for n := 1; n < 32; n++ {
						select {
						case c := <-s.traceExportQueue:
							batches[c.WorkspaceID] = append(batches[c.WorkspaceID], c)
						default:
							n = 32
						}
					}
					for workspace, calls := range batches {
						func() {
							delivery, cancel := context.WithTimeout(ctx, 12*time.Second)
							defer cancel()
							cfg, err := store.LoadTraceExportSettings(delivery, workspace)
							if err != nil {
								slog.Warn("trace export settings unavailable", "workspace_id", workspace)
								return
							}
							if !cfg.Enabled {
								return
							}
							if err := traceexport.Send(delivery, cfg, traceexport.Request(workspace, calls, cfg.IncludeContent)); err != nil {
								slog.Warn("trace export failed", "workspace_id", workspace, "observations", len(calls), "error", err.Error())
							}
						}()
					}
				}
			}
		}()
	}
}

func (s *Server) enqueueTraceExport(c service.LLMCall, bodies bool, req, resp []byte) {
	if s.traceExportQueue == nil || c.WorkspaceID == "" {
		return
	}
	// Never retain spill paths, full payloads or arbitrary caller metadata in
	// the bounded delivery queue. The feature toggle also gates tool content.
	c.RequestRef, c.ResponseRef, c.Metadata = "", "", nil
	c.RequestBody, c.ResponseBody = "", ""
	if bodies {
		c.RequestBody, c.ResponseBody = traceexport.Clip(string(req)), traceexport.Clip(string(resp))
		c.Input, c.Output, c.ErrorMessage = traceexport.Clip(c.Input), traceexport.Clip(c.Output), traceexport.Clip(c.ErrorMessage)
	} else {
		c.Input, c.Output, c.ErrorMessage = "", "", ""
	}
	select {
	case s.traceExportQueue <- c:
	default:
		// Rate-limit overflow reporting on a process-wide queue.
		now := time.Now().Unix()
		last := s.traceExportDropLog.Load()
		if now-last >= 60 && s.traceExportDropLog.CompareAndSwap(last, now) {
			slog.Warn("trace export queue full; observations dropped")
		}
	}
}

func traceExportWorkspace(ctx context.Context, auth *authResult) string {
	if auth != nil && auth.token != nil {
		return firstNonEmpty(auth.token.WorkspaceID, service.DefaultWorkspaceID)
	}
	if p, _, ok := service.ExecutionFromContext(ctx); ok {
		return p.WorkspaceID
	}
	if p, ok := service.AccessPrincipalFromContext(ctx); ok {
		return p.WorkspaceID
	}
	return service.DefaultWorkspaceID
}

func traceExportActor(r *http.Request) (string, bool) {
	a, ok := service.AccessPrincipalFromContext(r.Context())
	return a.WorkspaceID, ok && a.Allows("workspace.write", service.AccessResource{WorkspaceID: a.WorkspaceID})
}

func (s *Server) TraceExportSettingsAPI(w http.ResponseWriter, r *http.Request) {
	workspace, allowed := traceExportActor(r)
	if !allowed {
		httpResponse(w, "workspace administrator permission required", http.StatusForbidden)
		return
	}
	store, ok := s.store.(service.TraceExportStorer)
	if !ok {
		httpResponse(w, "trace export store unavailable", 503)
		return
	}
	current, err := store.LoadTraceExportSettings(r.Context(), workspace)
	if err != nil {
		httpResponse(w, "could not load trace export settings", 503)
		return
	}
	if r.Method == http.MethodGet {
		httpResponseJSON(w, current.Redacted(), 200)
		return
	}
	var requested service.TraceExportSettings
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 80<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(&requested); err != nil {
		httpResponse(w, "invalid trace export settings", 400)
		return
	}
	if requested.Version != current.Version {
		httpResponse(w, service.ErrTraceExportConflict.Error(), 409)
		return
	}
	requested.Endpoint = strings.TrimSpace(requested.Endpoint)
	if requested.SecretKey == "***" {
		requested.SecretKey = current.SecretKey
	}
	for k, value := range requested.Headers {
		if value != "***" {
			continue
		}
		stored, found := current.Headers[k]
		if !found {
			httpResponse(w, "masked header has no stored value; enter its value", 400)
			return
		}
		requested.Headers[k] = stored
	}
	isTest := r.Method == http.MethodPost
	validation := requested
	if isTest {
		validation.Enabled = true
	}
	if err := validation.Validate(); err != nil {
		httpResponse(w, err.Error(), 400)
		return
	}
	if isTest {
		id := ulid.Make().String()
		call := service.LLMCall{ID: id, TraceID: id, WorkspaceID: workspace, Name: "AT trace export connection test", ObservationType: service.ObservationEvent, Source: "connection_test", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		started := time.Now()
		err := traceexport.Send(r.Context(), validation, traceexport.Request(workspace, []service.LLMCall{call}, false))
		message := "The receiver accepted the test trace. Check the destination for ‘AT trace export connection test’."
		if err != nil {
			message = err.Error()
		}
		httpResponseJSON(w, map[string]any{"ok": err == nil, "message": message, "trace_id": traceexport.TraceHex(workspace, call), "duration_ms": time.Since(started).Milliseconds()}, 200)
		return
	}
	saved, err := store.SaveTraceExportSettings(r.Context(), requested)
	if err != nil {
		if errors.Is(err, service.ErrTraceExportConflict) {
			httpResponse(w, err.Error(), 409)
			return
		}
		if workspaceBusinessError(w, err) {
			return
		}
		httpResponse(w, "could not save trace export settings", 500)
		return
	}
	httpResponseJSON(w, saved.Redacted(), 200)
}
