package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/blob"
)

type chatShareRequest struct {
	ThroughSequence int64                    `json:"through_sequence"`
	Options         service.ChatShareOptions `json:"options"`
}

type chatShareImportRequest struct {
	Version     int64  `json:"version"`
	Title       string `json:"title"`
	ProviderKey string `json:"provider_key"`
	Model       string `json:"model"`
}

func chatShareError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrChatShareNotFound), errors.Is(err, service.ErrPlaygroundNotFound):
		nativeError(w, http.StatusNotFound, "chat share not found")
	case errors.Is(err, service.ErrChatShareConflict), errors.Is(err, service.ErrPlaygroundConflict):
		nativeError(w, http.StatusConflict, "chat share changed; reload and retry")
	case errors.Is(err, service.ErrChatShareTooLarge), errors.Is(err, service.ErrPlaygroundTooLarge):
		nativeError(w, http.StatusRequestEntityTooLarge, "chat share exceeds the portable snapshot limit")
	case errors.Is(err, service.ErrAccessDenied), errors.Is(err, service.ErrAccessResourceNotFound):
		nativeError(w, http.StatusForbidden, "chat share is unavailable in this workspace")
	default:
		nativeError(w, http.StatusServiceUnavailable, "chat sharing is unavailable")
	}
}

func (s *Server) chatShareAccess(w http.ResponseWriter, r *http.Request) (service.ChatShareStorer, service.AccessPrincipal, string) {
	playground, owner := s.playgroundAccess(w, r)
	if playground == nil {
		return nil, service.AccessPrincipal{}, ""
	}
	chatsEnabled, err := s.isFeatureEnabled(r.Context(), service.FeaturePlayground)
	if err != nil || !chatsEnabled {
		nativeError(w, http.StatusNotFound, "chats are disabled")
		return nil, service.AccessPrincipal{}, ""
	}
	enabled, err := s.isFeatureEnabled(r.Context(), service.FeatureChatSharing)
	if err != nil || !enabled {
		nativeError(w, http.StatusNotFound, "chat sharing is disabled")
		return nil, service.AccessPrincipal{}, ""
	}
	principal, ok := service.AccessPrincipalFromContext(r.Context())
	if !ok || principal.WorkspaceID == "" {
		nativeError(w, http.StatusForbidden, "chat sharing requires a workspace")
		return nil, service.AccessPrincipal{}, ""
	}
	store, ok := s.store.(service.ChatShareStorer)
	if !ok {
		nativeError(w, http.StatusServiceUnavailable, "chat sharing storage unavailable")
		return nil, service.AccessPrincipal{}, ""
	}
	return store, principal, owner
}

