package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

func (s *Server) AgentRuntimeSettingsAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentRuntimeSettingsStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	current, err := s.agentRuntimeSettingsStore.GetAgentRuntimeSettings(r.Context())
	if err != nil {
		httpResponse(w, "failed to load agent runtime settings", http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodGet {
		httpResponseJSON(w, current, http.StatusOK)
		return
	}
	if r.Method != http.MethodPut {
		httpResponse(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var requested service.AgentRuntimeSettings
	if err := json.NewDecoder(r.Body).Decode(&requested); err != nil {
		httpResponse(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := requested.Validate(); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	saved, err := s.agentRuntimeSettingsStore.SaveAgentRuntimeSettings(r.Context(), requested)
	if errors.Is(err, service.ErrAgentRuntimeSettingsConflict) {
		httpResponse(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		httpResponse(w, "failed to save agent runtime settings", http.StatusInternalServerError)
		return
	}
	httpResponseJSON(w, saved, http.StatusOK)
}
