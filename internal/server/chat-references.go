package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rakunlabs/at/internal/gateway/wire"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/blob"
)

// Browser-only extension: saved messages cross the network as opaque IDs.
// Exact rows (including tool correlation) are expanded under the same owner
// admission as history. Never accept this extension on the gateway API.
func (s *Server) resolveChatReferences(w http.ResponseWriter, r *http.Request, body []byte, req *wire.ChatCompletionRequest) bool {
	var input struct {
		ConversationID string `json:"at_conversation_id"`
		HistoryBefore  string `json:"at_history_before"`
		Messages       []struct {
			ID string `json:"at_message_id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &input); err != nil {
		httpResponseJSON(w, map[string]any{"error": map[string]string{"message": "invalid saved message references"}}, http.StatusBadRequest)
		return false
	}
	var ids []string
	for _, m := range input.Messages {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	if len(ids) == 0 && input.HistoryBefore == "" {
		return true
	}
	if input.ConversationID == "" || len(ids) > 2000 || len(input.Messages) > 4096 {
		httpResponseJSON(w, map[string]any{"error": map[string]string{"message": "saved message references require a conversation and at most 2000 saved messages"}}, http.StatusBadRequest)
		return false
	}
	store, owner := s.playgroundAccess(w, r)
	if store == nil {
		return false
	}
	refs, ok := store.(service.PlaygroundMessageReferenceStorer)
	if !ok {
		nativeError(w, http.StatusServiceUnavailable, "saved message references unavailable")
		return false
	}
	if input.HistoryBefore != "" {
		// Anchor must be the first referenced transcript row, after any current
		// system prompt. This prevents duplicating a caller's visible history.
		if len(ids) == 0 || ids[0] != input.HistoryBefore {
			nativeError(w, http.StatusBadRequest, "history prefix must anchor the first saved message")
			return false
		}
		prefix, err := refs.GetPlaygroundMessagePrefix(r.Context(), owner, input.ConversationID, input.HistoryBefore)
		if err != nil {
			playgroundError(w, err)
			return false
		}
		// A compaction summary replaces everything before it, so the unloaded
		// prefix starts at the latest one rather than at the first message.
		for i := len(prefix) - 1; i >= 0; i-- {
			if compacted, _ := prefix[i].Data["compaction"].(bool); compacted {
				prefix = prefix[i:]
				break
			}
		}
		position := 0
		for position < len(req.Messages) && req.Messages[position].Role == "system" {
			position++
		}
		// Reuse the exact media and reference resolver for the expanded prefix.
		var expanded []any
		for i, message := range req.Messages {
			if i == position {
				for _, m := range prefix {
					expanded = append(expanded, map[string]string{"at_message_id": m.ID})
				}
			}
			if input.Messages[i].ID != "" {
				expanded = append(expanded, map[string]string{"at_message_id": input.Messages[i].ID})
			} else {
				expanded = append(expanded, message)
			}
		}
		encoded, err := json.Marshal(map[string]any{"at_conversation_id": input.ConversationID, "messages": expanded})
		if err != nil {
			nativeError(w, http.StatusBadRequest, "invalid history prefix")
			return false
		}
		var resolved wire.ChatCompletionRequest
		if err := json.Unmarshal(encoded, &resolved); err != nil {
			nativeError(w, http.StatusBadRequest, "invalid history prefix")
			return false
		}
		if !s.resolveChatReferences(w, r, encoded, &resolved) {
			return false
		}
		req.Messages = resolved.Messages
		return true
	}
	stored, err := refs.GetPlaygroundMessageReferences(r.Context(), owner, input.ConversationID, ids)
	if err != nil {
		playgroundError(w, err)
		return false
	}
	if len(stored) != len(ids) {
		nativeError(w, http.StatusConflict, "saved message history changed; reload the conversation")
		return false
	}
	// Resolve each media object once per request, without a browser round trip.
	resolver := newStoredMessageResolver(s, owner)
	index := 0
	for i, ref := range input.Messages {
		if ref.ID == "" {
			continue
		}
		m := stored[index]
		index++
		if m.ID != ref.ID {
			nativeError(w, http.StatusConflict, "saved message history changed; reload the conversation")
			return false
		}
		resolved, err := resolver.message(r.Context(), m)
		switch {
		case errors.Is(err, errStoredMediaUnavailable):
			httpResponseJSON(w, map[string]any{"error": map[string]string{"message": "Could not load a saved attachment. Retry when storage is available, or remove the attachment."}}, http.StatusConflict)
			return false
		case errors.Is(err, errStoredHistoryTooLarge):
			httpResponseJSON(w, map[string]any{"error": map[string]string{"message": "Saved history exceeds the 64 MiB completion limit. Start a smaller conversation."}}, http.StatusRequestEntityTooLarge)
			return false
		case err != nil:
			nativeError(w, http.StatusConflict, "invalid saved message")
			return false
		}
		req.Messages[i] = resolved
	}
	return true
}

var (
	errStoredMediaUnavailable = errors.New("saved attachment unavailable")
	errStoredHistoryTooLarge  = errors.New("saved history exceeds the 64 MiB completion limit")
)

// storedMessageResolver turns saved Chats rows into the OpenAI-shaped messages
// a completion sends, inlining owner-scoped media once per request. Browser
// completions and background runs share it so both send identical context.
type storedMessageResolver struct {
	server    *Server
	owner     string
	media     map[string]string
	remaining int64
	expanded  int
}

func newStoredMessageResolver(s *Server, owner string) *storedMessageResolver {
	return &storedMessageResolver{server: s, owner: owner, media: map[string]string{}, remaining: 64 << 20}
}

func (sr *storedMessageResolver) message(ctx context.Context, m service.PlaygroundMessage) (wire.OpenAIMessage, error) {
	data := make(map[string]any, len(m.Data)+1)
	for k, v := range m.Data {
		data[k] = v
	}
	data["role"] = m.Role
	if parts, ok := data["content"].([]any); ok {
		resolved := make([]any, 0, len(parts))
		for _, value := range parts {
			part, ok := value.(map[string]any)
			if !ok {
				resolved = append(resolved, value)
				continue
			}
			kind, _ := part["type"].(string)
			if kind != "image" && kind != "file" {
				resolved = append(resolved, part)
				continue
			}
			name, _ := part["name"].(string)
			mime, _ := part["mime_type"].(string)
			attachment, _ := part["attachment"].(bool)
			if m.Role == "assistant" || (kind == "file" && !attachment) {
				resolved = append(resolved, map[string]any{"type": "text", "text": deliveredMediaReference(kind, name, mime, part)})
				continue
			}
			id, _ := part["media_id"].(string)
			if id == "" {
				resolved = append(resolved, map[string]any{"type": "text", "text": fmt.Sprintf("[%s %q was not saved to history]", kind, name)})
				continue
			}
			url, cached := sr.media[id]
			if !cached {
				var err error
				url, err = sr.server.chatMediaDataURL(ctx, sr.owner, id, &sr.remaining)
				if err != nil {
					return wire.OpenAIMessage{}, fmt.Errorf("%w: %w", errStoredMediaUnavailable, err)
				}
				sr.media[id] = url
			}
			resolved = append(resolved, chatStoredMediaPart(kind, name, mime, url))
		}
		data["content"] = resolved
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return wire.OpenAIMessage{}, err
	}
	sr.expanded += len(encoded)
	if sr.expanded > 64<<20 {
		return wire.OpenAIMessage{}, errStoredHistoryTooLarge
	}
	var resolved wire.OpenAIMessage
	if err := json.Unmarshal(encoded, &resolved); err != nil {
		return wire.OpenAIMessage{}, err
	}
	return resolved, nil
}

// deliveredMediaReference describes media the assistant delivered (images
// from generate_image, skill-run artifacts, ...). Providers accept only text
// in the assistant role, so it goes upstream as a reference the model can
// still cite or pass back to a tool (e.g. generate_image reference_images).
func deliveredMediaReference(kind, name, mime string, part map[string]any) string {
	if name == "" {
		name = kind
	}
	detail := mime
	if id, _ := part["media_id"].(string); id != "" {
		if detail != "" {
			detail += ", "
		}
		detail += service.MediaRefScheme + id
	}
	if detail == "" {
		return fmt.Sprintf("[%s %q delivered to the user]", kind, name)
	}
	return fmt.Sprintf("[%s %q (%s) delivered to the user]", kind, name, detail)
}

func chatStoredMediaPart(kind, name, mime, url string) map[string]any {
	switch {
	case kind == "image":
		return map[string]any{"type": "image_url", "image_url": map[string]string{"url": url}}
	case strings.HasPrefix(mime, "audio/"):
		format := strings.ReplaceAll(strings.TrimPrefix(strings.TrimPrefix(mime, "audio/"), "x-"), "mpeg", "mp3")
		_, data, _ := strings.Cut(url, ",")
		return map[string]any{"type": "input_audio", "input_audio": map[string]string{"data": data, "format": format}}
	case strings.HasPrefix(mime, "video/"):
		return map[string]any{"type": "video_url", "video_url": map[string]string{"url": url}}
	default:
		return map[string]any{"type": "file", "file": map[string]string{"filename": name, "file_data": url}}
	}
}

func (s *Server) chatMediaDataURL(ctx context.Context, owner, id string, remaining *int64) (string, error) {
	store, ok := s.store.(service.MediaStorer)
	principal, scoped := service.AccessPrincipalFromContext(ctx)
	if !ok || !scoped {
		return "", fmt.Errorf("media scope unavailable")
	}
	object, err := store.GetMediaObject(ctx, principal.WorkspaceID, owner, id)
	if err != nil {
		return "", err
	}
	if object == nil || object.SizeBytes > *remaining {
		return "", fmt.Errorf("saved attachments exceed the request limit or are unavailable")
	}
	settings, err := store.GetMediaSettings(ctx)
	if err != nil {
		return "", err
	}
	if settings == nil || !settings.Enabled() || object.Backend != settings.Backend {
		return "", fmt.Errorf("media backend unavailable")
	}
	target, err := blob.New(*settings)
	if err != nil {
		return "", err
	}
	reader, _, err := target.Get(ctx, object.StorageKey)
	if err != nil {
		return "", err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, *remaining+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > *remaining {
		return "", fmt.Errorf("saved attachments exceed the request limit")
	}
	*remaining -= int64(len(data))
	return "data:" + object.ContentType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}
