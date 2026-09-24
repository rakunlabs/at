package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/rakunlabs/query"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

func (s *Server) personalProviderStore(w http.ResponseWriter) (service.PersonalProviderStorer, bool) {
	store, ok := s.store.(service.PersonalProviderStorer)
	if !ok || s.store == nil {
		httpResponse(w, "personal provider store unavailable", http.StatusServiceUnavailable)
		return nil, false
	}
	return store, true
}

func validatePersonalProviderRequest(w http.ResponseWriter, cfg config.LLMConfig) bool {
	if cfg.Type == "" {
		httpResponse(w, "config.type is required", http.StatusBadRequest)
		return false
	}
	if !service.IsSupportedProviderType(cfg.Type) {
		httpResponse(w, fmt.Sprintf("unsupported config.type %q (supported: %s)", cfg.Type, strings.Join(service.SupportedProviderTypes, ", ")), http.StatusBadRequest)
		return false
	}
	if msg := validateRateLimitConfig(cfg.RateLimit); msg != "" {
		httpResponse(w, msg, http.StatusBadRequest)
		return false
	}
	if msg := validateProviderCredentialsJSON(cfg); msg != "" {
		httpResponse(w, msg, http.StatusBadRequest)
		return false
	}
	return true
}

func (s *Server) ListPersonalProvidersAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.personalProviderStore(w)
	if !ok {
		return
	}
	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}
	result, err := store.ListPersonalProviders(r.Context(), q)
	if err != nil {
		if workspaceBusinessError(w, err) {
			return
		}
		httpResponse(w, fmt.Sprintf("failed to list personal providers: %v", err), http.StatusInternalServerError)
		return
	}
	if result == nil {
		result = &service.ListResult[service.ProviderRecord]{Data: []service.ProviderRecord{}}
	}
	for i := range result.Data {
		redactProviderRecord(&result.Data[i])
	}
	httpResponseJSON(w, result, http.StatusOK)
}

func (s *Server) GetPersonalProviderAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.personalProviderStore(w)
	if !ok {
		return
	}
	record, err := store.GetPersonalProvider(r.Context(), r.PathValue("id"))
	if err != nil {
		if workspaceBusinessError(w, err) {
			return
		}
		httpResponse(w, fmt.Sprintf("failed to get personal provider: %v", err), http.StatusInternalServerError)
		return
	}
	if record == nil {
		httpResponse(w, "personal provider not found", http.StatusNotFound)
		return
	}
	redactProviderRecord(record)
	httpResponseJSON(w, record, http.StatusOK)
}

func (s *Server) CreatePersonalProviderAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.personalProviderStore(w)
	if !ok {
		return
	}
	var req struct {
		Key    string           `json:"key"`
		Scope  string           `json:"scope"`
		Config config.LLMConfig `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	if req.Key == "" {
		httpResponse(w, "key is required", http.StatusBadRequest)
		return
	}
	if req.Scope == "" {
		req.Scope = service.ProviderScopePersonal
	}
	if !service.ValidProviderScope(req.Scope) {
		httpResponse(w, "scope must be personal, workspace or global", http.StatusBadRequest)
		return
	}
	if !validatePersonalProviderRequest(w, req.Config) {
		return
	}
	actor, _ := service.AccessPrincipalFromContext(r.Context())
	user := s.getUserEmail(r)
	record, err := store.CreatePersonalProvider(r.Context(), service.ProviderRecord{
		OwnerUserID: actor.UserID, Key: req.Key, Scope: req.Scope, Config: req.Config, CreatedBy: user, UpdatedBy: user,
	})
	if err != nil {
		if workspaceBusinessError(w, err) {
			return
		}
		httpResponse(w, fmt.Sprintf("failed to create personal provider: %v", err), http.StatusInternalServerError)
		return
	}
	redactProviderRecord(record)
	httpResponseJSON(w, record, http.StatusCreated)
}

func (s *Server) UpdatePersonalProviderAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.personalProviderStore(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var req struct {
		Key                  string           `json:"key"`
		Config               config.LLMConfig `json:"config"`
		ClearCredentialsJSON bool             `json:"clear_credentials_json,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	existing, err := store.GetPersonalProvider(r.Context(), id)
	if err != nil {
		if workspaceBusinessError(w, err) {
			return
		}
		httpResponse(w, fmt.Sprintf("failed to read personal provider: %v", err), http.StatusInternalServerError)
		return
	}
	if existing == nil {
		httpResponse(w, "personal provider not found", http.StatusNotFound)
		return
	}
	if req.Key == "" {
		req.Key = existing.Key
	}
	req.Key = strings.TrimSpace(req.Key)
	if req.Key == "" {
		httpResponse(w, "key is required", http.StatusBadRequest)
		return
	}
	preserveProviderManagedAuth(&req.Config, existing.Config)
	preserveProviderCredentialsJSON(&req.Config, existing.Config, req.ClearCredentialsJSON)
	preserveProviderAvailability(&req.Config, existing.Config)
	if !validatePersonalProviderRequest(w, req.Config) {
		return
	}
	record, err := store.UpdatePersonalProvider(r.Context(), id, service.ProviderRecord{Key: req.Key, Config: req.Config, UpdatedBy: s.getUserEmail(r)})
	if err != nil {
		if workspaceBusinessError(w, err) {
			return
		}
		httpResponse(w, fmt.Sprintf("failed to update personal provider: %v", err), http.StatusInternalServerError)
		return
	}
	if record == nil {
		httpResponse(w, "personal provider not found", http.StatusNotFound)
		return
	}
	redactProviderRecord(record)
	httpResponseJSON(w, record, http.StatusOK)
}

func (s *Server) SetPersonalProviderScopeAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.personalProviderStore(w)
	if !ok {
		return
	}
	var req struct {
		Scope string `json:"scope"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !service.ValidProviderScope(req.Scope) {
		httpResponse(w, "scope must be personal, workspace or global", http.StatusBadRequest)
		return
	}
	record, err := store.SetPersonalProviderScope(r.Context(), r.PathValue("id"), req.Scope, s.getUserEmail(r))
	if err != nil {
		if workspaceBusinessError(w, err) {
			return
		}
		httpResponse(w, fmt.Sprintf("failed to update personal provider scope: %v", err), http.StatusInternalServerError)
		return
	}
	redactProviderRecord(record)
	httpResponseJSON(w, record, http.StatusOK)
}

func (s *Server) SetPersonalProviderDisabledAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.personalProviderStore(w)
	if !ok {
		return
	}
	var req struct {
		Disabled *bool `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Disabled == nil {
		httpResponse(w, "disabled must be a boolean", http.StatusBadRequest)
		return
	}
	if err := store.SetPersonalProviderDisabled(r.Context(), r.PathValue("id"), *req.Disabled, s.getUserEmail(r)); err != nil {
		if workspaceBusinessError(w, err) {
			return
		}
		httpResponse(w, fmt.Sprintf("failed to update personal provider availability: %v", err), http.StatusInternalServerError)
		return
	}
	httpResponseJSON(w, map[string]bool{"disabled": *req.Disabled}, http.StatusOK)
}

func (s *Server) DeletePersonalProviderAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.personalProviderStore(w)
	if !ok {
		return
	}
	if err := store.DeletePersonalProvider(r.Context(), r.PathValue("id")); err != nil {
		if workspaceBusinessError(w, err) {
			return
		}
		httpResponse(w, fmt.Sprintf("failed to delete personal provider: %v", err), http.StatusInternalServerError)
		return
	}
	httpResponse(w, "deleted", http.StatusOK)
}
