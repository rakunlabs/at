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

	// Check previous state for lifecycle changes.
	previous, _ := s.botConfigStore.GetBotConfig(r.Context(), id)

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

	// Handle lifecycle changes on update.
	wasRunning := s.isBotRunning(id)
	if record.Enabled && record.Token != "" && !wasRunning {
		// Enabled was turned on — start the bot.
		s.startBotFromConfig(s.ctx, record)
	} else if !record.Enabled && wasRunning {
		// Enabled was turned off — stop the bot.
		s.stopBot(id)
	} else if wasRunning && previous != nil && (previous.Token != record.Token || previous.Platform != record.Platform) {
		// Token or platform changed while running — restart.
		s.stopBot(id)
		if record.Enabled && record.Token != "" {
			s.startBotFromConfig(s.ctx, record)
		}
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

	// Stop the bot if running.
	s.stopBot(id)

	httpResponse(w, "deleted", http.StatusOK)
}

// ─── Bot Lifecycle API ───

// StartBotAPI handles POST /api/v1/bots/{id}/start.
func (s *Server) StartBotAPI(w http.ResponseWriter, r *http.Request) {
	if s.botConfigStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	record, err := s.botConfigStore.GetBotConfig(r.Context(), id)
	if err != nil {
		slog.Error("start bot: get config failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get bot config: %v", err), http.StatusInternalServerError)
		return
	}
	if record == nil {
		httpResponse(w, fmt.Sprintf("bot %q not found", id), http.StatusNotFound)
		return
	}

	if record.Token == "" {
		httpResponse(w, "bot has no token configured", http.StatusBadRequest)
		return
	}

	if s.isBotRunning(id) {
		httpResponse(w, "bot is already running", http.StatusConflict)
		return
	}

	s.startBotFromConfig(s.ctx, record)

	// Update enabled flag in DB.
	if !record.Enabled {
		record.Enabled = true
		record.UpdatedBy = s.getUserEmail(r)
		s.botConfigStore.UpdateBotConfig(r.Context(), id, *record)
	}

	httpResponseJSON(w, map[string]any{
		"status":  "running",
		"message": fmt.Sprintf("bot %q started", record.Name),
	}, http.StatusOK)
}

// StopBotAPI handles POST /api/v1/bots/{id}/stop.
func (s *Server) StopBotAPI(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if !s.stopBot(id) {
		httpResponse(w, "bot is not running", http.StatusConflict)
		return
	}

	// Update enabled flag in DB.
	if s.botConfigStore != nil {
		record, _ := s.botConfigStore.GetBotConfig(r.Context(), id)
		if record != nil && record.Enabled {
			record.Enabled = false
			record.UpdatedBy = s.getUserEmail(r)
			s.botConfigStore.UpdateBotConfig(r.Context(), id, *record)
		}
	}

	httpResponseJSON(w, map[string]any{
		"status":  "stopped",
		"message": "bot stopped",
	}, http.StatusOK)
}

// GetBotStatusAPI handles GET /api/v1/bots/{id}/status.
func (s *Server) GetBotStatusAPI(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	rb := s.getBotRunningInfo(id)
	if rb != nil {
		httpResponseJSON(w, map[string]any{
			"running":    true,
			"platform":   rb.platform,
			"started_at": rb.startedAt,
		}, http.StatusOK)
	} else {
		httpResponseJSON(w, map[string]any{
			"running": false,
		}, http.StatusOK)
	}
}
