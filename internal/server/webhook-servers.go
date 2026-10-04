package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/rakunlabs/at/internal/service"
)

type webhookServersResponse struct {
	Servers []service.WebhookServer `json:"servers"`
}

func (s *Server) webhookServerStore(w http.ResponseWriter) service.WebhookServerStorer {
	store, ok := s.store.(service.WebhookServerStorer)
	if !ok {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return nil
	}
	return store
}

func webhookServerError(w http.ResponseWriter, err error, action string) {
	switch {
	case errors.Is(err, service.ErrWebhookServerConflict), errors.Is(err, service.ErrWebhookRouteConflict):
		httpResponse(w, err.Error(), http.StatusConflict)
	case errors.Is(err, service.ErrAccessResourceNotFound):
		httpResponse(w, "webhook server not found", http.StatusNotFound)
	case errors.Is(err, service.ErrAccessDenied), errors.Is(err, service.ErrWorkspaceRequired):
		httpResponse(w, "webhook servers are managed by installation administrators", http.StatusForbidden)
	default:
		slog.Error("webhook server "+action+" failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to %s webhook server", action), http.StatusInternalServerError)
	}
}

func (s *Server) withWebhookStatus(v service.WebhookServer) service.WebhookServer {
	st := s.webhookListeners.Status(v.ID)
	if !v.Enabled {
		st = service.WebhookServerStatus{State: "disabled"}
	}
	v.Status = &st
	return v
}

// ListWebhookServersAPI handles GET /api/v1/webhook-servers. Administrators
// get the catalog; workspace members get the servers open to their workspace.
func (s *Server) ListWebhookServersAPI(w http.ResponseWriter, r *http.Request) {
	store := s.webhookServerStore(w)
	if store == nil {
		return
	}
	list, err := store.ListWebhookServers(r.Context())
	if err != nil {
		webhookServerError(w, err, "list")
		return
	}
	for i := range list {
		list[i] = s.withWebhookStatus(list[i])
	}
	httpResponseJSON(w, webhookServersResponse{Servers: list}, http.StatusOK)
}

// GetWebhookServerAPI handles GET /api/v1/webhook-servers/{id}.
func (s *Server) GetWebhookServerAPI(w http.ResponseWriter, r *http.Request) {
	store := s.webhookServerStore(w)
	if store == nil {
		return
	}
	v, err := store.GetWebhookServer(r.Context(), r.PathValue("id"))
	if err != nil {
		webhookServerError(w, err, "get")
		return
	}
	if v == nil {
		httpResponse(w, "webhook server not found", http.StatusNotFound)
		return
	}
	httpResponseJSON(w, s.withWebhookStatus(*v), http.StatusOK)
}

// CreateWebhookServerAPI handles POST /api/v1/webhook-servers.
func (s *Server) CreateWebhookServerAPI(w http.ResponseWriter, r *http.Request) {
	store := s.webhookServerStore(w)
	if store == nil {
		return
	}
	var req service.WebhookServer
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if err := s.checkWebhookServerPort(req); err != nil {
		httpResponse(w, err.Error(), http.StatusConflict)
		return
	}
	v, err := store.CreateWebhookServer(r.Context(), req)
	if err != nil {
		webhookServerError(w, err, "create")
		return
	}
	s.webhookListeners.Reload(s.ctx)
	httpResponseJSON(w, s.withWebhookStatus(*v), http.StatusCreated)
}

// UpdateWebhookServerAPI handles PUT /api/v1/webhook-servers/{id}. The TLS
// key "***" keeps the stored key.
func (s *Server) UpdateWebhookServerAPI(w http.ResponseWriter, r *http.Request) {
	store := s.webhookServerStore(w)
	if store == nil {
		return
	}
	var req service.WebhookServer
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if err := s.checkWebhookServerPort(req); err != nil {
		httpResponse(w, err.Error(), http.StatusConflict)
		return
	}
	v, err := store.UpdateWebhookServer(r.Context(), r.PathValue("id"), req)
	if err != nil {
		webhookServerError(w, err, "update")
		return
	}
	if v == nil {
		httpResponse(w, "webhook server not found", http.StatusNotFound)
		return
	}
	s.webhookListeners.Reload(s.ctx)
	httpResponseJSON(w, s.withWebhookStatus(*v), http.StatusOK)
}

// DeleteWebhookServerAPI handles DELETE /api/v1/webhook-servers/{id}. Bound
// webhooks lose that route; they stay on their other servers and on the main
// route unless hidden from it.
func (s *Server) DeleteWebhookServerAPI(w http.ResponseWriter, r *http.Request) {
	store := s.webhookServerStore(w)
	if store == nil {
		return
	}
	if err := store.DeleteWebhookServer(r.Context(), r.PathValue("id")); err != nil {
		webhookServerError(w, err, "delete")
		return
	}
	s.webhookListeners.Reload(s.ctx)
	httpResponse(w, "deleted", http.StatusOK)
}

// ReloadWebhookServersAPI handles POST /api/v1/webhook-servers/reload, which
// retries listeners whose port was busy without editing them.
func (s *Server) ReloadWebhookServersAPI(w http.ResponseWriter, r *http.Request) {
	if s.webhookServerStore(w) == nil {
		return
	}
	s.webhookListeners.Reload(s.ctx)
	s.ListWebhookServersAPI(w, r)
}

// checkWebhookServerPort refuses the main server's own port, which would make
// the dedicated listener fail to bind on every replica.
func (s *Server) checkWebhookServerPort(v service.WebhookServer) error {
	if strconv.Itoa(v.Port) == s.config.Port {
		return fmt.Errorf("port %d is the main server's port", v.Port)
	}
	return nil
}

// ListWebhookDeliveriesAPI handles GET /api/v1/triggers/{id}/deliveries.
func (s *Server) ListWebhookDeliveriesAPI(w http.ResponseWriter, r *http.Request) {
	store := s.webhookServerStore(w)
	if store == nil {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := store.ListWebhookDeliveries(r.Context(), r.PathValue("id"), limit)
	if err != nil {
		if errors.Is(err, service.ErrAccessResourceNotFound) {
			httpResponse(w, "trigger not found", http.StatusNotFound)
			return
		}
		if workspaceBusinessError(w, err) {
			return
		}
		slog.Error("list webhook deliveries failed", "trigger_id", r.PathValue("id"), "error", err)
		httpResponse(w, "failed to list deliveries", http.StatusInternalServerError)
		return
	}
	httpResponseJSON(w, map[string]any{"deliveries": list}, http.StatusOK)
}
