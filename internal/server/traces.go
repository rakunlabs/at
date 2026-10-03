package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// traceStore returns the trace read model, or writes 503.
func (s *Server) traceStore(w http.ResponseWriter) (service.TraceStorer, bool) {
	store, ok := s.llmCallStore.(service.TraceStorer)
	if !ok || store == nil {
		httpResponse(w, "trace explorer unavailable", http.StatusServiceUnavailable)
		return nil, false
	}
	return store, true
}

// traceListValues reads a filter that may be repeated or comma separated.
func traceListValues(q url.Values, key string) []string {
	var out []string
	for _, raw := range q[key] {
		for _, v := range strings.Split(raw, ",") {
			if v = strings.TrimSpace(v); v != "" && len(v) <= 256 && !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
	}
	if len(out) > 100 {
		out = out[:100]
	}
	return out
}

func traceTime(q url.Values, key string) (time.Time, error) {
	v := strings.TrimSpace(q.Get(key))
	if v == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return time.Time{}, errors.New(key + " must be an RFC3339 timestamp")
	}
	return t, nil
}

func traceInt(q url.Values, key string) (*int64, error) {
	v := strings.TrimSpace(q.Get(key))
	if v == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return nil, errors.New(key + " must be a non-negative integer")
	}
	return &n, nil
}

func traceFloat(q url.Values, key string) (*float64, error) {
	v := strings.TrimSpace(q.Get(key))
	if v == "" {
		return nil, nil
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return nil, errors.New(key + " must be a number")
	}
	return &n, nil
}

func tracePage(q url.Values) (uint64, uint64, error) {
	var offset, limit uint64
	if v := q.Get("offset"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return 0, 0, errors.New("offset must be a non-negative integer")
		}
		offset = n
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil || n == 0 {
			return 0, 0, errors.New("limit must be a positive integer")
		}
		limit = n
	}
	return offset, limit, nil
}

// parseTraceQuery maps query parameters onto a TraceQuery. Unknown
// parameters are rejected so a typo cannot silently widen a filter.
func parseTraceQuery(q url.Values) (service.TraceQuery, error) {
	known := []string{"from", "to", "q", "trace_id", "name", "user_id", "end_user", "session_id", "token_id", "task_id", "agent_id",
		"model", "source", "tag", "environment", "release", "status", "min_latency_ms", "max_latency_ms", "min_cost_cents",
		"max_cost_cents", "min_tokens", "max_tokens", "score_name", "min_score", "max_score", "bookmarked", "sort", "order", "offset", "limit"}
	for key := range q {
		if !slices.Contains(known, key) {
			return service.TraceQuery{}, errors.New("unknown filter: " + key)
		}
	}
	var tq service.TraceQuery
	var err error
	if tq.From, err = traceTime(q, "from"); err != nil {
		return tq, err
	}
	if tq.To, err = traceTime(q, "to"); err != nil {
		return tq, err
	}
	tq.Search = strings.TrimSpace(q.Get("q"))
	if len(tq.Search) > 256 {
		return tq, errors.New("q is too long")
	}
	tq.TraceIDs = traceListValues(q, "trace_id")
	tq.Names = traceListValues(q, "name")
	tq.UserIDs = traceListValues(q, "user_id")
	tq.EndUsers = traceListValues(q, "end_user")
	tq.SessionIDs = traceListValues(q, "session_id")
	tq.TokenIDs = traceListValues(q, "token_id")
	tq.TaskIDs = traceListValues(q, "task_id")
	tq.AgentIDs = traceListValues(q, "agent_id")
	tq.Models = traceListValues(q, "model")
	tq.Sources = traceListValues(q, "source")
	tq.Tags = traceListValues(q, "tag")
	tq.Environments = traceListValues(q, "environment")
	tq.Releases = traceListValues(q, "release")
	switch status := q.Get("status"); status {
	case "", "ok", "error":
		tq.Status = status
	default:
		return tq, errors.New("status must be ok or error")
	}
	if tq.MinLatencyMs, err = traceInt(q, "min_latency_ms"); err != nil {
		return tq, err
	}
	if tq.MaxLatencyMs, err = traceInt(q, "max_latency_ms"); err != nil {
		return tq, err
	}
	if tq.MinTokens, err = traceInt(q, "min_tokens"); err != nil {
		return tq, err
	}
	if tq.MaxTokens, err = traceInt(q, "max_tokens"); err != nil {
		return tq, err
	}
	if tq.MinCostCents, err = traceFloat(q, "min_cost_cents"); err != nil {
		return tq, err
	}
	if tq.MaxCostCents, err = traceFloat(q, "max_cost_cents"); err != nil {
		return tq, err
	}
	tq.ScoreName = strings.TrimSpace(q.Get("score_name"))
	if tq.MinScore, err = traceFloat(q, "min_score"); err != nil {
		return tq, err
	}
	if tq.MaxScore, err = traceFloat(q, "max_score"); err != nil {
		return tq, err
	}
	if (tq.MinScore != nil || tq.MaxScore != nil) && tq.ScoreName == "" {
		return tq, errors.New("min_score/max_score require score_name")
	}
	tq.Bookmarked = q.Get("bookmarked") == "true"
	tq.Sort = q.Get("sort")
	if tq.Sort != "" && !slices.Contains(service.TraceSortFields, tq.Sort) {
		return tq, errors.New("sort must be one of " + strings.Join(service.TraceSortFields, ", "))
	}
	switch q.Get("order") {
	case "", "desc":
		tq.Desc = true
	case "asc":
		tq.Desc = false
	default:
		return tq, errors.New("order must be asc or desc")
	}
	if tq.Offset, tq.Limit, err = tracePage(q); err != nil {
		return tq, err
	}
	return tq, nil
}

