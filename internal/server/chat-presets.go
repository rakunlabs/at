package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
)

// chatPresetsKey names the per-account list of named Chats setups inside the
// existing user_preferences table, so saving several workbench configurations
// needs no migration and no new table.
//
// The key keeps the historical `playground_` prefix used by every other Chats
// preference: the table is keyed by name, and renaming would orphan the rows
// an installation has already written.
const chatPresetsKey = "playground_presets"

// chatPresetsMaxBytes bounds the stored blob. Per-entry bounds are enforced by
// service.NormalizeChatPresets; this is the backstop for the whole list.
const chatPresetsMaxBytes = 256 << 10

// chatPresetRegistry is the stored shape. A wrapper object rather than a bare
// array, so a later field does not need a value migration.
type chatPresetRegistry struct {
	Presets []service.ChatPreset `json:"presets"`
}

// loadChatPresets reads the owner's presets. A missing row is an empty list,
// not an error: the page always asks.
func (s *Server) loadChatPresets(ctx context.Context, owner string) ([]service.ChatPreset, error) {
	pref, err := s.userPrefStore.GetUserPreference(ctx, owner, chatPresetsKey)
	if err != nil {
		return nil, err
	}
	if pref == nil || len(pref.Value) == 0 {
		return nil, nil
	}

	var stored chatPresetRegistry
	if err := json.Unmarshal(pref.Value, &stored); err != nil {
		// A list written by a newer client must not break the page.
		return nil, nil
	}

	return stored.Presets, nil
}

// ChatPresetsAPI handles GET and PUT /api/v1/chats/presets.
//
// The owner is resolved by playgroundAccess (the authenticated subject) and is
// never read from the request, matching the rest of the Chats surface: these
// are per-account rows, and the installation-wide `/user-preferences`
// endpoints — which take a user_id from the caller — stay administration.
//
// PUT replaces the whole list rather than patching one entry, the same shape
// the local-MCP registry uses: the page holds every preset anyway, and one
// write keeps add, rename, overwrite and delete from needing four endpoints
// with four separate admission entries. The cost is that two tabs editing
// presets concurrently resolve last-writer-wins, which for a personal picker
// is a better trade than a version column on a preference row.
func (s *Server) ChatPresetsAPI(w http.ResponseWriter, r *http.Request) {
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
		stored, err := s.loadChatPresets(r.Context(), owner)
		if err != nil {
			nativeError(w, http.StatusServiceUnavailable, "preference storage unavailable")
			return
		}
		// `[]`, never `null`: the page iterates the list without a nil check.
		if stored == nil {
			stored = []service.ChatPreset{}
		}
		httpResponseJSON(w, chatPresetRegistry{Presets: stored}, http.StatusOK)
	case http.MethodPut:
		var req chatPresetRegistry
		if !decodePlaygroundBody(w, r, &req) {
			return
		}

		stored, err := s.loadChatPresets(r.Context(), owner)
		if err != nil {
			nativeError(w, http.StatusServiceUnavailable, "preference storage unavailable")
			return
		}

		normalized, err := service.NormalizeChatPresets(stampChatPresets(stored, req.Presets))
		if err != nil {
			nativeError(w, http.StatusBadRequest, err.Error())
			return
		}

		encoded, err := json.Marshal(chatPresetRegistry{Presets: normalized})
		if err != nil {
			nativeError(w, http.StatusBadRequest, "invalid chat presets")
			return
		}
		if len(encoded) > chatPresetsMaxBytes {
			nativeError(w, http.StatusRequestEntityTooLarge, "chat presets are too large")
			return
		}

		if err := s.userPrefStore.SetUserPreference(r.Context(), service.UserPreference{
			UserID: owner,
			Key:    chatPresetsKey,
			Value:  json.RawMessage(encoded),
		}); err != nil {
			nativeError(w, http.StatusServiceUnavailable, "preference storage unavailable")
			return
		}

		httpResponseJSON(w, chatPresetRegistry{Presets: normalized}, http.StatusOK)
	default:
		nativeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// stampChatPresets resolves a submitted list against what is stored: it mints
// an ID for a new entry, preserves the original creation time of an existing
// one and stamps the update time. Identity is the server's to assign, so a
// client cannot claim another entry's id or backdate its own.
func stampChatPresets(stored, submitted []service.ChatPreset) []service.ChatPreset {
	byID := make(map[string]service.ChatPreset, len(stored))
	for _, preset := range stored {
		byID[preset.ID] = preset
	}

	now := time.Now().UTC().Format(time.RFC3339)
	out := make([]service.ChatPreset, 0, len(submitted))
	for _, preset := range submitted {
		prev, existed := byID[preset.ID]
		if preset.ID == "" || !existed {
			preset.ID = ulid.Make().String()
			preset.CreatedAt = now
		} else {
			preset.CreatedAt = prev.CreatedAt
		}
		preset.UpdatedAt = now
		out = append(out, preset)
	}

	return out
}
