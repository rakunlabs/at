package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rakunlabs/ada/middleware/auth/identity"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/loopgov"
)

const personalTextLimit = 32 * 1024
const personalOutputLimit = 1024 * 1024

func (s *Server) personalAccess(w http.ResponseWriter, r *http.Request) (service.PersonalChatStorer, string) {
	w.Header().Set("Cache-Control", "no-store")
	if s.nativeAuth == nil {
		nativeError(w, 503, "personal chat requires native authentication")
		return nil, ""
	}
	id := identity.FromContext(r.Context())
	if id == nil || id.Subject == "" || !id.HasRole("admin") {
		nativeError(w, 403, "personal chat requires a native administrator")
		return nil, ""
	}
	store, ok := s.store.(service.PersonalChatStorer)
	if !ok {
		nativeError(w, 503, "personal chat storage unavailable")
		return nil, ""
	}
	return store, id.Subject
}

func personalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrPersonalChatNotFound):
		nativeError(w, 404, "conversation or message not found")
	case errors.Is(err, service.ErrPersonalChatConflict):
		nativeError(w, 409, "conversation busy or request_id content mismatch")
	default:
		nativeError(w, 503, "personal chat storage unavailable")
	}
}

func decodePersonalBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		nativeError(w, 415, "application/json required")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024))
	d.DisallowUnknownFields()
	err = d.Decode(dst)
	if err == nil {
		if trailing := d.Decode(new(any)); trailing != io.EOF {
			err = trailing
			if err == nil {
				err = errors.New("trailing JSON")
			}
		}
	}
	if err != nil {
		var large *http.MaxBytesError
		code := 400
		if errors.As(err, &large) {
			code = 413
		}
		nativeError(w, code, "invalid personal chat request")
		return false
	}
	return true
}

type personalModel struct {
	ProviderKey string `json:"provider_key"`
	Model       string `json:"model"`
}

