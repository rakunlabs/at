package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

func providerGovernanceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrAccessResourceNotFound):
		httpResponse(w, "resource not found", http.StatusNotFound)
	case errors.Is(err, service.ErrAccessDenied), errors.Is(err, service.ErrWorkspaceRequired):
		httpResponse(w, "access denied", http.StatusForbidden)
	default:
		httpResponse(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) GetProviderBudgetAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.ProviderBudgetStorer)
	if !ok {
		httpResponse(w, "provider budget store unavailable", http.StatusServiceUnavailable)
		return
	}
	kind, id := r.PathValue("kind"), r.PathValue("id")
	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	if userID == "self" {
		if a, found := service.AccessPrincipalFromContext(r.Context()); found {
			userID = a.UserID
		}
	}
	status, err := store.GetProviderBudgetStatus(r.Context(), kind, id, userID)
	if err != nil {
		providerGovernanceError(w, err)
		return
	}
	if status == nil {
		httpResponseJSON(w, nil, http.StatusOK)
		return
	}
	httpResponseJSON(w, status, http.StatusOK)
}

func (s *Server) SaveProviderBudgetAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.ProviderBudgetStorer)
	if !ok {
		httpResponse(w, "provider budget store unavailable", http.StatusServiceUnavailable)
		return
	}
	var policy service.ProviderBudgetPolicy
	if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	policy.ResourceKind, policy.ResourceID = r.PathValue("kind"), r.PathValue("id")
	record, err := store.SaveProviderBudgetPolicy(r.Context(), policy)
	if err != nil {
		if errors.Is(err, service.ErrAccessDenied) || errors.Is(err, service.ErrAccessResourceNotFound) {
			providerGovernanceError(w, err)
		} else {
			httpResponse(w, err.Error(), http.StatusBadRequest)
		}
		return
	}
	httpResponseJSON(w, record, http.StatusOK)
}

func (s *Server) DeleteProviderBudgetAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.ProviderBudgetStorer)
	if !ok {
		httpResponse(w, "provider budget store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := store.DeleteProviderBudgetPolicy(r.Context(), r.PathValue("kind"), r.PathValue("id")); err != nil {
		providerGovernanceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ListProviderBudgetOverridesAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.ProviderBudgetStorer)
	if !ok {
		httpResponse(w, "provider budget store unavailable", http.StatusServiceUnavailable)
		return
	}
	rows, err := store.ListProviderBudgetOverrides(r.Context(), r.PathValue("policy_id"))
	if err != nil {
		providerGovernanceError(w, err)
		return
	}
	if rows == nil {
		rows = []service.ProviderBudgetOverride{}
	}
	httpResponseJSON(w, map[string]any{"items": rows}, http.StatusOK)
}

func (s *Server) SaveProviderBudgetOverrideAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.ProviderBudgetStorer)
	if !ok {
		httpResponse(w, "provider budget store unavailable", http.StatusServiceUnavailable)
		return
	}
	var override service.ProviderBudgetOverride
	if err := json.NewDecoder(r.Body).Decode(&override); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	override.PolicyID = r.PathValue("policy_id")
	if err := store.SaveProviderBudgetOverride(r.Context(), override); err != nil {
		if errors.Is(err, service.ErrAccessDenied) || errors.Is(err, service.ErrAccessResourceNotFound) {
			providerGovernanceError(w, err)
		} else {
			httpResponse(w, err.Error(), http.StatusBadRequest)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) DeleteProviderBudgetOverrideAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.ProviderBudgetStorer)
	if !ok {
		httpResponse(w, "provider budget store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := store.DeleteProviderBudgetOverride(r.Context(), r.PathValue("policy_id"), r.PathValue("user_id")); err != nil {
		providerGovernanceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ListVirtualProvidersAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.VirtualProviderStorer)
	if !ok {
		httpResponse(w, "virtual provider store unavailable", http.StatusServiceUnavailable)
		return
	}
	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}
	result, err := store.ListVirtualProviders(r.Context(), q)
	if err != nil {
		providerGovernanceError(w, err)
		return
	}
	httpResponseJSON(w, result, http.StatusOK)
}

