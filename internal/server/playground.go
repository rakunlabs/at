package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/rakunlabs/ada/middleware/auth/identity"

	"github.com/rakunlabs/at/internal/service"
)

const (
	// playgroundPayloadMaxBytes caps one serialized `data` (or conversation
	// `config`) payload. Playground transcripts persist text, tool calls and
	// tool results only; inline images and other binary attachments are
	// deliberately NOT persisted, so a megabyte is a generous ceiling that
	// still keeps a single row from becoming a denial-of-service vector.
	playgroundPayloadMaxBytes = 1 << 20
	// playgroundBodyMaxBytes bounds a whole request. A batch append carries
	// several messages, each individually capped at playgroundPayloadMaxBytes.
	playgroundBodyMaxBytes = 8 << 20
	playgroundLimitDefault = 50
	playgroundLimitMax     = 200
	// playgroundMessageBatchMax bounds one append call so a single request
	// cannot allocate an unbounded number of sequences.
	playgroundMessageBatchMax = 200
)

// playgroundListMeta mirrors service.ListMeta but carries a keyset cursor
// instead of a total: playground history is paged backwards, and counting the
// whole owner history on every read would be wasted work.
type playgroundListMeta struct {
	Limit      uint   `json:"limit,omitempty"`
	NextBefore string `json:"next_before,omitempty"`
}

type playgroundList[T any] struct {
	Data []T                `json:"data"`
	Meta playgroundListMeta `json:"meta"`
}

// playgroundAccess mirrors the guard the retired personal chat used: native
// authentication must be configured, the caller must be a native
// administrator, and the store must actually implement playground history.
func (s *Server) playgroundAccess(w http.ResponseWriter, r *http.Request) (service.PlaygroundStorer, string) {
	w.Header().Set("Cache-Control", "no-store")
	// Authentication settings live in the database, so the coordinator is
	// resolved per request; the boot-time field is nil on a normal server.
	if nativeRuntimeFromRequest(r, s.nativeAuth) == nil {
		nativeError(w, http.StatusServiceUnavailable, "playground history requires native authentication")
		return nil, ""
	}
	id := identity.FromContext(r.Context())
	if id == nil || id.Subject == "" || !id.HasRole("admin") {
		nativeError(w, http.StatusForbidden, "playground history requires a native administrator")
		return nil, ""
	}
	if s.store == nil {
		nativeError(w, http.StatusServiceUnavailable, "store not configured")
		return nil, ""
	}
	store, ok := s.store.(service.PlaygroundStorer)
	if !ok {
		nativeError(w, http.StatusServiceUnavailable, "playground history storage unavailable")
		return nil, ""
	}
	return store, id.Subject
}

func playgroundError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrPlaygroundNotFound):
		nativeError(w, http.StatusNotFound, "conversation not found")
	case errors.Is(err, service.ErrPlaygroundConflict):
		nativeError(w, http.StatusConflict, "conversation conflict")
	case errors.Is(err, service.ErrPlaygroundTooLarge):
		nativeError(w, http.StatusRequestEntityTooLarge, "conversation is too large to copy")
	default:
		nativeError(w, http.StatusServiceUnavailable, "playground history storage unavailable")
	}
}

func decodePlaygroundBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, playgroundBodyMaxBytes))
	d.DisallowUnknownFields()
	err := d.Decode(dst)
	if err == nil {
		if trailing := d.Decode(new(any)); !errors.Is(trailing, io.EOF) {
			err = trailing
			if err == nil {
				err = errors.New("trailing JSON")
			}
		}
	}
	if err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			nativeError(w, http.StatusRequestEntityTooLarge, "request body is too large")
			return false
		}
		nativeError(w, http.StatusBadRequest, "invalid playground request")
		return false
	}
	return true
}

