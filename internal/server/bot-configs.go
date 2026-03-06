package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ─── Bot Config CRUD ───

// ListBotConfigsAPI handles GET /api/v1/bots.
func (s *Server) ListBotConfigsAPI(w http.ResponseWriter, r *http.Request) {
	if s.botConfigStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.botConfigStore.ListBotConfigs(r.Context(), q)
	if err != nil {
		slog.Error("list bot configs failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list bot configs: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.BotConfig]{Data: []service.BotConfig{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetBotConfigAPI handles GET /api/v1/bots/{id}.
func (s *Server) GetBotConfigAPI(w http.ResponseWriter, r *http.Request) {
	if s.botConfigStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "bot config id is required", http.StatusBadRequest)
		return
	}

	record, err := s.botConfigStore.GetBotConfig(r.Context(), id)
	if err != nil {
		slog.Error("get bot config failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get bot config: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("bot config %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateBotConfigAPI handles POST /api/v1/bots.
func (s *Server) CreateBotConfigAPI(w http.ResponseWriter, r *http.Request) {
	if s.botConfigStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.BotConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Platform == "" {
		httpResponse(w, "platform is required (discord or telegram)", http.StatusBadRequest)
		return
	}

	if req.Token == "" {
		httpResponse(w, "token is required", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	req.CreatedBy = userEmail
	req.UpdatedBy = userEmail

	record, err := s.botConfigStore.CreateBotConfig(r.Context(), req)
	if err != nil {
		slog.Error("create bot config failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to create bot config: %v", err), http.StatusInternalServerError)
		return
	}

	// Start the bot if enabled (use server context, not request context).
	if record.Enabled && record.Token != "" {
		s.startBotFromConfig(s.ctx, record)
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateBotConfigAPI handles PUT /api/v1/bots/{id}.
func (s *Server) UpdateBotConfigAPI(w http.ResponseWriter, r *http.Request) {
	if s.botConfigStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "bot config id is required", http.StatusBadRequest)
		return
	}

	var req service.BotConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	req.UpdatedBy = userEmail

	record, err := s.botConfigStore.UpdateBotConfig(r.Context(), id, req)
	if err != nil {
		slog.Error("update bot config failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update bot config: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("bot config %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteBotConfigAPI handles DELETE /api/v1/bots/{id}.
func (s *Server) DeleteBotConfigAPI(w http.ResponseWriter, r *http.Request) {
	if s.botConfigStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "bot config id is required", http.StatusBadRequest)
		return
	}

	if err := s.botConfigStore.DeleteBotConfig(r.Context(), id); err != nil {
		slog.Error("delete bot config failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete bot config: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}
