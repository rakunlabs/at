package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

func (s *Server) ListMarketplacesAPI(w http.ResponseWriter, r *http.Request) {
	if s.marketplaceStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.marketplaceStore.ListMarketplaces(r.Context(), q)
	if err != nil {
		slog.Error("list marketplaces failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list marketplaces: %v", err), http.StatusInternalServerError)
		return
	}
	if records == nil {
		records = &service.ListResult[service.Marketplace]{Data: []service.Marketplace{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

func (s *Server) GetMarketplaceAPI(w http.ResponseWriter, r *http.Request) {
	if s.marketplaceStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "marketplace id is required", http.StatusBadRequest)
		return
	}

	record, err := s.marketplaceStore.GetMarketplace(r.Context(), id)
	if err != nil {
		slog.Error("get marketplace failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get marketplace: %v", err), http.StatusInternalServerError)
		return
	}
	if record == nil {
		httpResponse(w, fmt.Sprintf("marketplace %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

func (s *Server) CreateMarketplaceAPI(w http.ResponseWriter, r *http.Request) {
	if s.marketplaceStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.Marketplace
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if err := normalizeMarketplace(&req); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	req.CreatedBy = userEmail
	req.UpdatedBy = userEmail

	record, err := s.marketplaceStore.CreateMarketplace(r.Context(), req)
	if err != nil {
		slog.Error("create marketplace failed", "name", req.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create marketplace: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

func (s *Server) UpdateMarketplaceAPI(w http.ResponseWriter, r *http.Request) {
	if s.marketplaceStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "marketplace id is required", http.StatusBadRequest)
		return
	}

	var req service.Marketplace
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if err := normalizeMarketplace(&req); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.UpdatedBy = s.getUserEmail(r)

	record, err := s.marketplaceStore.UpdateMarketplace(r.Context(), id, req)
	if err != nil {
		slog.Error("update marketplace failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update marketplace: %v", err), http.StatusInternalServerError)
		return
	}
	if record == nil {
		httpResponse(w, fmt.Sprintf("marketplace %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

func (s *Server) DeleteMarketplaceAPI(w http.ResponseWriter, r *http.Request) {
	if s.marketplaceStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "marketplace id is required", http.StatusBadRequest)
		return
	}

	if err := s.marketplaceStore.DeleteMarketplace(r.Context(), id); err != nil {
		slog.Error("delete marketplace failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete marketplace: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, map[string]string{"status": "deleted"}, http.StatusOK)
}

func normalizeMarketplace(m *service.Marketplace) error {
	name := strings.TrimSpace(m.Name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	m.Name = slugifyClaudeName(name, "market")
	m.Description = strings.TrimSpace(m.Description)
	m.Skills = normalizeMarketplaceRefs(m.Skills)
	m.MCPServers = normalizeMarketplaceRefs(m.MCPServers)
	m.DirectMCPServers = normalizeMarketplaceMCPServers(m.DirectMCPServers)

	return nil
}

func normalizeMarketplaceRefs(refs []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func normalizeMarketplaceMCPServers(items []service.MarketplaceMCPServer) []service.MarketplaceMCPServer {
	seen := map[string]bool{}
	out := make([]service.MarketplaceMCPServer, 0, len(items))
	for _, item := range items {
		item.Name = slugifyClaudeName(strings.TrimSpace(item.Name), "mcp")
		item.Description = strings.TrimSpace(item.Description)
		item.Type = strings.TrimSpace(item.Type)
		item.URL = strings.TrimSpace(item.URL)
		item.Command = strings.TrimSpace(item.Command)
		if item.URL == "" && item.Command == "" {
			continue
		}
		if item.Type == "" && item.URL != "" {
			item.Type = "http"
		}
		if seen[item.Name] {
			continue
		}
		seen[item.Name] = true
		out = append(out, item)
	}
	if out == nil {
		out = []service.MarketplaceMCPServer{}
	}
	return out
}
