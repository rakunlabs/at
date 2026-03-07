package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

// ListUserPreferencesAPI handles GET /api/v1/user-preferences?user_id=...
func (s *Server) ListUserPreferencesAPI(w http.ResponseWriter, r *http.Request) {
	if s.userPrefStore == nil {
		httpResponse(w, "user preference store not configured", http.StatusServiceUnavailable)
		return
	}

	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		httpResponse(w, "user_id query parameter is required", http.StatusBadRequest)
		return
	}

	prefs, err := s.userPrefStore.ListUserPreferences(r.Context(), userID)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to list user preferences: %v", err), http.StatusInternalServerError)
		return
	}

	if prefs == nil {
		prefs = []service.UserPreference{}
	}

	// Redact secret values in the response.
	for i := range prefs {
		if prefs[i].Secret {
			prefs[i].Value = json.RawMessage(`"***"`)
		}
	}

	httpResponseJSON(w, map[string]any{
		"data": prefs,
	}, http.StatusOK)
}

// GetUserPreferenceAPI handles GET /api/v1/user-preferences/{user_id}/{key}
func (s *Server) GetUserPreferenceAPI(w http.ResponseWriter, r *http.Request) {
	if s.userPrefStore == nil {
		httpResponse(w, "user preference store not configured", http.StatusServiceUnavailable)
		return
	}

	userID := r.PathValue("user_id")
	key := r.PathValue("key")

	pref, err := s.userPrefStore.GetUserPreference(r.Context(), userID, key)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to get user preference: %v", err), http.StatusInternalServerError)
		return
	}
	if pref == nil {
		httpResponse(w, "user preference not found", http.StatusNotFound)
		return
	}

	// Redact secret values.
	if pref.Secret {
		pref.Value = json.RawMessage(`"***"`)
	}

	httpResponseJSON(w, pref, http.StatusOK)
}

// SetUserPreferenceAPI handles PUT /api/v1/user-preferences
func (s *Server) SetUserPreferenceAPI(w http.ResponseWriter, r *http.Request) {
	if s.userPrefStore == nil {
		httpResponse(w, "user preference store not configured", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		UserID string          `json:"user_id"`
		Key    string          `json:"key"`
		Value  json.RawMessage `json:"value"`
		Secret bool            `json:"secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if req.UserID == "" || req.Key == "" {
		httpResponse(w, "user_id and key are required", http.StatusBadRequest)
		return
	}
	if len(req.Value) == 0 {
		httpResponse(w, "value is required", http.StatusBadRequest)
		return
	}

	if err := s.userPrefStore.SetUserPreference(r.Context(), service.UserPreference{
		UserID: req.UserID,
		Key:    req.Key,
		Value:  req.Value,
		Secret: req.Secret,
	}); err != nil {
		httpResponse(w, fmt.Sprintf("failed to save user preference: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, map[string]string{"status": "saved"}, http.StatusOK)
}

// DeleteUserPreferenceAPI handles DELETE /api/v1/user-preferences/{user_id}/{key}
func (s *Server) DeleteUserPreferenceAPI(w http.ResponseWriter, r *http.Request) {
	if s.userPrefStore == nil {
		httpResponse(w, "user preference store not configured", http.StatusServiceUnavailable)
		return
	}

	userID := r.PathValue("user_id")
	key := r.PathValue("key")

	if err := s.userPrefStore.DeleteUserPreference(r.Context(), userID, key); err != nil {
		httpResponse(w, fmt.Sprintf("failed to delete user preference: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, map[string]string{"status": "deleted"}, http.StatusOK)
}
