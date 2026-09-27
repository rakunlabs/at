package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/agentloop"
	"github.com/rakunlabs/at/internal/service/blob"
	"github.com/rakunlabs/at/internal/service/workflow"
)

var developerAgentTools = []service.Tool{
	{Name: "read_file", Description: "Read a UTF-8 file in the project. Paths are relative to the project folder.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}}},
	{Name: "list_files", Description: "List files in the project (or in one sub-folder). .git and node_modules are skipped.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string", "description": "Optional sub-folder"}}}},
	{Name: "search", Description: "Search file contents with a regular expression. Returns path:line:text.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"pattern": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}}, "required": []string{"pattern"}}},
	{Name: "git_status", Description: "Read porcelain v2 Git status for the project.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
	{Name: "git_diff", Description: "Read the current Git diff for the project.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"staged": map[string]any{"type": "boolean"}}}},
	{Name: "write_file", Description: "Create or replace one UTF-8 file in the project with the full new content.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}}, "required": []string{"path", "content"}}},
	{Name: "edit_file", Description: "Replace exactly one occurrence of old_text with new_text in a file. Prefer this over write_file for small changes to large files.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "old_text": map[string]any{"type": "string"}, "new_text": map[string]any{"type": "string"}}, "required": []string{"path", "old_text", "new_text"}}},
	{Name: "run_command", Description: "Run one executable with an argv array in the project folder. Requires user approval by default.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}, "args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}, "required": []string{"command"}}},
	{Name: "ask_user", Description: "Pause and ask the user one necessary question.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"question": map[string]any{"type": "string"}}, "required": []string{"question"}}},
}

const developerSnapshotMaxBytes = 16 << 20

type activeDeveloperSession struct {
	cancel context.CancelFunc
}

func developerProfile(space *service.DeveloperSpace, mode string) service.DeveloperAgentProfile {
	switch mode {
	case service.DeveloperModePlan:
		return space.Config.Plan
	case service.DeveloperModeReview:
		return space.Config.Review
	default:
		return space.Config.Build
	}
}

func developerToolAction(call service.ToolCall) (string, string) {
	switch call.Name {
	case "read_file":
		return "read", stringArg(call.Arguments, "path")
	case "list_files":
		return "glob", "*"
	case "search":
		return "grep", stringArg(call.Arguments, "path")
	case "git_status":
		return "git", "status"
	case "git_diff":
		return "git", "diff"
	case "write_file", "edit_file":
		return "edit", stringArg(call.Arguments, "path")
	case "run_command":
		return "shell", stringArg(call.Arguments, "command")
	case "ask_user":
		return "question", "user"
	default:
		return "tool", call.Name
	}
}

func developerToolEffect(profile service.DeveloperAgentProfile, call service.ToolCall) string {
	action, resource := developerToolAction(call)
	effect := "ask"
	for _, rule := range profile.Rules {
		if rule.Action != action && rule.Action != "*" {
			continue
		}
		matched := rule.Resource == "*" || rule.Resource == resource
		if !matched {
			matched, _ = filepath.Match(rule.Resource, resource)
		}
		if matched {
			effect = rule.Effect
		}
	}
	return effect
}

func developerSliceArg(args map[string]any, key string) []any {
	value, _ := args[key].([]any)
	return value
}

// developerProjectFilePath validates a tool path relative to the project.
// .git is refused so the agent cannot rewrite hooks or config that would run
// on the user's next git command.
func developerProjectFilePath(raw string) (string, error) {
	clean, err := service.CleanDeveloperPath(raw)
	if err != nil {
		return "", err
	}
	if clean == "" {
		return "", errors.New("path is required")
	}
	if clean == ".git" || strings.HasPrefix(clean, ".git/") {
		return "", fmt.Errorf("the Git metadata directory is not available to file tools")
	}
	return clean, nil
}

func developerMessages(records []service.DeveloperSessionMessage, system, project string) []service.Message {
	messages := make([]service.Message, 0, len(records)+1)
	folder := "/workspace"
	if project != "" {
		folder = "/workspace/" + project
	}
	base := "You are AT's coding agent working inside the user's persistent development space. The active project folder is " + folder + "; every tool path is relative to it. Use typed tools instead of inventing filesystem or command results. Inspect before editing, keep changes scoped, prefer edit_file for small changes, and use ask_user only when a necessary decision cannot be inferred. Never claim a command, test, commit, or push happened unless its tool result confirms it. Format answers in Markdown."
	if system != "" {
		base += "\n\n" + system
	}
	messages = append(messages, service.Message{Role: "system", Content: base})
	for _, record := range records {
		messages = append(messages, service.Message{Role: record.Role, Content: record.Content})
	}
	return messages
}

// developerStream writes one session's events as server-sent events. Writes
// are serialized because tool observations may be reported from helpers.
type developerStream struct {
	mu      sync.Mutex
	w       http.ResponseWriter
	flusher http.Flusher
}

func newDeveloperStream(w http.ResponseWriter) (*developerStream, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpResponse(w, "streaming not supported", http.StatusInternalServerError)
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	return &developerStream{w: w, flusher: flusher}, true
}

func (s *developerStream) send(event map[string]any) {
	if s == nil {
		return
	}
	data, _ := json.Marshal(event)
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(s.w, "data: %s\n\n", data)
	s.flusher.Flush()
}

// fail reports a terminal error on the open stream. The HTTP status is already
// committed, so the error travels in-band and the client shows it.
func (s *developerStream) fail(message string) {
	s.send(map[string]any{"type": "error", "error": message})
}

func (s *Server) RunDeveloperSessionAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	session, err := store.GetDeveloperSession(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Prompt) == "" {
		httpResponse(w, "prompt is required", http.StatusBadRequest)
		return
	}
	h, ok := s.developerRuntime(w, r)
	if !ok {
		return
	}
	session, err = store.BeginDeveloperSessionRun(r.Context(), session.ID)
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	r, done := s.trackDeveloperSessionRequest(r, session.ID)
	defer done()
	if session.Title == "" {
		title := strings.Join(strings.Fields(req.Prompt), " ")
		if len([]rune(title)) > 60 {
			title = string([]rune(title)[:57]) + "…"
		}
		if renamed, err := store.RenameDeveloperSession(r.Context(), session.ID, title); err == nil {
			session = renamed
		}
	}
	if _, err := store.AppendDeveloperSessionMessage(r.Context(), service.DeveloperSessionMessage{SessionID: session.ID, Role: "user", Content: req.Prompt}); err != nil {
		_, _ = store.SetDeveloperSessionRuntime(r.Context(), session.ID, service.DeveloperSessionFailed, err.Error())
		developerSpaceError(w, err)
		return
	}
	stream, ok := newDeveloperStream(w)
	if !ok {
		_, _ = store.SetDeveloperSessionRuntime(r.Context(), session.ID, service.DeveloperSessionFailed, "streaming not supported")
		return
	}
	s.runDeveloperSession(r, stream, h, session, "")
}

// runDeveloperSession drives the agent until it answers, pauses for the user,
// or fails. Every outcome ends the stream with exactly one terminal event:
// "done" (with the session's new status) or "error".
func (s *Server) runDeveloperSession(r *http.Request, stream *developerStream, h *developerRuntimeHandle, session *service.DeveloperSession, traceID string) {
	store := h.store
	finish := func(status, message string) {
		updated, _ := store.SetDeveloperSessionRuntime(context.WithoutCancel(r.Context()), session.ID, status, message)
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
	kit, err := s.buildDeveloperToolkit(r.Context(), h.space, session)
	if err != nil {
		finish(service.DeveloperSessionFailed, err.Error())
		return
	}
	defer kit.Close()
	maxIterations := s.loopGov.ClampIterations(kit.maxIterations, 0)
	if maxIterations <= 0 {
		maxIterations = 20
	}
	running, _ := store.SetDeveloperSessionRuntime(r.Context(), session.ID, service.DeveloperSessionRunning, "")
	stream.send(map[string]any{"type": "status", "session": running})
	if traceID == "" {
		traceID = ulid.Make().String()
	}
	provider := service.ScopedExecutionProvider(info.provider, session.Provider)
	emptyRepairs := 0
	for iteration := 0; iteration < maxIterations; iteration++ {
		records, err := store.ListDeveloperSessionMessages(r.Context(), session.ID)
		if err != nil {
			finish(service.DeveloperSessionFailed, err.Error())
			return
		}
		messages := developerMessages(records, kit.systemPrompt, session.ProjectPath)
		step := len(records)
		s.captureDeveloperSnapshot(r, session, h, step, "before")
		stream.send(map[string]any{"type": "turn_start"})
		resp, callMessages, latency, err := agentloop.CallProviderStream(r.Context(), s.loopGov, provider, model, "developer:"+session.ID, session.ID, messages, kit.tools, func(delta agentloop.StreamDelta) {
			event := map[string]any{"type": "delta"}
			if delta.Content != "" {
				event["content"] = delta.Content
			}
			if delta.Reasoning != "" {
				event["reasoning"] = delta.Reasoning
			}
			stream.send(event)
		})
		if recordUsage := s.recordUsageFunc(); recordUsage != nil {
			event := workflow.UsageEvent{
				UserID: session.OwnerUserID, Source: "developer", TaskID: session.ID,
				Model: model, Provider: session.Provider, LatencyMs: latency, Status: "ok",
			}
			if resp != nil {
				event.Usage = resp.Usage
			}
			if err != nil {
				event.Status = "error"
				event.ErrorCode = classifyHTTPError(err)
				event.ErrorMessage = err.Error()
			}
			_ = recordUsage(r.Context(), event)
		}
		responseBody, _ := json.Marshal(resp)
		generationID := s.recordLLMCallAsync(r.Context(), llmAuditParams{
			source: "developer", endpoint: "developer_session", traceID: traceID, sessionID: session.ID,
			requestedModel: session.Provider + "/" + model, fullModel: session.Provider + "/" + model,
			requestBody: agentloop.GenerationRequestJSON(model, callMessages, kit.tools, ""), responseBody: responseBody,
			latencyMs: latency, status: map[bool]string{true: "error", false: "success"}[err != nil],
			finishReason: func() string {
				if resp != nil {
					return resp.FinishReason
				}
				return ""
			}(),
			usage: func() service.Usage {
				if resp != nil {
					return resp.Usage
				}
				return service.Usage{}
			}(),
			errMsg: func() string {
				if err != nil {
					return err.Error()
				}
				return ""
			}(),
			metadata: developerObservationMetadata(session, map[string]any{"iteration": iteration}),
		})
		if err != nil {
			status := service.DeveloperSessionFailed
			if errors.Is(err, context.Canceled) {
				status = service.DeveloperSessionCancelled
			}
			finish(status, err.Error())
			return
		}
		if resp.Refusal != "" && resp.Content == "" {
			resp.Content = resp.Refusal
		}
		if resp.Content == "" && len(resp.ToolCalls) == 0 {
			if emptyRepairs == 0 {
				emptyRepairs++
				if _, appendErr := store.AppendDeveloperSessionMessage(r.Context(), service.DeveloperSessionMessage{
					SessionID: session.ID, Role: "system", Content: "The previous model response was empty. Provide a concrete answer or a valid tool call now.",
				}); appendErr != nil {
					finish(service.DeveloperSessionFailed, appendErr.Error())
					return
				}
				continue
			}
			finish(service.DeveloperSessionFailed, "model returned an empty response twice")
			return
		}
		emptyRepairs = 0
		assistant := agentloop.AssistantMessage(resp)
		saved, err := store.AppendDeveloperSessionMessage(r.Context(), service.DeveloperSessionMessage{SessionID: session.ID, Role: "assistant", Content: assistant.Content})
		if err != nil {
			finish(service.DeveloperSessionFailed, err.Error())
			return
		}
		stream.send(map[string]any{"type": "message", "message": saved})
		if len(resp.ToolCalls) == 0 {
			s.captureDeveloperSnapshot(r, session, h, step, "after")
			if resp.FinishReason == "length" || resp.FinishReason == "content_filter" || resp.Refusal != "" {
				message := "model response stopped with finish reason " + resp.FinishReason
				if resp.Refusal != "" {
					message = "model refused the coding request"
				}
				finish(service.DeveloperSessionFailed, message)
				return
			}
			finish(service.DeveloperSessionCompleted, "")
			return
		}
		waitingKind := ""
		for _, call := range resp.ToolCalls {
			if call.Name == "ask_user" {
				waitingKind = "question"
				break
			}
			if kit.effect(call) == "ask" {
				waitingKind = "permission"
			}
		}
		if waitingKind != "" {
			pending, err := store.SaveDeveloperPendingTool(r.Context(), service.DeveloperPendingTool{
				SessionID: session.ID, Kind: waitingKind, ToolCalls: resp.ToolCalls, TraceID: traceID,
				ParentObservationID: generationID, Step: step,
			})
			if err != nil {
				finish(service.DeveloperSessionFailed, err.Error())
				return
			}
			status := service.DeveloperSessionWaitingPermission
			if waitingKind == "question" {
				status = service.DeveloperSessionWaitingQuestion
			}
			updated, _ := store.SetDeveloperSessionRuntime(r.Context(), session.ID, status, "")
			stream.send(map[string]any{"type": "pending", "pending_tool": pending})
			stream.send(map[string]any{"type": "done", "session": updated})
			return
		}
		blocks := make([]service.ContentBlock, 0, len(resp.ToolCalls))
		for _, call := range resp.ToolCalls {
			stream.send(map[string]any{"type": "tool_start", "tool_id": call.ID, "tool_name": call.Name})
			blocks = append(blocks, s.runDeveloperToolCall(r, stream, h, session, kit, traceID, generationID, call, kit.effect(call) == "allow", "tool denied by the active agent profile"))
		}
		saved, err = store.AppendDeveloperSessionMessage(r.Context(), service.DeveloperSessionMessage{SessionID: session.ID, Role: "tool", Content: blocks})
		if err != nil {
			finish(service.DeveloperSessionFailed, err.Error())
			return
		}
		stream.send(map[string]any{"type": "message", "message": saved})
		s.captureDeveloperSnapshot(r, session, h, step, "after")
	}
	finish(service.DeveloperSessionFailed, "iteration limit reached")
}

// runDeveloperToolCall executes (or refuses) one call, records it, and tells
// the page which files changed so open editors can reload.
func (s *Server) runDeveloperToolCall(r *http.Request, stream *developerStream, h *developerRuntimeHandle, session *service.DeveloperSession, kit *developerToolkit, traceID, parentID string, call service.ToolCall, execute bool, refusal string) service.ContentBlock {
	output := refusal
	var toolErr error
	started := time.Now()
	if execute {
		if isDeveloperContainerTool(call.Name) {
			output, toolErr = s.executeDeveloperTool(r, h, session.ProjectPath, call, kit.toolTimeout)
		} else {
			timeout := kit.toolTimeout
			if timeout <= 0 {
				timeout = 60
			}
			ctx, cancel := context.WithTimeout(kit.agentContext(r.Context(), session), time.Duration(timeout)*time.Second)
			output, toolErr = s.executeDeveloperAgentTool(ctx, kit, call)
			cancel()
		}
		if toolErr != nil {
			output = "tool error: " + toolErr.Error() + developerOutputSuffix(output)
		}
	}
	output, block := agentloop.ToolResult(s.loopGov, session.ID, call, output)
	s.recordDeveloperToolObservation(r, session, traceID, parentID, call, output, time.Since(started).Milliseconds(), toolErr)
	event := map[string]any{"type": "tool_result", "tool_id": call.ID, "tool_name": call.Name, "error": toolErr != nil}
	if execute && toolErr == nil && (call.Name == "write_file" || call.Name == "edit_file") {
		if rel, err := developerProjectFilePath(stringArg(call.Arguments, "path")); err == nil {
			event["changed"] = []string{developerJoin(session.ProjectPath, rel)}
		}
	}
	if execute && call.Name == "run_command" {
		// A command may have touched anything; the page refreshes its tree.
		event["changed_tree"] = true
	}
	stream.send(event)
	return block
}

func developerObservationMetadata(session *service.DeveloperSession, extra map[string]any) map[string]any {
	metadata := map[string]any{"mode": session.Mode, "project_path": session.ProjectPath}
	if session.AgentID != "" {
		metadata["agent_id"] = session.AgentID
	}
	for key, value := range extra {
		metadata[key] = value
	}
	return metadata
}

func developerOutputSuffix(output string) string {
	if strings.TrimSpace(output) == "" {
		return ""
	}
	return "\n" + output
}

func developerJoin(project, rel string) string {
	if project == "" {
		return rel
	}
	return project + "/" + rel
}

func (s *Server) captureDeveloperSnapshot(r *http.Request, session *service.DeveloperSession, h *developerRuntimeHandle, step int, phase string) {
	objects, ok := s.store.(service.StorageObjectStorer)
	if !ok {
		return
	}
	settingsStore, ok := s.store.(service.StorageSettingsStorer)
	if !ok {
		return
	}
	settings, err := settingsStore.GetStorageSettings(r.Context())
	if err != nil || !settings.Enabled() {
		return
	}
	target, err := blob.New(*settings)
	if err != nil {
		return
	}
	workDir := service.DeveloperAbsolutePath(session.ProjectPath)
	head, _, headCode, _ := h.exec(r.Context(), s, workDir, "git", "rev-parse", "HEAD")
	if headCode != 0 {
		return // not a repository; nothing to snapshot
	}
	diff, _, code, err := h.exec(r.Context(), s, workDir, "git", "diff", "--binary", "--no-ext-diff", "HEAD", "--")
	if err != nil || code != 0 {
		return
	}
	untracked, _, _, _ := h.exec(r.Context(), s, workDir, "git", "ls-files", "-z", "--others", "--exclude-standard")
	untrackedNames := strings.Split(strings.TrimSuffix(untracked, "\x00"), "\x00")
	if untracked == "" {
		untrackedNames = nil
	}
	truncated := len(diff) > developerSnapshotMaxBytes
	if truncated {
		diff = diff[:developerSnapshotMaxBytes]
	}
	if !truncated {
		for index, name := range untrackedNames {
			if name == "" {
				continue
			}
			if index >= 100 {
				truncated = true
				break
			}
			patch, _, patchCode, patchErr := h.exec(r.Context(), s, workDir, "git", "diff", "--binary", "--no-index", "--", "/dev/null", name)
			if patchErr != nil || (patchCode != 0 && patchCode != 1) {
				continue
			}
			if len(diff)+len(patch) > developerSnapshotMaxBytes {
				truncated = true
				break
			}
			diff += patch
		}
	}
	payload, _ := json.Marshal(map[string]any{
		"version": 1, "head_sha": strings.TrimSpace(head), "diff": diff,
		"untracked": untrackedNames, "truncated": truncated,
	})
	logicalPath := fmt.Sprintf("developer-sessions/%s/%06d-%s.json", session.ID, step, phase)
	key := "workspaces/" + session.WorkspaceID + "/snapshots/" + logicalPath
	if settings.Backend == service.StorageBackendS3 {
		key = service.NormalizeStoragePrefix(settings.S3.Prefix) + key
	}
	if err := target.Put(r.Context(), key, "application/json", payload); err != nil {
		return
	}
	sum := sha256.Sum256(payload)
	object, err := objects.PutStorageObject(r.Context(), service.StoredObject{
		WorkspaceID: session.WorkspaceID, OwnerUserID: session.OwnerUserID,
		Namespace: service.StorageNamespaceSnapshots, Path: logicalPath,
		Backend: settings.Backend, StorageKey: key, ContentType: "application/json",
		SizeBytes: int64(len(payload)), Checksum: hex.EncodeToString(sum[:]),
	})
	if err != nil {
		return
	}
	_, _ = h.store.SaveDeveloperSessionSnapshot(r.Context(), service.DeveloperSessionSnapshot{
		SessionID: session.ID, Step: step, Phase: phase, HeadSHA: strings.TrimSpace(head), StorageObjectID: object.ID,
	})
}

// ConfirmDeveloperSessionToolAPI approves or rejects the waiting tool calls and
// continues the run on a new event stream.
func (s *Server) ConfirmDeveloperSessionToolAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Approved bool `json:"approved"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, "invalid request body", http.StatusBadRequest)
		return
	}
	s.resumeDeveloperSession(w, r, "permission", func(r *http.Request, stream *developerStream, h *developerRuntimeHandle, session *service.DeveloperSession, kit *developerToolkit, pending *service.DeveloperPendingTool) []service.ContentBlock {
		blocks := make([]service.ContentBlock, 0, len(pending.ToolCalls))
		for _, call := range pending.ToolCalls {
			effect := kit.effect(call)
			refusal := "tool denied by the active agent profile"
			if effect == "ask" && !req.Approved {
				refusal = "tool execution rejected by the user"
			}
			execute := effect == "allow" || (effect == "ask" && req.Approved)
			stream.send(map[string]any{"type": "tool_start", "tool_id": call.ID, "tool_name": call.Name})
			blocks = append(blocks, s.runDeveloperToolCall(r, stream, h, session, kit, pending.TraceID, pending.ParentObservationID, call, execute, refusal))
		}
		return blocks
	})
}

func (s *Server) AnswerDeveloperSessionQuestionAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Answer) == "" {
		httpResponse(w, "answer is required", http.StatusBadRequest)
		return
	}
	s.resumeDeveloperSession(w, r, "question", func(r *http.Request, stream *developerStream, _ *developerRuntimeHandle, session *service.DeveloperSession, _ *developerToolkit, pending *service.DeveloperPendingTool) []service.ContentBlock {
		blocks := make([]service.ContentBlock, 0, len(pending.ToolCalls))
		for _, call := range pending.ToolCalls {
			output := "tool deferred while waiting for the user's answer"
			if call.Name == "ask_user" {
				output = strings.TrimSpace(req.Answer)
			}
			_, block := agentloop.ToolResult(s.loopGov, session.ID, call, output)
			blocks = append(blocks, block)
			s.recordDeveloperToolObservation(r, session, pending.TraceID, pending.ParentObservationID, call, output, 0, nil)
		}
		return blocks
	})
}

func (s *Server) resumeDeveloperSession(w http.ResponseWriter, r *http.Request, kind string, resolve func(*http.Request, *developerStream, *developerRuntimeHandle, *service.DeveloperSession, *developerToolkit, *service.DeveloperPendingTool) []service.ContentBlock) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	session, err := store.GetDeveloperSession(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	h, ok := s.developerRuntime(w, r)
	if !ok {
		return
	}
	pending, err := store.ClaimDeveloperPendingTool(r.Context(), session.ID)
	if err != nil || pending == nil {
		httpResponse(w, "nothing is waiting for a "+kind, http.StatusConflict)
		return
	}
	if pending.Kind != kind {
		pending.State = "pending"
		_, _ = store.SaveDeveloperPendingTool(r.Context(), *pending)
		httpResponse(w, "the session is waiting for a "+pending.Kind+", not a "+kind, http.StatusConflict)
		return
	}
	r, done := s.trackDeveloperSessionRequest(r, session.ID)
	defer done()
	stream, ok := newDeveloperStream(w)
	if !ok {
		return
	}
	kit, err := s.buildDeveloperToolkit(r.Context(), h.space, session)
	if err != nil {
		// Keep the calls pending so the user can retry once the agent is fixed.
		pending.State = "pending"
		_, _ = store.SaveDeveloperPendingTool(context.WithoutCancel(r.Context()), *pending)
		stream.fail(err.Error())
		return
	}
	blocks := resolve(r, stream, h, session, kit, pending)
	kit.Close()
	saved, err := store.ResolveDeveloperPendingTool(r.Context(), session.ID, blocks)
	if err != nil {
		stream.fail(err.Error())
		return
	}
	stream.send(map[string]any{"type": "message", "message": saved})
	s.captureDeveloperSnapshot(r, session, h, pending.Step, "after")
	s.runDeveloperSession(r, stream, h, session, pending.TraceID)
}

func (s *Server) trackDeveloperSessionRequest(r *http.Request, sessionID string) (*http.Request, func()) {
	ctx, cancel := context.WithCancel(r.Context())
	active := &activeDeveloperSession{cancel: cancel}
	s.activeDeveloperSessions.Store(sessionID, active)
	return r.WithContext(ctx), func() {
		s.activeDeveloperSessions.CompareAndDelete(sessionID, active)
		cancel()
	}
}

func (s *Server) CancelDeveloperSessionAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	session, err := store.GetDeveloperSession(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	if value, ok := s.activeDeveloperSessions.Load(session.ID); ok {
		value.(*activeDeveloperSession).cancel()
	}
	_ = store.DeleteDeveloperPendingTool(r.Context(), session.ID)
	updated, err := store.SetDeveloperSessionRuntime(r.Context(), session.ID, service.DeveloperSessionCancelled, "cancelled by user")
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	httpResponseJSON(w, updated, http.StatusOK)
}

func (s *Server) recordDeveloperToolObservation(r *http.Request, session *service.DeveloperSession, traceID, parentID string, call service.ToolCall, output string, latencyMs int64, callErr error) {
	input, _ := json.Marshal(call.Arguments)
	params := llmAuditParams{
		source: "developer", endpoint: "developer_session", traceID: traceID, sessionID: session.ID,
		obsType: service.ObservationTool, parentObservationID: parentID, name: call.Name,
		input: string(input), output: output, latencyMs: latencyMs, status: "success",
		metadata: developerObservationMetadata(session, nil),
	}
	if callErr != nil {
		params.status = "error"
		params.level = service.ObservationLevelError
		params.errMsg = callErr.Error()
	}
	s.recordLLMCallAsync(r.Context(), params)
}

// executeDeveloperTool runs one agent tool inside the project folder. File
// tools go through the same containment script as the browser's file API.
func (s *Server) executeDeveloperTool(r *http.Request, h *developerRuntimeHandle, project string, call service.ToolCall, timeoutSeconds int) (string, error) {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 60
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	workDir := service.DeveloperAbsolutePath(project)
	switch call.Name {
	case "read_file":
		rel, err := developerProjectFilePath(stringArg(call.Arguments, "path"))
		if err != nil {
			return "", err
		}
		raw, err := s.runDeveloperFS(ctx, h, nil, "read", project, rel)
		if err != nil {
			return "", err
		}
		var file struct {
			Content  string `json:"content"`
			Binary   bool   `json:"binary"`
			TooLarge bool   `json:"too_large"`
			Size     int64  `json:"size"`
		}
		if err := json.Unmarshal(raw, &file); err != nil {
			return "", err
		}
		if file.Binary {
			return "", fmt.Errorf("%s is a binary file (%d bytes)", rel, file.Size)
		}
		if file.TooLarge {
			return "", fmt.Errorf("%s is %d bytes, above the 2 MiB read limit; use search or run_command to inspect it", rel, file.Size)
		}
		return file.Content, nil
	case "list_files":
		rel := ""
		if candidate := stringArg(call.Arguments, "path"); candidate != "" {
			clean, err := service.CleanDeveloperPath(candidate)
			if err != nil {
				return "", err
			}
			rel = clean
		}
		raw, err := s.runDeveloperFS(ctx, h, nil, "walk", project, rel)
		if err != nil {
			return "", err
		}
		var listing struct {
			Files     []string `json:"files"`
			Truncated bool     `json:"truncated"`
		}
		if err := json.Unmarshal(raw, &listing); err != nil {
			return "", err
		}
		out := strings.Join(listing.Files, "\n")
		if listing.Truncated {
			out += "\n[listing truncated at 10000 files; narrow the path]"
		}
		return out, nil
	case "search":
		pattern := stringArg(call.Arguments, "pattern")
		if pattern == "" {
			return "", fmt.Errorf("pattern is required")
		}
		start := ""
		if candidate := stringArg(call.Arguments, "path"); candidate != "" {
			clean, err := developerProjectFilePath(candidate)
			if err != nil {
				return "", err
			}
			start = clean
		}
		raw, err := s.runDeveloperFS(ctx, h, nil, "search", project, pattern, start)
		if err != nil {
			return "", err
		}
		var result struct {
			Matches []struct {
				Path string `json:"path"`
				Line int    `json:"line"`
				Text string `json:"text"`
			} `json:"matches"`
			Truncated bool `json:"truncated"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return "", err
		}
		var b strings.Builder
		for _, match := range result.Matches {
			fmt.Fprintf(&b, "%s:%d:%s\n", match.Path, match.Line, match.Text)
		}
		if result.Truncated {
			b.WriteString("[results truncated at 2000 matches]\n")
		}
		if b.Len() == 0 {
			return "no matches", nil
		}
		return b.String(), nil
	case "write_file":
		rel, err := developerProjectFilePath(stringArg(call.Arguments, "path"))
		if err != nil {
			return "", err
		}
		content := stringArg(call.Arguments, "content")
		if len(content) > developerWriteMaxBytes {
			return "", fmt.Errorf("content exceeds 2 MiB")
		}
		if _, err := s.runDeveloperFS(ctx, h, strings.NewReader(content), "write", project, rel, ""); err != nil {
			return "", err
		}
		return fmt.Sprintf("wrote %s (%d bytes)", rel, len(content)), nil
	case "edit_file":
		rel, err := developerProjectFilePath(stringArg(call.Arguments, "path"))
		if err != nil {
			return "", err
		}
		oldText, newText := stringArg(call.Arguments, "old_text"), stringArg(call.Arguments, "new_text")
		if oldText == "" {
			return "", fmt.Errorf("old_text is required")
		}
		raw, err := s.runDeveloperFS(ctx, h, nil, "read", project, rel)
		if err != nil {
			return "", err
		}
		var file struct {
			Content string `json:"content"`
			Version string `json:"version"`
			Binary  bool   `json:"binary"`
		}
		if err := json.Unmarshal(raw, &file); err != nil || file.Binary {
			return "", fmt.Errorf("%s is not an editable text file", rel)
		}
		switch count := strings.Count(file.Content, oldText); count {
		case 0:
			return "", fmt.Errorf("old_text was not found in %s; read the file again and copy the exact text", rel)
		case 1:
		default:
			return "", fmt.Errorf("old_text occurs %d times in %s; include more surrounding lines so it is unique", count, rel)
		}
		updated := strings.Replace(file.Content, oldText, newText, 1)
		if len(updated) > developerWriteMaxBytes {
			return "", fmt.Errorf("result exceeds 2 MiB")
		}
		if _, err := s.runDeveloperFS(ctx, h, strings.NewReader(updated), "write", project, rel, file.Version); err != nil {
			return "", err
		}
		return "edited " + rel, nil
	}

	var command string
	var args []string
	switch call.Name {
	case "git_status":
		command, args = "git", []string{"status", "--porcelain=v2", "--branch"}
	case "git_diff":
		args = []string{"diff", "--no-ext-diff"}
		if boolArg(call.Arguments, "staged") {
			args = append(args, "--cached")
		}
		command, args = "git", append(args, "--")
	case "run_command":
		command = stringArg(call.Arguments, "command")
		if command == "" || strings.ContainsRune(command, '/') {
			return "", fmt.Errorf("command must be a binary name")
		}
		for _, value := range developerSliceArg(call.Arguments, "args") {
			args = append(args, fmt.Sprint(value))
		}
	default:
		return "", fmt.Errorf("unknown developer tool %q", call.Name)
	}
	timeoutArgs := []string{"--signal=KILL", "--kill-after=1s", fmt.Sprintf("%ds", timeoutSeconds), command}
	timeoutArgs = append(timeoutArgs, args...)
	stdout, stderr, code, err := h.exec(ctx, s, workDir, "timeout", timeoutArgs...)
	if err != nil {
		return "", err
	}
	output := stdout
	if stderr != "" {
		output += "\n" + stderr
	}
	if code != 0 {
		return output, fmt.Errorf("command exited with code %d", code)
	}
	return output, nil
}