func (s *Server) personalModels() []personalModel {
	items := []personalModel{}
	for _, key := range s.availableProviderKeys() {
		info, ok := s.getProviderInfo(key)
		if !ok || info.provider == nil {
			continue
		}
		models := info.models
		if len(models) == 0 {
			models = []string{info.defaultModel}
		}
		seen := map[string]bool{}
		for _, model := range models {
			if model != "" && !seen[model] {
				items = append(items, personalModel{key, model})
				seen[model] = true
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ProviderKey == items[j].ProviderKey {
			return items[i].Model < items[j].Model
		}
		return items[i].ProviderKey < items[j].ProviderKey
	})
	return items
}

func (s *Server) personalModelAllowed(key, model string) bool {
	for _, item := range s.personalModels() {
		if item.ProviderKey == key && item.Model == model {
			return true
		}
	}
	return false
}

func (s *Server) PersonalChatModelsAPI(w http.ResponseWriter, r *http.Request) {
	if store, _ := s.personalAccess(w, r); store == nil {
		return
	}
	httpResponseJSON(w, s.personalModels(), 200)
}

// PersonalChatAPI handles only the new personal conversation resource routes.
func (s *Server) PersonalChatAPI(w http.ResponseWriter, r *http.Request) {
	store, owner := s.personalAccess(w, r)
	if store == nil {
		return
	}
	id, mid := r.PathValue("id"), r.PathValue("message_id")
	var result any
	var err error
	status := 200
	switch {
	case r.Method == "GET":
		limit := uint64(50)
		if raw := r.URL.Query().Get("limit"); raw != "" {
			limit, err = strconv.ParseUint(raw, 10, 32)
			if err != nil || limit < 1 || limit > 100 {
				nativeError(w, 400, "limit must be 1 through 100")
				return
			}
		}
		before := r.URL.Query().Get("before")
		if len(before) > 128 {
			nativeError(w, 400, "invalid cursor")
			return
		}
		switch {
		case id == "":
			var items []service.PersonalConversation
			items, err = store.ListPersonalConversations(r.Context(), owner, before, uint(limit))
			next := ""
			if len(items) == int(limit) {
				next = items[len(items)-1].ID
			}
			result = map[string]any{"items": items, "next_before": next}
		case mid != "":
			result, err = store.GetPersonalMessage(r.Context(), owner, id, mid)
		case strings.HasSuffix(r.URL.Path, "/messages"):
			var items []service.PersonalChatMessage
			items, err = store.ListPersonalMessages(r.Context(), owner, id, before, uint(limit))
			next := ""
			if len(items) == int(limit) {
				next = items[len(items)-1].ID
			}
			result = map[string]any{"items": items, "next_before": next}
		default:
			result, err = store.GetPersonalConversation(r.Context(), owner, id)
		}
	case r.Method == "POST" && mid != "":
		result, err = store.CancelPersonalTurn(r.Context(), owner, id, mid)
	case r.Method == "DELETE":
		err = store.DeletePersonalConversation(r.Context(), owner, id)
		status = 204
	case r.Method == "POST" || r.Method == "PATCH":
		// Resolve ownership before body validation for nested resources.
		if id != "" {
			if _, err = store.GetPersonalConversation(r.Context(), owner, id); err != nil {
				personalError(w, err)
				return
			}
		}
		var body struct {
			Title        *string `json:"title"`
			ProviderKey  *string `json:"provider_key"`
			Model        *string `json:"model"`
			SystemPrompt *string `json:"system_prompt"`
		}
		if !decodePersonalBody(w, r, &body) {
			return
		}
		patch := map[string]string{}
		for key, value := range map[string]*string{"title": body.Title, "provider_key": body.ProviderKey, "model": body.Model, "system_prompt": body.SystemPrompt} {
			if value == nil {
				continue
			}
			max := personalTextLimit
			if key != "system_prompt" {
				max = 256
			}
			if !utf8.ValidString(*value) || strings.ContainsRune(*value, 0) || len(*value) > max || (key != "system_prompt" && strings.TrimSpace(*value) == "") {
				nativeError(w, 400, "invalid conversation settings")
				return
			}
			patch[key] = *value
		}
		if (body.Model == nil) != (body.ProviderKey == nil) {
			nativeError(w, 400, "provider_key and model must be supplied together")
			return
		}
		if body.Model != nil && !s.personalModelAllowed(*body.ProviderKey, *body.Model) {
			nativeError(w, 400, "model is not in the personal chat catalog")
			return
		}
		if r.Method == "POST" {
			if body.Model == nil {
				nativeError(w, 400, "provider_key and model required")
				return
			}
			title := "New conversation"
			if body.Title != nil {
				title = *body.Title
			}
			result, err = store.CreatePersonalConversation(r.Context(), service.PersonalConversation{OwnerUserID: owner, Title: title, ProviderKey: *body.ProviderKey, Model: *body.Model, SystemPrompt: patch["system_prompt"]})
			status = 201
		} else {
			result, err = store.PatchPersonalConversation(r.Context(), owner, id, patch)
		}
	default:
		nativeError(w, 405, "method not allowed")
		return
	}
	if err != nil {
		personalError(w, err)
		return
	}
	if status == 204 {
		w.WriteHeader(status)
		return
	}
	httpResponseJSON(w, result, status)
}

func personalSSE(w http.ResponseWriter, event string, data any) error {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(5 * time.Second))
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
		return err
	}
	return rc.Flush()
}

// Unlike the permissive gateway fallback, unknown/missing reasons are never
// evidence of completion. Reuse its mapping only for explicitly known values.
func personalFinishReason(reason string) string {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "stop", "end_turn", "stop_sequence", "endofturn", "length", "max_tokens", "max_output_tokens", "content_filter", "safety", "blocklist", "prohibited_content", "spii", "recitation":
		return normalizeFinishReason(&service.LLMResponse{FinishReason: reason})
	case "complete": // Cohere
		return "stop"
	case "guardrail_intervened", "content_filtered": // Bedrock
		return "content_filter"
	default:
		return ""
	}
}

