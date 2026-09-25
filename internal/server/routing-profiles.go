package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ─── Routing profile CRUD API ───
//
// A routing profile is a named, ordered list of provider/model targets that a
// gateway request may name as its model. Expansion happens during chain
// resolution, after which each target passes the unchanged token model-access
// and provider allowlist checks — a profile grants routing, never authorization.

// routingProfileModels returns the token-visible routing profiles as model
// entries for GET /gateway/v1/models.
//
// A profile is listed when at least one of its targets is usable by this token:
// listing one that would be refused on use is the failure mode ListModels
// already avoids for disabled providers. A lookup failure lists nothing rather
// than failing the call — the endpoint's job is to advertise what is routable,
// and provider models are unaffected.
func (s *Server) routingProfileModels(ctx context.Context, auth *authResult) []ModelData {
	store, ok := s.routingProfileStore.(service.GatewayRoutingProfileStorer)
	if !ok || auth == nil || auth.token == nil || auth.token.WorkspaceID == "" {
		return nil
	}

	profiles, err := store.ListGatewayRoutingProfiles(ctx, auth.token.WorkspaceID)
	if err != nil {
		slog.Error("list routing profiles for models failed", "error", err)
		return nil
	}

	out := make([]ModelData, 0, len(profiles))
	for _, profile := range profiles {
		usable := false
		for _, target := range profile.Targets {
			if _, _, _, resolveErr := s.resolveModel(ctx, auth, target); resolveErr == nil {
				usable = true
				break
			}
		}
		if !usable {
			continue
		}
		out = append(out, ModelData{
			ID:             profile.Name,
			Object:         "model",
			OwnedBy:        "at-routing-profile",
			RoutingProfile: true,
		})
	}

	return out
}

// ListRoutingProfilesAPI handles GET /api/v1/routing-profiles.
func (s *Server) ListRoutingProfilesAPI(w http.ResponseWriter, r *http.Request) {
	if s.routingProfileStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.routingProfileStore.ListRoutingProfiles(r.Context(), q)
	if err != nil {
		slog.Error("list routing profiles failed", "error", err)
		workspaceError(w, err)
		return
	}

	if records == nil {
		records = &service.ListResult[service.RoutingProfile]{Data: []service.RoutingProfile{}}
	}
	if records.Data == nil {
		records.Data = []service.RoutingProfile{}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetRoutingProfileAPI handles GET /api/v1/routing-profiles/{id}.
func (s *Server) GetRoutingProfileAPI(w http.ResponseWriter, r *http.Request) {
	if s.routingProfileStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "routing profile id is required", http.StatusBadRequest)
		return
	}

	record, err := s.routingProfileStore.GetRoutingProfile(r.Context(), id)
	if err != nil {
		slog.Error("get routing profile failed", "id", id, "error", err)
		workspaceError(w, err)
		return
	}
	if record == nil {
		httpResponse(w, fmt.Sprintf("routing profile %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateRoutingProfileAPI handles POST /api/v1/routing-profiles.
func (s *Server) CreateRoutingProfileAPI(w http.ResponseWriter, r *http.Request) {
	if s.routingProfileStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.RoutingProfile
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	req = service.NormalizeRoutingProfile(req)
	if err := service.ValidateRoutingProfile(req); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Reject a duplicate name with 409 rather than letting the UNIQUE constraint
	// surface as an opaque 500. The constraint is still the authority under a
	// concurrent create; this only improves the common case.
	existing, err := s.routingProfileStore.GetRoutingProfileByName(r.Context(), req.Name)
	if err != nil {
		slog.Error("check routing profile name failed", "name", req.Name, "error", err)
		workspaceError(w, err)
		return
	}
	if existing != nil {
		httpResponse(w, fmt.Sprintf("routing profile %q already exists", req.Name), http.StatusConflict)
		return
	}

	userEmail := s.getUserEmail(r)
	req.CreatedBy = userEmail
	req.UpdatedBy = userEmail

	record, err := s.routingProfileStore.CreateRoutingProfile(r.Context(), req)
	if err != nil {
		if isUniqueViolation(err) {
			httpResponse(w, fmt.Sprintf("routing profile %q already exists", req.Name), http.StatusConflict)
			return
		}
		slog.Error("create routing profile failed", "name", req.Name, "error", err)
		workspaceError(w, err)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateRoutingProfileAPI handles PUT /api/v1/routing-profiles/{id}.
func (s *Server) UpdateRoutingProfileAPI(w http.ResponseWriter, r *http.Request) {
	if s.routingProfileStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "routing profile id is required", http.StatusBadRequest)
		return
	}

	var req service.RoutingProfile
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	req = service.NormalizeRoutingProfile(req)
	if err := service.ValidateRoutingProfile(req); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}

	// A rename onto another row's name is a conflict, not a 500. Renaming a
	// profile to its own current name stays valid.
	existing, err := s.routingProfileStore.GetRoutingProfileByName(r.Context(), req.Name)
	if err != nil {
		slog.Error("check routing profile name failed", "name", req.Name, "error", err)
		workspaceError(w, err)
		return
	}
	if existing != nil && existing.ID != id {
		httpResponse(w, fmt.Sprintf("routing profile %q already exists", req.Name), http.StatusConflict)
		return
	}

	req.UpdatedBy = s.getUserEmail(r)

	record, err := s.routingProfileStore.UpdateRoutingProfile(r.Context(), id, req)
	if err != nil {
		if isUniqueViolation(err) {
			httpResponse(w, fmt.Sprintf("routing profile %q already exists", req.Name), http.StatusConflict)
			return
		}
		slog.Error("update routing profile failed", "id", id, "error", err)
		workspaceError(w, err)
		return
	}
	if record == nil {
		httpResponse(w, fmt.Sprintf("routing profile %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteRoutingProfileAPI handles DELETE /api/v1/routing-profiles/{id}.
func (s *Server) DeleteRoutingProfileAPI(w http.ResponseWriter, r *http.Request) {
	if s.routingProfileStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "routing profile id is required", http.StatusBadRequest)
		return
	}

	if err := s.routingProfileStore.DeleteRoutingProfile(r.Context(), id); err != nil {
		slog.Error("delete routing profile failed", "id", id, "error", err)
		workspaceError(w, err)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}