// playgroundPayloadFits rejects payloads that would bloat a single row. Images
// are not persisted, so anything this large is a client bug.
func playgroundPayloadFits(w http.ResponseWriter, field string, v map[string]any) bool {
	if v == nil {
		return true
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		nativeError(w, http.StatusBadRequest, "invalid playground "+field)
		return false
	}
	if len(encoded) > playgroundPayloadMaxBytes {
		nativeError(w, http.StatusRequestEntityTooLarge, field+" exceeds the 1 MiB limit; playground history does not persist images or binary attachments")
		return false
	}
	return true
}

func playgroundQueryLimit(r *http.Request) uint {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return playgroundLimitDefault
	}
	n, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || n == 0 {
		return playgroundLimitDefault
	}
	if n > playgroundLimitMax {
		return playgroundLimitMax
	}
	return uint(n)
}

// PlaygroundConversationsAPI handles GET and POST /v1/playground/conversations.
func (s *Server) PlaygroundConversationsAPI(w http.ResponseWriter, r *http.Request) {
	store, owner := s.playgroundAccess(w, r)
	if store == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		limit := playgroundQueryLimit(r)
		before := r.URL.Query().Get("before")
		if len(before) > 128 {
			nativeError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		items, err := store.ListPlaygroundConversations(r.Context(), owner, before, limit)
		if err != nil {
			playgroundError(w, err)
			return
		}
		next := ""
		if uint(len(items)) == limit && limit > 0 {
			next = items[len(items)-1].ID
		}
		httpResponseJSON(w, playgroundList[service.PlaygroundConversation]{Data: items, Meta: playgroundListMeta{Limit: limit, NextBefore: next}}, http.StatusOK)
	case http.MethodPost:
		var body struct {
			Title        string         `json:"title"`
			SystemPrompt string         `json:"system_prompt"`
			ProviderKey  string         `json:"provider_key"`
			Model        string         `json:"model"`
			Config       map[string]any `json:"config"`
		}
		if !decodePlaygroundBody(w, r, &body) {
			return
		}
		if !playgroundPayloadFits(w, "config", body.Config) {
			return
		}
		created, err := store.CreatePlaygroundConversation(r.Context(), service.PlaygroundConversation{
			OwnerUserID:  owner,
			Title:        body.Title,
			SystemPrompt: body.SystemPrompt,
			ProviderKey:  body.ProviderKey,
			Model:        body.Model,
			Config:       body.Config,
		})
		if err != nil {
			playgroundError(w, err)
			return
		}
		httpResponseJSON(w, created, http.StatusCreated)
	default:
		nativeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// PlaygroundConversationAPI handles GET, PATCH and DELETE on a single
// /v1/playground/conversations/{id}.
func (s *Server) PlaygroundConversationAPI(w http.ResponseWriter, r *http.Request) {
	store, owner := s.playgroundAccess(w, r)
	if store == nil {
		return
	}
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodGet:
		c, err := store.GetPlaygroundConversation(r.Context(), owner, id)
		if err != nil {
			playgroundError(w, err)
			return
		}
		httpResponseJSON(w, c, http.StatusOK)
	case http.MethodPatch:
		var body struct {
			Title        *string         `json:"title"`
			SystemPrompt *string         `json:"system_prompt"`
			ProviderKey  *string         `json:"provider_key"`
			Model        *string         `json:"model"`
			Config       *map[string]any `json:"config"`
		}
		if !decodePlaygroundBody(w, r, &body) {
			return
		}
		patch := map[string]any{}
		for key, value := range map[string]*string{"title": body.Title, "system_prompt": body.SystemPrompt, "provider_key": body.ProviderKey, "model": body.Model} {
			if value != nil {
				patch[key] = *value
			}
		}
		if body.Config != nil {
			if !playgroundPayloadFits(w, "config", *body.Config) {
				return
			}
			patch["config"] = *body.Config
		}
		if len(patch) == 0 {
			nativeError(w, http.StatusBadRequest, "no fields to patch")
			return
		}
		c, err := store.PatchPlaygroundConversation(r.Context(), owner, id, patch)
		if err != nil {
			playgroundError(w, err)
			return
		}
		httpResponseJSON(w, c, http.StatusOK)
	case http.MethodDelete:
		if err := store.DeletePlaygroundConversation(r.Context(), owner, id); err != nil {
			playgroundError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		nativeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// PlaygroundForkAPI handles POST
// /v1/playground/conversations/{id}/fork. It answers with the NEW
// conversation, so the client can navigate straight to the branch; the source
// is left untouched.
func (s *Server) PlaygroundForkAPI(w http.ResponseWriter, r *http.Request) {
	store, owner := s.playgroundAccess(w, r)
	if store == nil {
		return
	}
	if r.Method != http.MethodPost {
		nativeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		FromSequence int64  `json:"from_sequence"`
		Title        string `json:"title"`
	}
	if !decodePlaygroundBody(w, r, &body) {
		return
	}
	// A missing from_sequence decodes to zero, which is never a valid
	// sequence: sequences start at 1.
	if body.FromSequence < 1 {
		nativeError(w, http.StatusBadRequest, "from_sequence must be a positive integer")
		return
	}
	forked, err := store.ForkPlaygroundConversation(r.Context(), owner, r.PathValue("id"), body.FromSequence, body.Title)
	if err != nil {
		playgroundError(w, err)
		return
	}
	httpResponseJSON(w, forked, http.StatusCreated)
}

// PlaygroundMessagesAPI handles GET, POST and DELETE on
// /v1/playground/conversations/{id}/messages.
func (s *Server) PlaygroundMessagesAPI(w http.ResponseWriter, r *http.Request) {
	store, owner := s.playgroundAccess(w, r)
	if store == nil {
		return
	}
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodGet:
		limit := playgroundQueryLimit(r)
		before := r.URL.Query().Get("before")
		if len(before) > 128 {
			nativeError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		items, err := store.ListPlaygroundMessages(r.Context(), owner, id, before, limit)
		if err != nil {
			playgroundError(w, err)
			return
		}
		// Items are chronological, so the oldest one anchors the next page.
		next := ""
		if uint(len(items)) == limit && limit > 0 {
			next = items[0].ID
		}
		httpResponseJSON(w, playgroundList[service.PlaygroundMessage]{Data: items, Meta: playgroundListMeta{Limit: limit, NextBefore: next}}, http.StatusOK)
	case http.MethodPost:
		var body struct {
			Messages []struct {
				Role        string         `json:"role"`
				ProviderKey string         `json:"provider_key"`
				Model       string         `json:"model"`
				Data        map[string]any `json:"data"`
			} `json:"messages"`
		}
		if !decodePlaygroundBody(w, r, &body) {
			return
		}
		if len(body.Messages) == 0 {
			nativeError(w, http.StatusBadRequest, "messages must not be empty")
			return
		}
		if len(body.Messages) > playgroundMessageBatchMax {
			nativeError(w, http.StatusBadRequest, "messages must contain at most 200 entries")
			return
		}
		items := make([]service.PlaygroundMessage, 0, len(body.Messages))
		for _, m := range body.Messages {
			if !service.ValidPlaygroundRole(m.Role) {
				nativeError(w, http.StatusBadRequest, "role must be one of user, assistant, tool")
				return
			}
			if !playgroundPayloadFits(w, "data", m.Data) {
				return
			}
			items = append(items, service.PlaygroundMessage{Role: m.Role, ProviderKey: m.ProviderKey, Model: m.Model, Data: m.Data})
		}
		stored, err := store.AppendPlaygroundMessages(r.Context(), owner, id, items)
		if err != nil {
			playgroundError(w, err)
			return
		}
		httpResponseJSON(w, playgroundList[service.PlaygroundMessage]{Data: stored}, http.StatusCreated)
	case http.MethodDelete:
		from, err := strconv.ParseInt(r.URL.Query().Get("from_sequence"), 10, 64)
		if err != nil || from < 1 {
			nativeError(w, http.StatusBadRequest, "from_sequence must be a positive integer")
			return
		}
		if err := store.TruncatePlaygroundMessages(r.Context(), owner, id, from); err != nil {
			playgroundError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		nativeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