func (s *Server) GetVirtualProviderAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.VirtualProviderStorer)
	if !ok {
		httpResponse(w, "virtual provider store unavailable", http.StatusServiceUnavailable)
		return
	}
	record, err := store.GetVirtualProvider(r.Context(), r.PathValue("id"))
	if err != nil {
		providerGovernanceError(w, err)
		return
	}
	if record == nil {
		httpResponse(w, "virtual provider not found", http.StatusNotFound)
		return
	}
	httpResponseJSON(w, record, http.StatusOK)
}

func decodeVirtualProvider(w http.ResponseWriter, r *http.Request) (service.VirtualProvider, bool) {
	var record service.VirtualProvider
	if err := json.NewDecoder(r.Body).Decode(&record); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return service.VirtualProvider{}, false
	}
	if err := service.ValidateVirtualProvider(record); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return service.VirtualProvider{}, false
	}
	return record, true
}

func (s *Server) CreateVirtualProviderAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.VirtualProviderStorer)
	if !ok {
		httpResponse(w, "virtual provider store unavailable", http.StatusServiceUnavailable)
		return
	}
	record, ok := decodeVirtualProvider(w, r)
	if !ok {
		return
	}
	created, err := store.CreateVirtualProvider(r.Context(), record)
	if err != nil {
		if errors.Is(err, service.ErrAccessDenied) {
			providerGovernanceError(w, err)
		} else {
			httpResponse(w, err.Error(), http.StatusBadRequest)
		}
		return
	}
	httpResponseJSON(w, created, http.StatusCreated)
}

func (s *Server) UpdateVirtualProviderAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.VirtualProviderStorer)
	if !ok {
		httpResponse(w, "virtual provider store unavailable", http.StatusServiceUnavailable)
		return
	}
	record, ok := decodeVirtualProvider(w, r)
	if !ok {
		return
	}
	updated, err := store.UpdateVirtualProvider(r.Context(), r.PathValue("id"), record)
	if err != nil {
		if errors.Is(err, service.ErrAccessDenied) || errors.Is(err, service.ErrAccessResourceNotFound) {
			providerGovernanceError(w, err)
		} else {
			httpResponse(w, err.Error(), http.StatusBadRequest)
		}
		return
	}
	httpResponseJSON(w, updated, http.StatusOK)
}

func (s *Server) DeleteVirtualProviderAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.VirtualProviderStorer)
	if !ok {
		httpResponse(w, "virtual provider store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := store.DeleteVirtualProvider(r.Context(), r.PathValue("id")); err != nil {
		providerGovernanceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ListVirtualProviderGrantsAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.VirtualProviderStorer)
	if !ok {
		httpResponse(w, "virtual provider store unavailable", http.StatusServiceUnavailable)
		return
	}
	rows, err := store.ListVirtualProviderGrants(r.Context(), r.PathValue("id"))
	if err != nil {
		providerGovernanceError(w, err)
		return
	}
	httpResponseJSON(w, map[string]any{"items": rows}, http.StatusOK)
}

func (s *Server) SaveVirtualProviderGrantAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.VirtualProviderStorer)
	if !ok {
		httpResponse(w, "virtual provider store unavailable", http.StatusServiceUnavailable)
		return
	}
	var grant service.VirtualProviderGrant
	if err := json.NewDecoder(r.Body).Decode(&grant); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	grant.VirtualProviderID = r.PathValue("id")
	record, err := store.SaveVirtualProviderGrant(r.Context(), grant)
	if err != nil {
		if errors.Is(err, service.ErrAccessDenied) || errors.Is(err, service.ErrAccessResourceNotFound) {
			providerGovernanceError(w, err)
		} else {
			httpResponse(w, err.Error(), http.StatusBadRequest)
		}
		return
	}
	httpResponseJSON(w, record, http.StatusOK)
}

func (s *Server) DeleteVirtualProviderGrantAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.VirtualProviderStorer)
	if !ok {
		httpResponse(w, "virtual provider store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := store.DeleteVirtualProviderGrant(r.Context(), r.PathValue("id"), r.PathValue("workspace_id")); err != nil {
		providerGovernanceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
