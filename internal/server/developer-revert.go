package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/agentloop"
	"github.com/rakunlabs/at/internal/service/workflow"
)

const developerCompactionPrompt = `Summarize this coding session so it can continue in a fresh context. The summary replaces the earlier messages: anything you leave out is lost.

Include, in this order:
1. The user's goal and any constraints or preferences they stated.
2. Decisions made and the reasons for them.
3. Work completed: files created or changed (exact paths), commands run and their outcome, test results, facts established about the codebase.
4. Open questions, failing tests or errors not yet resolved, and the next steps.

Write it as plain notes addressed to yourself. Do not call tools. Do not answer the conversation; only summarize it.`

// compactDeveloperSession asks the session's model (tools off) to summarize
// what it currently sees and stores the answer as a compaction message. Later
// model calls start there; nothing is deleted.
func (s *Server) compactDeveloperSession(r *http.Request, stream *runEventStream, h *developerRuntimeHandle, session *service.DeveloperSession, focus string) {
	store := h.store
	finish := func(status, message string) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
		defer cancel()
		updated, _ := store.SetDeveloperSessionRuntime(ctx, session.ID, status, message)
		if status == service.DeveloperSessionFailed || status == service.DeveloperSessionCancelled {
			stream.send(map[string]any{"type": "status", "session": updated})
			stream.fail(message)
			return
		}
		stream.send(map[string]any{"type": "done", "session": updated})
	}
	if session.Provider == "" {
		finish(service.DeveloperSessionFailed, "choose a model for this session first")
		return
	}
	if err := service.CheckExecution(r.Context(), service.ExecutionAction{Kind: "resource", Name: "providers.use", ResourceID: session.Provider}); err != nil {
		finish(service.DeveloperSessionFailed, err.Error())
		return
	}
	info, err := s.getExecutionProviderInfo(r.Context(), session.Provider)
	if err != nil {
		finish(service.DeveloperSessionFailed, err.Error())
		return
	}
	model := session.Model
	if model == "" {
		model = info.defaultModel
	}
	records, err := store.ListDeveloperSessionMessages(r.Context(), session.ID)
	if err != nil {
		finish(service.DeveloperSessionFailed, err.Error())
		return
	}
	if len(records) == 0 {
		finish(service.DeveloperSessionCompleted, "")
		return
	}
	running, _ := store.SetDeveloperSessionRuntime(r.Context(), session.ID, service.DeveloperSessionRunning, "")
	stream.send(map[string]any{"type": "status", "session": running})
	stream.send(map[string]any{"type": "turn_start"})

	request := developerCompactionPrompt
	if focus != "" {
		request += "\n\nFocus the summary on: " + focus
	}
	// Tools are off, so the conversation's tool calls are flattened to text:
	// some providers refuse tool blocks in a request that declares no tools.
	messages := developerMessages(records, "", session.ProjectPath, "")
	messages = append(developerFlattenTools(messages), service.Message{Role: "user", Content: request})
	provider := service.ScopedExecutionProvider(info.provider, session.Provider)
	resp, callMessages, latency, err := agentloop.CallProviderStream(r.Context(), s.loopGov, provider, model, "developer:"+session.ID, session.ID, messages, nil, session.ReasoningEffort, func(delta agentloop.StreamDelta) {
		if delta.Content != "" {
			stream.send(map[string]any{"type": "delta", "content": delta.Content})
		}
	})
	if recordUsage := s.recordUsageFunc(); recordUsage != nil {
		event := workflow.UsageEvent{UserID: session.OwnerUserID, Source: "developer", TaskID: session.ID, Model: model, Provider: session.Provider, LatencyMs: latency, Status: "ok"}
		if resp != nil {
			event.Usage = resp.Usage
		}
		if err != nil {
			event.Status, event.ErrorCode, event.ErrorMessage = "error", classifyHTTPError(err), err.Error()
		}
		_ = recordUsage(r.Context(), event)
	}
	responseBody, _ := json.Marshal(resp)
	s.recordLLMCallAsync(r.Context(), llmAuditParams{
		source: "developer", endpoint: "developer_compaction", sessionID: session.ID,
		requestedModel: session.Provider + "/" + model, fullModel: session.Provider + "/" + model,
		requestBody: agentloop.GenerationRequestJSON(model, callMessages, nil, session.ReasoningEffort), responseBody: responseBody,
		latencyMs: latency, status: map[bool]string{true: "error", false: "success"}[err != nil],
		metadata: developerObservationMetadata(session, map[string]any{"compaction": true}),
	})
	if err != nil {
		status := service.DeveloperSessionFailed
		if errors.Is(err, context.Canceled) {
			status = service.DeveloperSessionCancelled
		}
		finish(status, err.Error())
		return
	}
	summary := strings.TrimSpace(resp.Content)
	if summary == "" {
		finish(service.DeveloperSessionFailed, "the model returned an empty summary; nothing was compacted")
		return
	}
	text := "[Summary of the earlier conversation, written to continue it in a fresh context."
	if focus != "" {
		text += " The user asked it to focus on: " + focus
	}
	text += "]\n\n" + summary
	saved, err := store.AppendDeveloperSessionMessage(r.Context(), service.DeveloperSessionMessage{
		SessionID: session.ID, Role: "assistant", Kind: service.DeveloperMessageKindCompaction,
		Content: []service.ContentBlock{{Type: "text", Text: text}},
	})
	if err != nil {
		finish(service.DeveloperSessionFailed, err.Error())
		return
	}
	stream.send(map[string]any{"type": "message", "message": saved})
	finish(service.DeveloperSessionCompleted, "")
}