func buildChatShareSnapshot(conversation *service.PlaygroundConversation, messages []service.PlaygroundMessage, options service.ChatShareOptions) (service.ChatSharePayload, []string, error) {
	if conversation == nil || len(messages) == 0 || messages[len(messages)-1].Role != "assistant" {
		return service.ChatSharePayload{}, nil, service.ErrChatShareConflict
	}
	payload := service.ChatSharePayload{Title: conversation.Title, ProviderKey: conversation.ProviderKey, Model: conversation.Model, Messages: []service.PlaygroundMessage{}}
	if options.IncludeSystemPrompt {
		payload.SystemPrompt = conversation.SystemPrompt
	}
	pendingTools := map[string]struct{}{}
	mediaSet := map[string]struct{}{}
	for _, message := range messages {
		if !options.IncludeToolOutputs && message.Role == "tool" {
			continue
		}
		portable := service.PlaygroundMessage{Sequence: message.Sequence, Role: message.Role, CreatedAt: message.CreatedAt, Data: map[string]any{}}
		for _, field := range []string{"content", "refusal"} {
			if value, ok := message.Data[field]; ok {
				portable.Data[field] = portableChatValue(value, options.IncludeAttachments, mediaSet)
			}
		}
		if options.IncludeToolOutputs {
			if calls, ok := message.Data["tool_calls"]; ok {
				portable.Data["tool_calls"] = portableChatValue(calls, options.IncludeAttachments, mediaSet)
				for _, id := range chatToolCallIDs(calls) {
					pendingTools[id] = struct{}{}
				}
			}
			if callID, ok := message.Data["tool_call_id"].(string); ok && callID != "" {
				if _, paired := pendingTools[callID]; !paired {
					return service.ChatSharePayload{}, nil, service.ErrChatShareConflict
				}
				delete(pendingTools, callID)
				portable.Data["tool_call_id"] = callID
			}
			if name, ok := message.Data["name"].(string); ok {
				portable.Data["name"] = name
			}
		}
		payload.Messages = append(payload.Messages, portable)
	}
	if len(pendingTools) != 0 {
		return service.ChatSharePayload{}, nil, service.ErrChatShareConflict
	}
	last := payload.Messages[len(payload.Messages)-1]
	if _, hasCalls := last.Data["tool_calls"]; hasCalls {
		return service.ChatSharePayload{}, nil, service.ErrChatShareConflict
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return service.ChatSharePayload{}, nil, fmt.Errorf("encode chat share snapshot: %w", err)
	}
	if len(encoded) > service.ChatShareMaxBytes {
		return service.ChatSharePayload{}, nil, service.ErrChatShareTooLarge
	}
	mediaIDs := make([]string, 0, len(mediaSet))
	for id := range mediaSet {
		mediaIDs = append(mediaIDs, id)
	}
	return payload, mediaIDs, nil
}

func portableChatValue(value any, includeAttachments bool, mediaSet map[string]struct{}) any {
	switch current := value.(type) {
	case map[string]any:
		if mediaID, _ := current["media_id"].(string); mediaID != "" {
			if !includeAttachments {
				return nil
			}
			mediaSet[mediaID] = struct{}{}
		}
		out := make(map[string]any, len(current))
		for key, child := range current {
			out[key] = portableChatValue(child, includeAttachments, mediaSet)
		}
		return out
	case []any:
		out := make([]any, 0, len(current))
		for _, child := range current {
			portable := portableChatValue(child, includeAttachments, mediaSet)
			if portable != nil {
				out = append(out, portable)
			}
		}
		return out
	default:
		return current
	}
}

func chatToolCallIDs(value any) []string {
	items, _ := value.([]any)
	ids := make([]string, 0, len(items))
	for _, item := range items {
		call, _ := item.(map[string]any)
		if id, _ := call["id"].(string); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func replaceSnapshotMedia(payload service.ChatSharePayload, replacements map[string]string) service.ChatSharePayload {
	for i := range payload.Messages {
		if data, ok := replaceChatMediaValue(payload.Messages[i].Data, replacements).(map[string]any); ok {
			payload.Messages[i].Data = data
		}
	}
	return payload
}

func replaceChatMediaValue(value any, replacements map[string]string) any {
	switch current := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(current))
		for key, child := range current {
			if key == "media_id" {
				if id, ok := child.(string); ok && replacements[id] != "" {
					out[key] = replacements[id]
					continue
				}
			}
			out[key] = replaceChatMediaValue(child, replacements)
		}
		return out
	case []any:
		out := make([]any, len(current))
		for i, child := range current {
			out[i] = replaceChatMediaValue(child, replacements)
		}
		return out
	default:
		return current
	}
}