func (s *Server) SendPersonalChatAPI(w http.ResponseWriter, r *http.Request) {
	store, owner := s.personalAccess(w, r)
	if store == nil {
		return
	}
	id := r.PathValue("id")
	if _, err := store.GetPersonalConversation(r.Context(), owner, id); err != nil {
		personalError(w, err)
		return
	}
	var body struct {
		Content   string `json:"content"`
		RequestID string `json:"request_id"`
	}
	if !decodePersonalBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Content) == "" || len(body.Content) > personalTextLimit || !utf8.ValidString(body.Content) || strings.ContainsRune(body.Content, 0) || strings.ContainsRune(body.RequestID, 0) || len(body.RequestID) < 1 || len(body.RequestID) > 128 || strings.TrimSpace(body.RequestID) != body.RequestID {
		nativeError(w, 400, "content must be 1 through 32768 UTF-8 bytes; request_id must be 1 through 128 bytes")
		return
	}
	turn, err := store.BeginPersonalTurn(r.Context(), owner, id, body.RequestID, body.Content)
	if err != nil {
		personalError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	defer http.NewResponseController(w).SetWriteDeadline(time.Time{})
	if turn.Replay {
		if personalSSE(w, "accepted", turn) != nil {
			return
		}
		if personalSSE(w, "snapshot", turn.Assistant) != nil {
			return
		}
		if !turn.Assistant.Active() {
			event := "done"
			if turn.Assistant.Status != "completed" {
				event = "error"
			}
			_ = personalSSE(w, event, turn.Assistant)
		}
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	m := turn.Assistant
	started := time.Now()
	var usage service.Usage
	called := false
	// A disconnect must not cancel the final database write. This bounded cleanup
	// also handles setup failures and never reports a terminal event before commit.
	defer func() {
		// Check before our own cleanup cancel: request cancellation wins even if
		// EOF/finish/error was selected concurrently. A timeout is not incomplete EOF.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			m.Status = "failed"
			m.Error = "generation_timeout"
		} else if ctx.Err() != nil || m.Active() {
			m.Status = "cancelled"
			m.Error = "generation_cancelled"
		}
		cancel()
		finalCtx, finalCancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer finalCancel()
		stored, saveErr := store.CheckpointPersonalTurn(finalCtx, owner, id, turn.LeaseToken, m)
		if saveErr == nil {
			m = *stored
		}
		if called {
			status := "ok"
			if m.Status != "completed" {
				status = "error"
			}
			fullModel := m.ProviderKey + "/" + m.Model
			// Shared admin traces receive accounting skeletons, never private transcript
			// bodies. Conversation ownership must not be bypassed via the trace API.
			s.recordLLMCallAsync(finalCtx, llmAuditParams{source: "chat", name: "personal_chat", endpoint: r.URL.Path, traceID: m.ID, sessionID: id, userField: owner, requestedModel: fullModel, fullModel: fullModel, usage: usage, latencyMs: time.Since(started).Milliseconds(), streamed: true, status: status, errCode: m.Error, errMsg: m.Error, finishReason: m.FinishReason})
			if s.costEventStore != nil {
				cost := s.estimateGatewayUsageCostCents(finalCtx, m.ProviderKey, m.Model, fullModel, usage)
				_ = s.costEventStore.RecordCostEvent(finalCtx, service.CostEvent{AgentID: "personal:" + owner, Provider: m.ProviderKey, Model: m.Model, InputTokens: int64(usage.PromptTokens), OutputTokens: int64(usage.CompletionTokens), CacheReadTokens: int64(usage.CacheReadTokens), CacheWriteTokens: int64(usage.CacheWriteTokens), CostCents: cost, LatencyMs: time.Since(started).Milliseconds(), Status: status, ErrorCode: m.Error, ErrorMessage: m.Error})
			}
		}
		if saveErr == nil && r.Context().Err() == nil {
			event := "done"
			if stored.Status != "completed" {
				event = "error"
			}
			_ = personalSSE(w, event, stored)
		}
	}()
	if personalSSE(w, "accepted", turn) != nil {
		return
	}
	info, ok := s.getProviderInfo(m.ProviderKey)
	if !ok || info.provider == nil || !s.personalModelAllowed(m.ProviderKey, m.Model) {
		m.Status = "failed"
		m.Error = "model_unavailable"
		return
	}
	// Load a bounded suffix, never a client transcript. Keep durable history intact.
	history := []service.Message{}
	before, size := "", 0
	for size < 128*1024 && len(history) < 200 {
		page, err := store.ListPersonalMessages(ctx, owner, id, before, 10)
		if err != nil {
			m.Status = "failed"
			m.Error = "history_unavailable"
			return
		}
		for _, msg := range page {
			before = msg.ID
			if msg.Status != "completed" {
				continue
			}
			history = append(history, service.Message{Role: msg.Role, Content: msg.Content})
			size += len(msg.Content)
			if size >= 128*1024 {
				break
			}
		}
		if len(page) < 10 {
			break
		}
	}
	for i, j := 0, len(history)-1; i < j; i, j = i+1, j-1 {
		history[i], history[j] = history[j], history[i]
	}
	if turn.Conversation.SystemPrompt != "" {
		history = append([]service.Message{{Role: "system", Content: turn.Conversation.SystemPrompt}}, history...)
	}
	history, err = loopgov.New(loopgov.Config{WindowTokens: 32768}, nil).Limit(ctx, "", id, history)
	if err != nil {
		return
	}
	claimCtx, claimCancel := context.WithTimeout(ctx, 3*time.Second)
	claimed, claimErr := store.CheckpointPersonalTurn(claimCtx, owner, id, turn.LeaseToken, m)
	claimCancel()
	if claimErr != nil {
		m.Status = "failed"
		m.Error = "storage_unavailable"
		return
	}
	if !claimed.Active() {
		m = *claimed
		return
	}
	chunks := make(chan service.StreamChunk)
	called = true
	// Adapters honour ctx for both connection setup and stream reads. Forwarding
	// is cancellable even when the browser stops consuming; no detached generation.
	go func() {
		defer close(chunks)
		send := func(c service.StreamChunk) bool {
			select {
			case chunks <- c:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if sp, ok := info.provider.(service.LLMStreamProvider); ok {
			upstream, _, err := sp.ChatStream(ctx, turn.Assistant.Model, history, nil, nil)
			if err != nil {
				send(service.StreamChunk{Error: err})
				return
			}
			// Some adapters use blocking channel sends. Drain buffered chunks after
			// cancellation so their HTTP readers can unwind and release resources.
			defer func() {
				if ctx.Err() == nil {
					return
				}
				timer := time.NewTimer(5 * time.Second)
				defer timer.Stop()
				for {
					select {
					case _, open := <-upstream:
						if !open {
							return
						}
					case <-timer.C:
						return
					}
				}
			}()
			for {
				select {
				case <-ctx.Done():
					return
				case chunk, open := <-upstream:
					if !open {
						return
					}
					if !send(chunk) {
						return
					}
				}
			}
		}
		resp, err := info.provider.Chat(ctx, turn.Assistant.Model, history, nil, nil)
		if err != nil {
			send(service.StreamChunk{Error: err})
			return
		}
		if resp == nil {
			send(service.StreamChunk{Error: errors.New("empty response")})
			return
		}
		send(service.StreamChunk{Content: resp.Content, FinishReason: resp.FinishReason, Usage: &resp.Usage, ToolCalls: resp.ToolCalls, InlineImages: resp.InlineImages})
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			saveCtx, saveCancel := context.WithTimeout(ctx, 3*time.Second)
			stored, err := store.CheckpointPersonalTurn(saveCtx, owner, id, turn.LeaseToken, m)
			saveCancel()
			if err != nil {
				m.Status = "failed"
				m.Error = "storage_unavailable"
				return
			}
			if !stored.Active() {
				m = *stored
				return
			}
			if personalSSE(w, "heartbeat", map[string]string{"assistant_message_id": m.ID}) != nil {
				return
			}
		case chunk, open := <-chunks:
			if ctx.Err() != nil {
				return
			}
			if !open {
				if m.FinishReason == "" {
					m.Status = "failed"
					m.Error = "incomplete_stream"
				} else {
					m.Status = "completed"
				}
				return
			}
			if chunk.Usage != nil {
				usage = *chunk.Usage
				m.Usage = service.PersonalChatUsage{PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, CacheReadTokens: usage.CacheReadTokens, CacheWriteTokens: usage.CacheWriteTokens, ReasoningTokens: usage.ReasoningTokens, TotalTokens: usage.TotalTokenCount()}
			}
			if chunk.Error != nil {
				m.Status = "failed"
				m.Error = "provider_error"
				return
			}
			if len(chunk.ToolCalls) != 0 || len(chunk.InlineImages) != 0 {
				m.Status = "failed"
				m.Error = "unsupported_output"
				return
			}
			if !utf8.ValidString(chunk.Content) || strings.ContainsRune(chunk.Content, 0) {
				m.Status = "failed"
				m.Error = "unsupported_output"
				return
			}
			if len(m.Content)+len(chunk.Content) > personalOutputLimit {
				m.Status = "failed"
				m.Error = "output_limit"
				return
			}
			if chunk.Content != "" {
				offset := len(m.Content)
				m.Content += chunk.Content
				m.Status = "streaming"
				if personalSSE(w, "delta", map[string]any{"assistant_message_id": m.ID, "offset": offset, "content": chunk.Content}) != nil {
					return
				}
			}
			if chunk.FinishReason != "" {
				finish := personalFinishReason(chunk.FinishReason)
				if finish == "" {
					m.Status = "failed"
					m.Error = "unsupported_output"
					return
				}
				m.FinishReason = finish
			}
		}
	}
}
