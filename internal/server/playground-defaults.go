package server

import (
	"encoding/json"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

// playgroundDefaultsKey names the per-account Playground preset inside the
// existing user_preferences table, so remembering a workbench setup needs no
// migration and no new table.
const playgroundDefaultsKey = "playground_defaults"

// playgroundDefaultsMaxBytes bounds the stored preset. It holds selections
// (model, agent, tool names), never transcripts, so this is generous.
const playgroundDefaultsMaxBytes = 64 << 10

// playgroundDefaults is the caller's saved starting point for a *new*
// conversation. Existing conversations keep their own persisted config; this
// only seeds the next one, which is what makes a tuned setup survive "New
// chat" instead of resetting to the alphabetically first model.
//
// It is an alias, not a copy, of the selection payload a named preset carries
// (see service.ChatPreset): a preset is a default with a name, so the two wire
// shapes must not be able to drift apart.
type playgroundDefaults = service.ChatWorkbenchSetup

// PlaygroundDefaultsAPI handles GET and PUT /api/v1/chats/defaults.
//
// The owner is resolved by playgroundAccess (the authenticated subject) and is
// never read from the request, matching the rest of the Playground surface:
// these are per-account rows, and the installation-wide `/user-preferences`
// endpoints — which take a user_id from the caller — stay administration.
func (s *Server) PlaygroundDefaultsAPI(w http.ResponseWriter, r *http.Request) {
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
		pref, err := s.userPrefStore.GetUserPreference(r.Context(), owner, playgroundDefaultsKey)
		if err != nil {
			nativeError(w, http.StatusServiceUnavailable, "preference storage unavailable")
			return
		}
		// An account that has never saved one gets the empty preset rather
		// than a 404: the client always asks, and "no preset" is not an error.
		out := playgroundDefaults{}
		if pref != nil && len(pref.Value) > 0 {
			if err := json.Unmarshal(pref.Value, &out); err != nil {
				// A preset written by a newer client must not break the page.
				out = playgroundDefaults{}
			}
		}
		httpResponseJSON(w, out, http.StatusOK)
	case http.MethodPut:
		var req playgroundDefaults
		if !decodePlaygroundBody(w, r, &req) {
			return
		}
		encoded, err := json.Marshal(req)
		if err != nil {
			nativeError(w, http.StatusBadRequest, "invalid playground defaults")
			return
		}
		if len(encoded) > playgroundDefaultsMaxBytes {
			nativeError(w, http.StatusRequestEntityTooLarge, "playground defaults are too large")
			return
		}
		if err := s.userPrefStore.SetUserPreference(r.Context(), service.UserPreference{
			UserID: owner,
			Key:    playgroundDefaultsKey,
			Value:  json.RawMessage(encoded),
		}); err != nil {
			nativeError(w, http.StatusServiceUnavailable, "preference storage unavailable")
			return
		}
		httpResponseJSON(w, req, http.StatusOK)
	default:
		nativeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