func (s *Server) copyChatMedia(ctx context.Context, sourceOwner, targetOwner string, ids []string) (map[string]string, []service.MediaObject, error) {
	replacements := map[string]string{}
	if len(ids) == 0 {
		return replacements, nil, nil
	}
	store, ok := s.store.(service.MediaStorer)
	if !ok {
		return nil, nil, service.ErrMediaNotFound
	}
	settings, err := store.GetMediaSettings(ctx)
	if err != nil || !settings.Enabled() {
		return nil, nil, service.ErrMediaNotFound
	}
	target, err := blob.New(*settings)
	if err != nil {
		return nil, nil, err
	}
	created := []service.MediaObject{}
	for _, id := range ids {
		object, err := store.GetMediaObject(ctx, sourceOwner, id)
		if err != nil || object.Backend != settings.Backend {
			s.cleanupChatMedia(ctx, target, store, targetOwner, created)
			return nil, nil, service.ErrMediaNotFound
		}
		reader, _, err := target.Get(ctx, object.StorageKey)
		if err != nil {
			s.cleanupChatMedia(ctx, target, store, targetOwner, created)
			return nil, nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, mediaUploadMaxBytes+1))
		reader.Close()
		if readErr != nil || len(data) > mediaUploadMaxBytes {
			s.cleanupChatMedia(ctx, target, store, targetOwner, created)
			return nil, nil, service.ErrChatShareTooLarge
		}
		ext := mediaAllowedContentTypes[object.ContentType]
		key := mediaStorageKey(*settings, targetOwner, ext)
		if err := target.Put(ctx, key, object.ContentType, data); err != nil {
			s.cleanupChatMedia(ctx, target, store, targetOwner, created)
			return nil, nil, err
		}
		copy, err := store.CreateMediaObject(ctx, service.MediaObject{OwnerUserID: targetOwner, Backend: object.Backend, StorageKey: key, ContentType: object.ContentType, SizeBytes: object.SizeBytes, Checksum: object.Checksum})
		if err != nil {
			target.Delete(ctx, key) //nolint:errcheck // best effort rollback
			s.cleanupChatMedia(ctx, target, store, targetOwner, created)
			return nil, nil, err
		}
		created = append(created, *copy)
		replacements[id] = copy.ID
	}
	return replacements, created, nil
}

func (s *Server) cleanupChatMedia(ctx context.Context, target blob.Store, store service.MediaStorer, owner string, objects []service.MediaObject) {
	for _, object := range objects {
		if _, err := store.DeleteMediaObject(ctx, owner, object.ID); err != nil {
			slog.Warn("chat media row cleanup failed", "id", object.ID, "error", err.Error())
		}
		if err := target.Delete(ctx, object.StorageKey); err != nil {
			slog.Warn("chat media blob cleanup failed", "key", object.StorageKey, "error", err.Error())
		}
	}
}

func chatShareOwner(id string) string { return "chat-share:" + id }

func (s *Server) buildChatShare(r *http.Request, store service.ChatShareStorer, owner, conversationID string, req chatShareRequest) (service.ChatSharePayload, []string, error) {
	conversation, messages, err := store.ReadPlaygroundPrefix(r.Context(), owner, conversationID, req.ThroughSequence)
	if err != nil {
		return service.ChatSharePayload{}, nil, err
	}
	return buildChatShareSnapshot(conversation, messages, req.Options)
}

func (s *Server) ChatSharePreviewAPI(w http.ResponseWriter, r *http.Request) {
	store, _, owner := s.chatShareAccess(w, r)
	if store == nil {
		return
	}
	var req chatShareRequest
	if !decodePlaygroundBody(w, r, &req) {
		return
	}
	payload, mediaIDs, err := s.buildChatShare(r, store, owner, r.PathValue("id"), req)
	if err != nil {
		chatShareError(w, err)
		return
	}
	httpResponseJSON(w, map[string]any{"payload": payload, "attachment_count": len(mediaIDs), "options": req.Options}, http.StatusOK)
}

func (s *Server) ChatConversationShareAPI(w http.ResponseWriter, r *http.Request) {
	store, principal, owner := s.chatShareAccess(w, r)
	if store == nil {
		return
	}
	share, err := store.GetChatShareBySource(r.Context(), owner, r.PathValue("id"), principal.WorkspaceID)
	if err != nil || share.RevokedAt != "" {
		chatShareError(w, service.ErrChatShareNotFound)
		return
	}
	httpResponseJSON(w, share, http.StatusOK)
}

