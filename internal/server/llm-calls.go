package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ListLLMCallsAPI handles GET /api/v1/llm-calls. Returns paginated call
// records with request/response bodies clipped to a preview. Supports the
// standard query filtering (?filter=, ?sort=, ?limit=, ?offset=).
func (s *Server) ListLLMCallsAPI(w http.ResponseWriter, r *http.Request) {
	if s.llmCallStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}
	// Default to newest-first when the caller doesn't specify a sort.
	if q != nil && len(q.Sort) == 0 {
		q.Sort = []query.ExpressionSort{{Field: "created_at", Desc: true}}
	}

	records, err := s.llmCallStore.ListLLMCalls(r.Context(), q)
	if err != nil {
		slog.Error("list llm calls failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list llm calls: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.LLMCall]{Data: []service.LLMCall{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetLLMCallAPI handles GET /api/v1/llm-calls/{id}. Returns the full record
// including complete request/response bodies. When a body was spilled to a
// file (large payloads), the file contents are loaded back inline so the
// caller always sees the whole payload.
func (s *Server) GetLLMCallAPI(w http.ResponseWriter, r *http.Request) {
	if s.llmCallStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	call, err := s.llmCallStore.GetLLMCall(r.Context(), id)
	if err != nil {
		slog.Error("get llm call failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get llm call: %v", err), http.StatusInternalServerError)
		return
	}
	if call == nil {
		httpResponse(w, "llm call not found", http.StatusNotFound)
		return
	}

	// Rehydrate spilled bodies from disk so the detail view is complete.
	if call.RequestTruncated && call.RequestRef != "" {
		if b, rErr := os.ReadFile(call.RequestRef); rErr == nil {
			call.RequestBody = string(b)
		}
	}
	if call.ResponseTruncated && call.ResponseRef != "" {
		if b, rErr := os.ReadFile(call.ResponseRef); rErr == nil {
			call.ResponseBody = string(b)
		}
	}

	httpResponseJSON(w, call, http.StatusOK)
}