// developerFlattenTools rewrites tool calls and results as text.
func developerFlattenTools(messages []service.Message) []service.Message {
	out := make([]service.Message, 0, len(messages))
	for _, message := range messages {
		blocks, ok := message.Content.([]service.ContentBlock)
		if !ok {
			out = append(out, message)
			continue
		}
		var parts []string
		for _, block := range blocks {
			switch block.Type {
			case "text":
				parts = append(parts, block.Text)
			case "tool_use":
				input, _ := json.Marshal(block.Input)
				parts = append(parts, fmt.Sprintf("[called %s %s]", block.Name, clipRunes(string(input), 2000)))
			case "tool_result":
				parts = append(parts, fmt.Sprintf("[tool result]\n%s", clipRunes(block.ContentText(), 4000)))
			case "image", "document":
				parts = append(parts, "[attached file]")
			}
		}
		role := message.Role
		if role == "tool" {
			role = "user"
		}
		if text := strings.TrimSpace(strings.Join(parts, "\n")); text != "" {
			out = append(out, service.Message{Role: role, Content: text})
		}
	}
	return out
}

func clipRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// RevertDeveloperSessionAPI is undo: it rewinds the transcript to before a
// user message (the latest one by default, or an earlier one, or the one
// before the current undo point) and restores the files that message's run
// changed. The hidden messages are deleted only when the next prompt is
// sent, so unrevert can bring everything back.
func (s *Server) RevertDeveloperSessionAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MessageID string `json:"message_id"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpResponse(w, "invalid request body", http.StatusBadRequest)
			return
		}
	}
	s.withDeveloperRevert(w, r, func(r *http.Request, h *developerRuntimeHandle, session *service.DeveloperSession) (*service.DeveloperSession, []string, error) {
		records, err := h.store.ListDeveloperSessionMessages(r.Context(), session.ID)
		if err != nil {
			return nil, nil, err
		}
		target := -1
		limit := len(records)
		if session.RevertMessageID != "" {
			for i := range records {
				if records[i].ID == session.RevertMessageID {
					limit = i
					break
				}
			}
		}
		for i := limit - 1; i >= 0; i-- {
			if records[i].Role != "user" || records[i].Kind != "" {
				continue
			}
			if req.MessageID == "" || records[i].ID == req.MessageID {
				target = i
				break
			}
		}
		if target < 0 {
			return nil, nil, &developerRevertError{"there is nothing to undo"}
		}
		if records[target].Snapshot == "" {
			return nil, nil, &developerRevertError{"this message has no file snapshot (git was not available in the space when it ran)"}
		}
		// Redo returns to the files as they are now, unless an undo is already
		// in progress: then "now" is the state that undo produced, and the
		// state to redo to is the one recorded by the first undo.
		redo := session.RevertSnapshot
		if session.RevertMessageID == "" {
			if redo = s.takeDeveloperSnapshot(r.Context(), h, session.ProjectPath); redo == "" {
				return nil, nil, &developerRevertError{"could not record the current files; is git installed in the space?"}
			}
		}
		changed, err := s.restoreDeveloperSnapshot(r.Context(), h, session.ProjectPath, records[target].Snapshot)
		if err != nil {
			return nil, nil, err
		}
		updated, err := h.store.SetDeveloperSessionRevert(r.Context(), session.ID, records[target].ID, redo)
		return updated, changed, err
	})
}

// UnrevertDeveloperSessionAPI is redo: it shows the hidden messages again and
// returns the files to how they were before the first undo.
func (s *Server) UnrevertDeveloperSessionAPI(w http.ResponseWriter, r *http.Request) {
	s.withDeveloperRevert(w, r, func(r *http.Request, h *developerRuntimeHandle, session *service.DeveloperSession) (*service.DeveloperSession, []string, error) {
		if session.RevertMessageID == "" {
			return nil, nil, &developerRevertError{"there is nothing to redo"}
		}
		var changed []string
		if session.RevertSnapshot != "" {
			var err error
			if changed, err = s.restoreDeveloperSnapshot(r.Context(), h, session.ProjectPath, session.RevertSnapshot); err != nil {
				return nil, nil, err
			}
		}
		updated, err := h.store.SetDeveloperSessionRevert(r.Context(), session.ID, "", "")
		return updated, changed, err
	})
}

type developerRevertError struct{ message string }

func (e *developerRevertError) Error() string { return e.message }

// withDeveloperRevert holds the session's run receipt while files are
// restored, so an undo cannot interleave with a running turn on any replica.
func (s *Server) withDeveloperRevert(w http.ResponseWriter, r *http.Request, apply func(*http.Request, *developerRuntimeHandle, *service.DeveloperSession) (*service.DeveloperSession, []string, error)) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	session, err := store.GetDeveloperSession(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	if session.Status == service.DeveloperSessionRunning || strings.HasPrefix(session.Status, "waiting") {
		httpResponse(w, "finish or stop the current turn before undoing", http.StatusConflict)
		return
	}
	runs, ok := s.store.(service.DeveloperRunStorer)
	if !ok {
		httpResponse(w, "durable developer run store unavailable", http.StatusServiceUnavailable)
		return
	}
	h, ok := s.developerRuntime(w, r)
	if !ok {
		return
	}
	runID := ulid.Make().String()
	if err := runs.AcquireDeveloperRun(r.Context(), session.ID, runID); err != nil {
		developerSpaceError(w, err)
		return
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
		defer cancel()
		_ = runs.ReleaseDeveloperRun(ctx, session.ID, runID)
	}()
	ctx := service.ContextWithDeveloperRun(r.Context(), runID)
	if s.containerManager != nil {
		runtimeCtx, done, err := s.containerManager.BeginRuntimeRun(ctx)
		if err != nil {
			httpResponse(w, err.Error(), http.StatusConflict)
			return
		}
		defer done()
		ctx = runtimeCtx
	}
	r = r.WithContext(ctx)
	updated, changed, err := apply(r, h, session)
	if err != nil {
		var refusal *developerRevertError
		if errors.As(err, &refusal) {
			httpResponse(w, refusal.message, http.StatusConflict)
			return
		}
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, map[string]any{"session": updated, "changed": changed}, http.StatusOK)
}