func (s *Server) PublishChatShareAPI(w http.ResponseWriter, r *http.Request) {
	store, principal, owner := s.chatShareAccess(w, r)
	if store == nil {
		return
	}
	var req chatShareRequest
	if !decodePlaygroundBody(w, r, &req) {
		return
	}
	payload, sourceMedia, err := s.buildChatShare(r, store, owner, r.PathValue("id"), req)
	if err != nil {
		chatShareError(w, err)
		return
	}
	shareID := ulid.Make().String()
	replacements, copies, err := s.copyChatMedia(r.Context(), owner, chatShareOwner(shareID), sourceMedia)
	if err != nil {
		chatShareError(w, err)
		return
	}
	share, err := store.CreateChatShare(r.Context(), service.ChatShare{ID: shareID, WorkspaceID: principal.WorkspaceID, SourceConversationID: r.PathValue("id"), SourceOwnerUserID: owner, ThroughSequence: req.ThroughSequence, Payload: replaceSnapshotMedia(payload, replacements), Options: req.Options}, mediaObjectIDs(copies))
	if err != nil {
		s.cleanupCopiedChatMedia(r.Context(), chatShareOwner(shareID), copies)
		chatShareError(w, err)
		return
	}
	httpResponseJSON(w, share, http.StatusCreated)
}

func mediaObjectIDs(objects []service.MediaObject) []string {
	ids := make([]string, 0, len(objects))
	for _, object := range objects {
		ids = append(ids, object.ID)
	}
	return ids
}

func (s *Server) cleanupCopiedChatMedia(ctx context.Context, owner string, objects []service.MediaObject) {
	store, ok := s.store.(service.MediaStorer)
	if !ok || len(objects) == 0 {
		return
	}
	settings, err := store.GetMediaSettings(ctx)
	if err != nil {
		return
	}
	target, err := blob.New(*settings)
	if err != nil {
		return
	}
	s.cleanupChatMedia(ctx, target, store, owner, objects)
}