func writeTraceStoreError(w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, service.ErrTraceNotFound):
		httpResponse(w, "trace not found", http.StatusNotFound)
	case errors.Is(err, service.ErrAccessDenied):
		httpResponse(w, "not allowed", http.StatusForbidden)
	default:
		slog.Error(what+" failed", "error", err)
		httpResponse(w, what+" failed", http.StatusInternalServerError)
	}
}

// ListTracesAPI handles GET /api/v1/traces.
func (s *Server) ListTracesAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.traceStore(w)
	if !ok {
		return
	}
	tq, err := parseTraceQuery(r.URL.Query())
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	result, err := store.ListTraces(r.Context(), tq)
	if err != nil {
		writeTraceStoreError(w, err, "list traces")
		return
	}
	httpResponseJSON(w, result, http.StatusOK)
}

// GetTraceAPI handles GET /api/v1/traces/{id}.
func (s *Server) GetTraceAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.traceStore(w)
	if !ok {
		return
	}
	detail, err := store.GetTrace(r.Context(), r.PathValue("id"))
	if err != nil {
		writeTraceStoreError(w, err, "get trace")
		return
	}
	if detail == nil {
		httpResponse(w, "trace not found", http.StatusNotFound)
		return
	}
	httpResponseJSON(w, detail, http.StatusOK)
}

// GetTraceFacetsAPI handles GET /api/v1/traces/facets.
func (s *Server) GetTraceFacetsAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.traceStore(w)
	if !ok {
		return
	}
	from, err := traceTime(r.URL.Query(), "from")
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	facets, err := store.GetTraceFacets(r.Context(), from)
	if err != nil {
		writeTraceStoreError(w, err, "trace facets")
		return
	}
	httpResponseJSON(w, facets, http.StatusOK)
}

// ListTraceSessionsAPI handles GET /api/v1/traces/sessions.
func (s *Server) ListTraceSessionsAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.traceStore(w)
	if !ok {
		return
	}
	q := r.URL.Query()
	for key := range q {
		if !slices.Contains([]string{"from", "to", "q", "user_id", "source", "offset", "limit"}, key) {
			httpResponse(w, "unknown filter: "+key, http.StatusBadRequest)
			return
		}
	}
	var sq service.TraceSessionQuery
	var err error
	if sq.From, err = traceTime(q, "from"); err == nil {
		sq.To, err = traceTime(q, "to")
	}
	if err == nil {
		sq.Offset, sq.Limit, err = tracePage(q)
	}
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	sq.Search = strings.TrimSpace(q.Get("q"))
	sq.UserIDs = traceListValues(q, "user_id")
	sq.Sources = traceListValues(q, "source")
	result, err := store.ListTraceSessions(r.Context(), sq)
	if err != nil {
		writeTraceStoreError(w, err, "list trace sessions")
		return
	}
	httpResponseJSON(w, result, http.StatusOK)
}

// GetTraceSessionAPI handles GET /api/v1/traces/sessions/{id}?token_id=.
// Sessions are namespaced by API token, so the token is part of the key.
func (s *Server) GetTraceSessionAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.traceStore(w)
	if !ok {
		return
	}
	detail, err := store.GetTraceSession(r.Context(), r.PathValue("id"), r.URL.Query().Get("token_id"))
	if err != nil {
		writeTraceStoreError(w, err, "get trace session")
		return
	}
	if detail == nil {
		httpResponse(w, "session not found", http.StatusNotFound)
		return
	}
	httpResponseJSON(w, detail, http.StatusOK)
}

