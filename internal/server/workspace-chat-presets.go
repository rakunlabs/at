package server

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

type workspaceChatPresetInput struct {
	Name string `json:"name"`
	service.ChatWorkbenchSetup
}

type workspaceChatPresetList struct {
	Presets []service.WorkspaceChatPreset `json:"presets"`
}

// WorkspaceChatPresetsAPI handles the workspace-shared half of the Chats preset
// picker. Applying is client-side and open to every admitted reader; writes are
// owner-only in the store, with ownership always stamped from the principal.
func (s *Server) WorkspaceChatPresetsAPI(w http.ResponseWriter, r *http.Request) {
	_, owner := s.playgroundAccess(w, r)
	if owner == "" {
		return
	}
	store, ok := s.store.(service.WorkspaceChatPresetStorer)
	if !ok {
		nativeError(w, http.StatusServiceUnavailable, "workspace preset storage unavailable")
		return
	}
	actor, ok := service.AccessPrincipalFromContext(r.Context())
	if !ok || actor.WorkspaceID == "" {
		nativeError(w, http.StatusForbidden, "workspace selection is required")
		return
	}

	switch r.Method {
	case http.MethodGet:
		presets, err := store.ListWorkspaceChatPresets(r.Context(), actor.WorkspaceID)
		if err != nil {
			workspaceChatPresetError(w, err)
			return
		}
		if presets == nil {
			presets = []service.WorkspaceChatPreset{}
		}
		httpResponseJSON(w, workspaceChatPresetList{Presets: presets}, http.StatusOK)

	case http.MethodPost:
		input, ok := decodeWorkspaceChatPresetInput(w, r)
		if !ok {
			return
		}
		created, err := store.CreateWorkspaceChatPreset(r.Context(), service.WorkspaceChatPreset{
			ChatPreset:  input,
			WorkspaceID: actor.WorkspaceID,
		})
		if err != nil {
			workspaceChatPresetError(w, err)
			return
		}
		httpResponseJSON(w, created, http.StatusCreated)

	case http.MethodPut:
		id := r.PathValue("id")
		if id == "" {
			nativeError(w, http.StatusBadRequest, "workspace preset id is required")
			return
		}
		input, ok := decodeWorkspaceChatPresetInput(w, r)
		if !ok {
			return
		}
		updated, err := store.UpdateWorkspaceChatPreset(r.Context(), id, service.WorkspaceChatPreset{
			ChatPreset:  input,
			WorkspaceID: actor.WorkspaceID,
		})
		if err != nil {
			workspaceChatPresetError(w, err)
			return
		}
		if updated == nil {
			nativeError(w, http.StatusNotFound, "workspace preset not found or not owned by you")
			return
		}
		httpResponseJSON(w, updated, http.StatusOK)

	case http.MethodDelete:
		id := r.PathValue("id")
		if id == "" {
			nativeError(w, http.StatusBadRequest, "workspace preset id is required")
			return
		}
		if err := store.DeleteWorkspaceChatPreset(r.Context(), id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				nativeError(w, http.StatusNotFound, "workspace preset not found or not owned by you")
				return
			}
			workspaceChatPresetError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		nativeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func decodeWorkspaceChatPresetInput(w http.ResponseWriter, r *http.Request) (service.ChatPreset, bool) {
	var req workspaceChatPresetInput
	if !decodePlaygroundBody(w, r, &req) {
		return service.ChatPreset{}, false
	}
	normalized, err := service.NormalizeChatPresets([]service.ChatPreset{{Name: req.Name, ChatWorkbenchSetup: req.ChatWorkbenchSetup}})
	if err != nil {
		nativeError(w, http.StatusBadRequest, err.Error())
		return service.ChatPreset{}, false
	}

	return normalized[0], true
}

func workspaceChatPresetError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrChatPresetNameConflict):
		nativeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, service.ErrAccessDenied):
		nativeError(w, http.StatusForbidden, "workspace preset access denied")
	default:
		nativeError(w, http.StatusServiceUnavailable, "workspace preset storage unavailable")
	}
}