func (s *Server) ChatShareAPI(w http.ResponseWriter, r *http.Request) {
	store, principal, owner := s.chatShareAccess(w, r)
	if store == nil {
		return
	}
	share, err := store.GetChatShare(r.Context(), r.PathValue("id"))
	if err != nil || share.WorkspaceID != principal.WorkspaceID || share.RevokedAt != "" {
		chatShareError(w, service.ErrChatShareNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		httpResponseJSON(w, share, http.StatusOK)
	case http.MethodPut:
		if share.SourceOwnerUserID != owner {
			chatShareError(w, service.ErrChatShareNotFound)
			return
		}
		var req chatShareRequest
		if !decodePlaygroundBody(w, r, &req) {
			return
		}
		payload, sourceMedia, err := s.buildChatShare(r, store, owner, share.SourceConversationID, req)
		if err != nil {
			chatShareError(w, err)
			return
		}
		replacements, copies, err := s.copyChatMedia(r.Context(), owner, chatShareOwner(share.ID), sourceMedia)
		if err != nil {
			chatShareError(w, err)
			return
		}
		share.ThroughSequence, share.Payload, share.Options = req.ThroughSequence, replaceSnapshotMedia(payload, replacements), req.Options
		updated, err := store.UpdateChatShare(r.Context(), *share, mediaObjectIDs(copies))
		if err != nil {
			s.cleanupCopiedChatMedia(r.Context(), chatShareOwner(share.ID), copies)
			chatShareError(w, err)
			return
		}
		httpResponseJSON(w, updated, http.StatusOK)
	case http.MethodDelete:
		if share.SourceOwnerUserID != owner {
			chatShareError(w, service.ErrChatShareNotFound)
			return
		}
		objects, err := store.RevokeChatShare(r.Context(), share.ID, owner)
		if err != nil {
			chatShareError(w, err)
			return
		}
		s.deleteChatMediaBlobs(r.Context(), objects)
		w.WriteHeader(http.StatusNoContent)
	default:
		nativeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) deleteChatMediaBlobs(ctx context.Context, objects []service.MediaObject) {
	if len(objects) == 0 {
		return
	}
	store, ok := s.store.(service.MediaStorer)
	if !ok {
		return
	}
	settings, err := store.GetMediaSettings(ctx)
	if err != nil {
		return
	}
	target, err := blob.New(*settings)
	if err != nil {
		return
	}
	for _, object := range objects {
		if object.Backend == settings.Backend {
			if err := target.Delete(ctx, object.StorageKey); err != nil {
				slog.Warn("chat share blob cleanup failed", "key", object.StorageKey, "error", err.Error())
			}
		}
	}
}

func (s *Server) ImportChatShareAPI(w http.ResponseWriter, r *http.Request) {
	store, principal, owner := s.chatShareAccess(w, r)
	if store == nil {
		return
	}
	var req chatShareImportRequest
	if !decodePlaygroundBody(w, r, &req) {
		return
	}
	share, err := store.GetChatShare(r.Context(), r.PathValue("id"))
	if err != nil || share.WorkspaceID != principal.WorkspaceID || share.RevokedAt != "" || req.Version != share.CurrentVersion {
		chatShareError(w, service.ErrChatShareConflict)
		return
	}
	if req.ProviderKey != "" || req.Model != "" {
		if req.ProviderKey == "" || req.Model == "" {
			nativeError(w, http.StatusBadRequest, "provider_key and model must be selected together")
			return
		}
		if _, err := s.workspaceProviderInfo(r.Context(), req.ProviderKey, req.Model); err != nil {
			chatShareError(w, service.ErrAccessDenied)
			return
		}
	}
	shareMedia := collectSnapshotMediaIDs(share.Payload)
	replacements, copies, err := s.copyChatMedia(r.Context(), chatShareOwner(share.ID), owner, shareMedia)
	if err != nil {
		chatShareError(w, err)
		return
	}
	conversation, err := store.ImportChatShare(r.Context(), share.ID, req.Version, owner, req.Title, req.ProviderKey, req.Model, replacements)
	if err != nil {
		s.cleanupCopiedChatMedia(r.Context(), owner, copies)
		chatShareError(w, err)
		return
	}
	httpResponseJSON(w, conversation, http.StatusCreated)
}

func collectSnapshotMediaIDs(payload service.ChatSharePayload) []string {
	set := map[string]struct{}{}
	for _, message := range payload.Messages {
		collectChatMediaIDs(message.Data, set)
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	return ids
}

func collectChatMediaIDs(value any, set map[string]struct{}) {
	switch current := value.(type) {
	case map[string]any:
		if id, _ := current["media_id"].(string); id != "" {
			set[id] = struct{}{}
		}
		for _, child := range current {
			collectChatMediaIDs(child, set)
		}
	case []any:
		for _, child := range current {
			collectChatMediaIDs(child, set)
		}
	}
}

func (s *Server) ChatShareMediaAPI(w http.ResponseWriter, r *http.Request) {
	store, principal, _ := s.chatShareAccess(w, r)
	if store == nil {
		return
	}
	share, err := store.GetChatShare(r.Context(), r.PathValue("id"))
	if err != nil || share.WorkspaceID != principal.WorkspaceID || share.RevokedAt != "" {
		chatShareError(w, service.ErrChatShareNotFound)
		return
	}
	found := false
	for _, id := range collectSnapshotMediaIDs(share.Payload) {
		if id == r.PathValue("media") {
			found = true
			break
		}
	}
	if !found {
		chatShareError(w, service.ErrChatShareNotFound)
		return
	}
	mediaStore, ok := s.store.(service.MediaStorer)
	if !ok {
		chatShareError(w, service.ErrChatShareNotFound)
		return
	}
	r.SetPathValue("id", r.PathValue("media"))
	s.mediaServe(w, r, mediaStore, chatShareOwner(share.ID))
}