// traceScoreRequest is the body for creating a score.
type traceScoreRequest struct {
	TraceID       string   `json:"trace_id"`
	ObservationID string   `json:"observation_id"`
	Name          string   `json:"name"`
	DataType      string   `json:"data_type"`
	Value         *float64 `json:"value"`
	BoolValue     *bool    `json:"bool_value"`
	StringValue   string   `json:"string_value"`
	Comment       string   `json:"comment"`
}

func (req traceScoreRequest) score() service.TraceScore {
	value := req.Value
	if req.BoolValue != nil {
		v := 0.0
		if *req.BoolValue {
			v = 1
		}
		value = &v
		if req.DataType == "" {
			req.DataType = service.ScoreBoolean
		}
	}
	return service.TraceScore{
		TraceID: req.TraceID, ObservationID: req.ObservationID, Name: req.Name, DataType: req.DataType,
		Value: value, StringValue: req.StringValue, Comment: req.Comment,
	}
}

const traceScoreBodyMaxBytes = 16 << 10

// CreateTraceScoreAPI handles POST /api/v1/traces/{id}/scores: a manual
// annotation by the signed-in account.
func (s *Server) CreateTraceScoreAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.traceStore(w)
	if !ok {
		return
	}
	var req traceScoreRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, traceScoreBodyMaxBytes)).Decode(&req); err != nil {
		httpResponse(w, "invalid score body", http.StatusBadRequest)
		return
	}
	req.TraceID = r.PathValue("id")
	score, err := store.CreateTraceScore(r.Context(), req.score())
	if err != nil {
		if errors.Is(err, service.ErrTraceNotFound) || errors.Is(err, service.ErrAccessDenied) {
			writeTraceStoreError(w, err, "create trace score")
			return
		}
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	httpResponseJSON(w, score, http.StatusCreated)
}

// DeleteTraceScoreAPI handles DELETE /api/v1/traces/scores/{id}.
func (s *Server) DeleteTraceScoreAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.traceStore(w)
	if !ok {
		return
	}
	if err := store.DeleteTraceScore(r.Context(), r.PathValue("id")); err != nil {
		writeTraceStoreError(w, err, "delete trace score")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SetTraceBookmarkAPI handles PUT /api/v1/traces/{id}/bookmark {bookmarked}.
func (s *Server) SetTraceBookmarkAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.traceStore(w)
	if !ok {
		return
	}
	var req struct {
		Bookmarked bool `json:"bookmarked"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		httpResponse(w, "invalid bookmark body", http.StatusBadRequest)
		return
	}
	if err := store.SetTraceBookmark(r.Context(), r.PathValue("id"), req.Bookmarked); err != nil {
		writeTraceStoreError(w, err, "bookmark trace")
		return
	}
	httpResponseJSON(w, map[string]bool{"bookmarked": req.Bookmarked}, http.StatusOK)
}

// GatewayScoresAPI handles POST /gateway/v1/scores. A client scores a trace
// it produced: the trace must contain an observation recorded with the same
// API token, so one token cannot annotate another client's traffic.
func (s *Server) GatewayScoresAPI(w http.ResponseWriter, r *http.Request) {
	auth, authErr := s.authenticateRequest(r)
	if authErr != "" {
		httpResponseJSON(w, map[string]any{"error": map[string]any{
			"message": authErr, "type": "invalid_request_error", "code": "invalid_api_key",
		}}, http.StatusUnauthorized)
		return
	}
	if auth == nil || auth.token == nil {
		httpResponseJSON(w, map[string]any{"error": map[string]any{
			"message": "scores require an API token", "type": "invalid_request_error", "code": "invalid_api_key",
		}}, http.StatusUnauthorized)
		return
	}
	store, ok := s.llmCallStore.(service.TraceStorer)
	if !ok || store == nil {
		httpResponseJSON(w, map[string]any{"error": map[string]any{"message": "tracing unavailable", "type": "server_error"}}, http.StatusServiceUnavailable)
		return
	}
	var req traceScoreRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, traceScoreBodyMaxBytes)).Decode(&req); err != nil {
		gatewayBadRequest(w, "invalid request body", "", "")
		return
	}
	score, err := store.CreateTraceScoreForToken(r.Context(), auth.token.WorkspaceID, auth.token.ID, req.score())
	if err != nil {
		if errors.Is(err, service.ErrTraceNotFound) {
			httpResponseJSON(w, map[string]any{"error": map[string]any{
				"message": "trace not found for this token", "type": "invalid_request_error", "param": "trace_id", "code": "trace_not_found",
			}}, http.StatusNotFound)
			return
		}
		gatewayBadRequest(w, err.Error(), "", "invalid_score")
		return
	}
	httpResponseJSON(w, score, http.StatusCreated)
}
