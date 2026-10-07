package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

// chatCommandsKey stores an account's personal Chats commands in
// user_preferences, like presets, so no migration is needed for them.
const chatCommandsKey = "playground_commands"

const chatCommandsMaxBytes = 512 << 10

type chatCommandRegistry struct {
	Commands []service.ChatCommand `json:"commands"`
}

func (s *Server) loadChatCommands(ctx context.Context, owner string) ([]service.ChatCommand, error) {
	pref, err := s.userPrefStore.GetUserPreference(ctx, owner, chatCommandsKey)
	if err != nil {
		return nil, err
	}
	if pref == nil || len(pref.Value) == 0 {
		return nil, nil
	}
	var stored chatCommandRegistry
	if err := json.Unmarshal(pref.Value, &stored); err != nil {
		return nil, nil
	}

	return stored.Commands, nil
}

// ChatCommandsAPI handles GET and PUT /api/v1/chats/commands. The owner is the
// authenticated subject; PUT replaces the whole list (the preset contract).
func (s *Server) ChatCommandsAPI(w http.ResponseWriter, r *http.Request) {
	_, owner := s.playgroundAccess(w, r)
	if owner == "" {
		return
	}
	if s.userPrefStore == nil {
		nativeError(w, http.StatusServiceUnavailable, "preference storage unavailable")
		return
	}

	switch r.Method {
	case http.MethodGet:
		stored, err := s.loadChatCommands(r.Context(), owner)
		if err != nil {
			nativeError(w, http.StatusServiceUnavailable, "preference storage unavailable")
			return
		}
		if stored == nil {
			stored = []service.ChatCommand{}
		}
		httpResponseJSON(w, chatCommandRegistry{Commands: stored}, http.StatusOK)
	case http.MethodPut:
		var req chatCommandRegistry
		if !decodePlaygroundBody(w, r, &req) {
			return
		}
		stored, err := s.loadChatCommands(r.Context(), owner)
		if err != nil {
			nativeError(w, http.StatusServiceUnavailable, "preference storage unavailable")
			return
		}
		normalized, err := service.NormalizeChatCommands(stampChatCommands(stored, req.Commands))
		if err != nil {
			nativeError(w, http.StatusBadRequest, err.Error())
			return
		}
		encoded, err := json.Marshal(chatCommandRegistry{Commands: normalized})
		if err != nil {
			nativeError(w, http.StatusBadRequest, "invalid chat commands")
			return
		}
		if len(encoded) > chatCommandsMaxBytes {
			nativeError(w, http.StatusRequestEntityTooLarge, "chat commands are too large")
			return
		}
		if err := s.userPrefStore.SetUserPreference(r.Context(), service.UserPreference{
			UserID: owner,
			Key:    chatCommandsKey,
			Value:  json.RawMessage(encoded),
		}); err != nil {
			nativeError(w, http.StatusServiceUnavailable, "preference storage unavailable")
			return
		}
		httpResponseJSON(w, chatCommandRegistry{Commands: normalized}, http.StatusOK)
	default:
		nativeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// stampChatCommands assigns server-side identity and timestamps, so a client
// cannot claim another entry's id or backdate its own.
func stampChatCommands(stored, submitted []service.ChatCommand) []service.ChatCommand {
	byID := make(map[string]service.ChatCommand, len(stored))
	for _, c := range stored {
		byID[c.ID] = c
	}
	now := time.Now().UTC().Format(time.RFC3339)
	out := make([]service.ChatCommand, 0, len(submitted))
	for _, c := range submitted {
		prev, existed := byID[c.ID]
		if c.ID == "" || !existed {
			c.ID = ulid.Make().String()
			c.CreatedAt = now
		} else {
			c.CreatedAt = prev.CreatedAt
		}
		c.UpdatedAt = now
		out = append(out, c)
	}

	return out
}

type workspaceChatCommandList struct {
	Commands []service.WorkspaceChatCommand `json:"commands"`
}

// WorkspaceChatCommandsAPI serves the workspace-shared commands. Every Chats
// member may list and run them or publish one; only the creator may change it.
func (s *Server) WorkspaceChatCommandsAPI(w http.ResponseWriter, r *http.Request) {
	_, owner := s.playgroundAccess(w, r)
	if owner == "" {
		return
	}
	store, ok := s.store.(service.WorkspaceChatCommandStorer)
	if !ok {
		nativeError(w, http.StatusServiceUnavailable, "workspace command storage unavailable")
		return
	}
	actor, ok := service.AccessPrincipalFromContext(r.Context())
	if !ok || actor.WorkspaceID == "" {
		nativeError(w, http.StatusForbidden, "workspace selection is required")
		return
	}

	switch r.Method {
	case http.MethodGet:
		commands, err := store.ListWorkspaceChatCommands(r.Context(), actor.WorkspaceID)
		if err != nil {
			workspaceChatCommandError(w, err)
			return
		}
		if commands == nil {
			commands = []service.WorkspaceChatCommand{}
		}
		httpResponseJSON(w, workspaceChatCommandList{Commands: commands}, http.StatusOK)
	case http.MethodPost, http.MethodPut:
		input, ok := decodeWorkspaceChatCommandInput(w, r)
		if !ok {
			return
		}
		record := service.WorkspaceChatCommand{ChatCommand: input, WorkspaceID: actor.WorkspaceID}
		if r.Method == http.MethodPost {
			created, err := store.CreateWorkspaceChatCommand(r.Context(), record)
			if err != nil {
				workspaceChatCommandError(w, err)
				return
			}
			httpResponseJSON(w, created, http.StatusCreated)
			return
		}
		id := r.PathValue("id")
		if id == "" {
			nativeError(w, http.StatusBadRequest, "workspace command id is required")
			return
		}
		updated, err := store.UpdateWorkspaceChatCommand(r.Context(), id, record)
		if err != nil {
			workspaceChatCommandError(w, err)
			return
		}
		if updated == nil {
			nativeError(w, http.StatusNotFound, "workspace command not found or not owned by you")
			return
		}
		httpResponseJSON(w, updated, http.StatusOK)
	case http.MethodDelete:
		id := r.PathValue("id")
		if id == "" {
			nativeError(w, http.StatusBadRequest, "workspace command id is required")
			return
		}
		if err := store.DeleteWorkspaceChatCommand(r.Context(), id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				nativeError(w, http.StatusNotFound, "workspace command not found or not owned by you")
				return
			}
			workspaceChatCommandError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		nativeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func decodeWorkspaceChatCommandInput(w http.ResponseWriter, r *http.Request) (service.ChatCommand, bool) {
	var req service.ChatCommand
	if !decodePlaygroundBody(w, r, &req) {
		return service.ChatCommand{}, false
	}
	normalized, err := service.NormalizeChatCommands([]service.ChatCommand{{
		Name: req.Name, Description: req.Description, Template: req.Template, Model: req.Model,
	}})
	if err != nil {
		nativeError(w, http.StatusBadRequest, err.Error())
		return service.ChatCommand{}, false
	}

	return normalized[0], true
}

func workspaceChatCommandError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrChatCommandNameConflict):
		nativeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, service.ErrAccessDenied):
		nativeError(w, http.StatusForbidden, "workspace command access denied")
	default:
		nativeError(w, http.StatusServiceUnavailable, "workspace command storage unavailable")
	}
}
