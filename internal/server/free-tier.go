package server

import (
	"encoding/json"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

func (s *Server) FreeTierCatalogAPI(w http.ResponseWriter, r *http.Request) {
	httpResponseJSON(w, service.FreeTierCatalog, http.StatusOK)
}

func (s *Server) ProviderUsePolicyAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.ProviderUsePolicyStorer)
	if !ok {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	var policy *service.ProviderUsePolicy
	var err error
	if r.Method == http.MethodPut {
		var input service.ProviderUsePolicy
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768)).Decode(&input) != nil {
			httpResponse(w, "invalid provider policy", http.StatusBadRequest)
			return
		}
		if err := input.Validate(); err != nil {
			httpResponse(w, err.Error(), http.StatusBadRequest)
			return
		}
		policy, err = store.SaveProviderUsePolicy(r.Context(), input)
	} else {
		policy, err = store.GetProviderUsePolicy(r.Context())
	}
	if err != nil {
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, policy, http.StatusOK)
}

func (s *Server) ProviderQuotaUsageAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.ProviderQuotaStorer)
	if !ok {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if key := r.PathValue("key"); key != "" {
		provider, err := s.store.GetProvider(r.Context(), key)
		if err != nil {
			workspaceError(w, err)
			return
		}
		if provider == nil {
			httpResponse(w, "provider not found", http.StatusNotFound)
			return
		}
		id = provider.ID
	}
	usage, err := store.ProviderQuotaUsage(r.Context(), id)
	if err != nil {
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, usage, http.StatusOK)
}

// These routes use the same store guards as the ordinary editor; the account
// can create only its own profile, never a workspace-owned copy via this route.
func (s *Server) CreatePersonalRoutingProfileAPI(w http.ResponseWriter, r *http.Request) {
	if s.routingProfileStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	var input service.RoutingProfile
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768)).Decode(&input) != nil {
		httpResponse(w, "invalid routing profile", http.StatusBadRequest)
		return
	}
	input.Scope = "personal"
	input = service.NormalizeRoutingProfile(input)
	if err := service.ValidateRoutingProfile(input); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	created, err := s.routingProfileStore.CreateRoutingProfile(r.Context(), input)
	if err != nil {
		if isUniqueViolation(err) {
			httpResponse(w, "profile name already in use in this workspace; choose another", http.StatusConflict)
			return
		}
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, created, http.StatusCreated)
}
