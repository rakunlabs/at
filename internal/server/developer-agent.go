package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/agentloop"
	"github.com/rakunlabs/at/internal/service/blob"
	"github.com/rakunlabs/at/internal/service/container"
	"github.com/rakunlabs/at/internal/service/workflow"
)

var developerAgentTools = []service.Tool{
	{Name: "read_file", Description: "Read a UTF-8 file in the active worktree.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}}},
	{Name: "list_files", Description: "List files in the active worktree.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
	{Name: "search", Description: "Search text recursively in the active worktree.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"pattern": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}}, "required": []string{"pattern"}}},
	{Name: "git_status", Description: "Read porcelain v2 Git status.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
	{Name: "git_diff", Description: "Read the current Git diff.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"staged": map[string]any{"type": "boolean"}}}},
	{Name: "write_file", Description: "Replace one UTF-8 file in the active worktree.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}}, "required": []string{"path", "content"}}},
	{Name: "run_command", Description: "Run one executable with an argv array in the active worktree. Requires user approval by default.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}, "args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}, "required": []string{"command"}}},
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
	case "write_file":
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

func developerWorktreeFilePath(raw string) (string, error) {
	clean, err := normalizeStorageFilePath(raw, false)
	if err != nil {
		return "", err
	}
	if clean == ".git" || strings.HasPrefix(clean, ".git/") {
		return "", fmt.Errorf("the Git metadata directory is not available to file tools")
	}
	return clean, nil
}

func developerMessages(records []service.DeveloperSessionMessage, system string) []service.Message {
	messages := make([]service.Message, 0, len(records)+1)
	base := "You are AT's native coding agent in one isolated Git worktree. Tool paths are relative to the worktree. Use typed tools instead of inventing filesystem or command results. Inspect before editing, keep changes scoped, and use ask_user only when a necessary decision cannot be inferred. Never claim a command, test, commit, or push happened unless its tool result confirms it."
	if system != "" {
		base += "\n\n" + system
	}
	messages = append(messages, service.Message{Role: "system", Content: base})
	for _, record := range records {
		messages = append(messages, service.Message{Role: record.Role, Content: record.Content})
	}
	return messages
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
	session, err = store.BeginDeveloperSessionRun(r.Context(), session.ID)
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	r, done := s.trackDeveloperSessionRequest(r, session.ID)
	defer done()
	if _, err := store.AppendDeveloperSessionMessage(r.Context(), service.DeveloperSessionMessage{SessionID: session.ID, Role: "user", Content: req.Prompt}); err != nil {
		_, _ = store.SetDeveloperSessionRuntime(r.Context(), session.ID, service.DeveloperSessionFailed, err.Error())
		developerSpaceError(w, err)
		return
	}
	s.runDeveloperSession(w, r, store, session, "")
}

func (s *Server) runDeveloperSession(w http.ResponseWriter, r *http.Request, store service.DeveloperSpaceStorer, session *service.DeveloperSession, traceID string) {
	if session.WorktreeID == "" {
		_, _ = store.SetDeveloperSessionRuntime(r.Context(), session.ID, service.DeveloperSessionFailed, "coding session requires an available worktree")
		httpResponse(w, "coding session requires an available worktree", http.StatusConflict)
		return
	}
	worktree, err := store.GetDeveloperWorktree(r.Context(), session.WorktreeID)
	if err != nil || worktree.State != service.DeveloperWorktreeReady {
		_, _ = store.SetDeveloperSessionRuntime(r.Context(), session.ID, service.DeveloperSessionFailed, "coding session worktree is not ready")
		httpResponse(w, "coding session worktree is not ready", http.StatusConflict)
		return
	}
	_, space, cfg, scope, ok := s.developerRuntime(w, r, session.SpaceID)
	if !ok {
		_, _ = store.SetDeveloperSessionRuntime(r.Context(), session.ID, service.DeveloperSessionFailed, "developer runtime unavailable")
		return
	}
	if session.Provider == "" {
		_, _ = store.SetDeveloperSessionRuntime(r.Context(), session.ID, service.DeveloperSessionFailed, "coding session provider is required")
		httpResponse(w, "coding session provider is required", http.StatusConflict)
		return
	}
	if err := service.CheckExecution(r.Context(), service.ExecutionAction{Kind: "resource", Name: "providers.use", ResourceID: session.Provider}); err != nil {
		_, _ = store.SetDeveloperSessionRuntime(r.Context(), session.ID, service.DeveloperSessionFailed, err.Error())
		fileAccessError(w, err)
		return
	}
	info, err := s.getExecutionProviderInfo(r.Context(), session.Provider)
	if err != nil {
		_, _ = store.SetDeveloperSessionRuntime(r.Context(), session.ID, service.DeveloperSessionFailed, err.Error())
		httpResponse(w, err.Error(), http.StatusBadGateway)
		return
	}
	model := session.Model
	if model == "" {
		model = info.defaultModel
	}
	profile := developerProfile(space, session.Mode)
	maxIterations := s.loopGov.ClampIterations(profile.MaxIterations, 0)
	if maxIterations <= 0 {
		maxIterations = 20
	}
	_, _ = store.SetDeveloperSessionRuntime(r.Context(), session.ID, service.DeveloperSessionRunning, "")
	if traceID == "" {
		traceID = ulid.Make().String()
	}
	emptyRepairs := 0
	for iteration := 0; iteration < maxIterations; iteration++ {
		records, err := store.ListDeveloperSessionMessages(r.Context(), session.ID)
		if err != nil {
			_, _ = store.SetDeveloperSessionRuntime(r.Context(), session.ID, service.DeveloperSessionFailed, err.Error())
			developerSpaceError(w, err)
			return
		}
		messages := developerMessages(records, profile.SystemPrompt)
		step := len(records)
		s.captureDeveloperSnapshot(r, session, worktree, cfg, scope, step, "before")
		provider := service.ScopedExecutionProvider(info.provider, session.Provider)
		resp, callMessages, latency, err := agentloop.CallProvider(r.Context(), s.loopGov, provider, model, "developer:"+session.ID, session.ID, messages, developerAgentTools, "")
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
			requestBody: agentloop.GenerationRequestJSON(model, callMessages, developerAgentTools, ""), responseBody: responseBody,
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
			metadata: map[string]any{"iteration": iteration, "mode": session.Mode, "worktree_id": worktree.ID},
		})
		if err != nil {
			status := service.DeveloperSessionFailed
			if errors.Is(err, context.Canceled) {
				status = service.DeveloperSessionCancelled
			}
			_, _ = store.SetDeveloperSessionRuntime(context.WithoutCancel(r.Context()), session.ID, status, err.Error())
			httpResponse(w, err.Error(), http.StatusBadGateway)
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
					_, _ = store.SetDeveloperSessionRuntime(r.Context(), session.ID, service.DeveloperSessionFailed, appendErr.Error())
					developerSpaceError(w, appendErr)
					return
				}
				continue
			}
			message := "model returned an empty response twice"
			_, _ = store.SetDeveloperSessionRuntime(r.Context(), session.ID, service.DeveloperSessionFailed, message)
			httpResponse(w, message, http.StatusBadGateway)
			return
		}
		emptyRepairs = 0
		assistant := agentloop.AssistantMessage(resp)
		if _, err := store.AppendDeveloperSessionMessage(r.Context(), service.DeveloperSessionMessage{SessionID: session.ID, Role: "assistant", Content: assistant.Content}); err != nil {
			_, _ = store.SetDeveloperSessionRuntime(r.Context(), session.ID, service.DeveloperSessionFailed, err.Error())
			developerSpaceError(w, err)
			return
		}
		if len(resp.ToolCalls) == 0 {
			s.captureDeveloperSnapshot(r, session, worktree, cfg, scope, step, "after")
			if resp.FinishReason == "length" || resp.FinishReason == "content_filter" || resp.Refusal != "" {
				message := "model response stopped with finish reason " + resp.FinishReason
				if resp.Refusal != "" {
					message = "model refused the coding request"
				}
				updated, _ := store.SetDeveloperSessionRuntime(r.Context(), session.ID, service.DeveloperSessionFailed, message)
				httpResponseJSON(w, map[string]any{"session": updated, "content": resp.Content}, http.StatusConflict)
				return
			}
			updated, _ := store.SetDeveloperSessionRuntime(r.Context(), session.ID, service.DeveloperSessionCompleted, "")
			httpResponseJSON(w, map[string]any{"session": updated, "content": resp.Content}, http.StatusOK)
			return
		}
		waitingKind := ""
		for _, call := range resp.ToolCalls {
			if call.Name == "ask_user" {
				waitingKind = "question"
				break
			}
			if developerToolEffect(profile, call) == "ask" {
				waitingKind = "permission"
			}
		}
		if waitingKind != "" {
			pending, err := store.SaveDeveloperPendingTool(r.Context(), service.DeveloperPendingTool{
				SessionID: session.ID, Kind: waitingKind, ToolCalls: resp.ToolCalls, TraceID: traceID,
				ParentObservationID: generationID, Step: step,
			})
			if err != nil {
				_, _ = store.SetDeveloperSessionRuntime(r.Context(), session.ID, service.DeveloperSessionFailed, err.Error())
				developerSpaceError(w, err)
				return
			}
			status := service.DeveloperSessionWaitingPermission
			if waitingKind == "question" {
				status = service.DeveloperSessionWaitingQuestion
			}
			updated, _ := store.SetDeveloperSessionRuntime(r.Context(), session.ID, status, "")
			httpResponseJSON(w, map[string]any{"session": updated, "pending_tool": pending}, http.StatusAccepted)
			return
		}
		blocks := make([]service.ContentBlock, 0, len(resp.ToolCalls))
		for _, call := range resp.ToolCalls {
			effect := developerToolEffect(profile, call)
			output := "tool denied by the active agent profile"
			var toolErr error
			started := time.Now()
			if effect == "allow" {
				output, toolErr = s.executeDeveloperTool(r, worktree, cfg, scope, call, profile.ToolTimeoutSeconds)
				if toolErr != nil {
					output = "tool error: " + toolErr.Error()
				}
			}
			output, block := agentloop.ToolResult(s.loopGov, session.ID, call, output)
			blocks = append(blocks, block)
			s.recordDeveloperToolObservation(r, session, traceID, generationID, call, output, time.Since(started).Milliseconds(), toolErr)
		}
		if _, err := store.AppendDeveloperSessionMessage(r.Context(), service.DeveloperSessionMessage{SessionID: session.ID, Role: "tool", Content: blocks}); err != nil {
			_, _ = store.SetDeveloperSessionRuntime(r.Context(), session.ID, service.DeveloperSessionFailed, err.Error())
			developerSpaceError(w, err)
			return
		}
		s.captureDeveloperSnapshot(r, session, worktree, cfg, scope, step, "after")
	}
	_, _ = store.SetDeveloperSessionRuntime(r.Context(), session.ID, service.DeveloperSessionFailed, "iteration limit reached")
	httpResponse(w, "coding session iteration limit reached", http.StatusConflict)
}

func (s *Server) captureDeveloperSnapshot(r *http.Request, session *service.DeveloperSession, worktree *service.DeveloperWorktree, cfg container.Config, scope string, step int, phase string) {
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
	workDir := developerWorktreePath(worktree.ID)
	head, _, _, _ := s.containerManager.ExecArgs(r.Context(), scope, cfg, workDir, nil, "git", "rev-parse", "HEAD")
	diff, _, code, err := s.containerManager.ExecArgs(r.Context(), scope, cfg, workDir, nil, "git", "diff", "--binary", "--no-ext-diff", "HEAD", "--")
	if err != nil || code != 0 {
		return
	}
	untracked, _, _, _ := s.containerManager.ExecArgs(r.Context(), scope, cfg, workDir, nil, "git", "ls-files", "-z", "--others", "--exclude-standard")
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
			patch, _, patchCode, patchErr := s.containerManager.ExecArgs(r.Context(), scope, cfg, workDir, nil, "git", "diff", "--binary", "--no-index", "--", "/dev/null", name)
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
	developerStore, ok := s.store.(service.DeveloperSpaceStorer)
	if !ok {
		return
	}
	_, _ = developerStore.SaveDeveloperSessionSnapshot(r.Context(), service.DeveloperSessionSnapshot{
		SessionID: session.ID, Step: step, Phase: phase, HeadSHA: strings.TrimSpace(head), StorageObjectID: object.ID,
	})
}

func (s *Server) ConfirmDeveloperSessionToolAPI(w http.ResponseWriter, r *http.Request) {
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
		Approved bool `json:"approved"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, "invalid request body", http.StatusBadRequest)
		return
	}
	worktree, err := store.GetDeveloperWorktree(r.Context(), session.WorktreeID)
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	_, space, cfg, scope, ok := s.developerRuntime(w, r, session.SpaceID)
	if !ok {
		return
	}
	pending, err := store.ClaimDeveloperPendingTool(r.Context(), session.ID)
	if err != nil || pending == nil {
		httpResponse(w, "no tool is waiting for approval", http.StatusConflict)
		return
	}
	if pending.Kind != "permission" {
		_, _ = store.SaveDeveloperPendingTool(r.Context(), *pending)
		httpResponse(w, "coding session is waiting for an answer, not permission", http.StatusConflict)
		return
	}
	r, done := s.trackDeveloperSessionRequest(r, session.ID)
	defer done()
	profile := developerProfile(space, session.Mode)
	blocks := make([]service.ContentBlock, 0, len(pending.ToolCalls))
	for _, call := range pending.ToolCalls {
		effect := developerToolEffect(profile, call)
		output := "tool denied by the active agent profile"
		var toolErr error
		started := time.Now()
		if effect == "ask" && !req.Approved {
			output = "tool execution rejected by the user"
		} else if effect == "allow" || (effect == "ask" && req.Approved) {
			output, toolErr = s.executeDeveloperTool(r, worktree, cfg, scope, call, profile.ToolTimeoutSeconds)
			if toolErr != nil {
				output = "tool error: " + toolErr.Error()
			}
		}
		_, block := agentloop.ToolResult(s.loopGov, session.ID, call, output)
		blocks = append(blocks, block)
		s.recordDeveloperToolObservation(r, session, pending.TraceID, pending.ParentObservationID, call, output, time.Since(started).Milliseconds(), toolErr)
	}
	if _, err := store.ResolveDeveloperPendingTool(r.Context(), session.ID, blocks); err != nil {
		developerSpaceError(w, err)
		return
	}
	s.captureDeveloperSnapshot(r, session, worktree, cfg, scope, pending.Step, "after")
	s.runDeveloperSession(w, r, store, session, pending.TraceID)
}

func (s *Server) AnswerDeveloperSessionQuestionAPI(w http.ResponseWriter, r *http.Request) {
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
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Answer) == "" {
		httpResponse(w, "answer is required", http.StatusBadRequest)
		return
	}
	pending, err := store.ClaimDeveloperPendingTool(r.Context(), session.ID)
	if err != nil || pending == nil {
		httpResponse(w, "no question is waiting for an answer", http.StatusConflict)
		return
	}
	if pending.Kind != "question" {
		_, _ = store.SaveDeveloperPendingTool(r.Context(), *pending)
		httpResponse(w, "coding session is waiting for permission, not an answer", http.StatusConflict)
		return
	}
	r, done := s.trackDeveloperSessionRequest(r, session.ID)
	defer done()
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
	if _, err := store.ResolveDeveloperPendingTool(r.Context(), session.ID, blocks); err != nil {
		developerSpaceError(w, err)
		return
	}
	s.runDeveloperSession(w, r, store, session, pending.TraceID)
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
		metadata: map[string]any{"mode": session.Mode, "worktree_id": session.WorktreeID},
	}
	if callErr != nil {
		params.status = "error"
		params.level = service.ObservationLevelError
		params.errMsg = callErr.Error()
	}
	s.recordLLMCallAsync(r.Context(), params)
}

func (s *Server) executeDeveloperTool(r *http.Request, worktree *service.DeveloperWorktree, cfg container.Config, scope string, call service.ToolCall, timeoutSeconds int) (string, error) {
	workDir := developerWorktreePath(worktree.ID)
	var command string
	var args []string
	switch call.Name {
	case "read_file":
		candidate, err := developerWorktreeFilePath(stringArg(call.Arguments, "path"))
		if err != nil {
			return "", err
		}
		script := "import pathlib,sys;root=pathlib.Path.cwd().resolve();p=(root/sys.argv[1]).resolve();rel=p.relative_to(root);\nif rel.parts and rel.parts[0]=='.git': raise ValueError('Git metadata is not available to file tools')\ndata=p.read_bytes();\nif len(data)>1048576: raise ValueError('file exceeds 1 MiB read limit')\nsys.stdout.write(data.decode('utf-8'))"
		command, args = "python3", []string{"-c", script, candidate}
	case "list_files":
		script := "import os,pathlib;root=pathlib.Path.cwd().resolve();count=0\nfor base,dirs,files in os.walk(root,followlinks=False):\n dirs[:]=sorted(d for d in dirs if d!='.git' and not (pathlib.Path(base)/d).is_symlink())\n for name in sorted(files):\n  p=pathlib.Path(base)/name\n  if p.is_symlink(): continue\n  print(p.relative_to(root));count+=1\n  if count>=10000: raise SystemExit(0)"
		command, args = "python3", []string{"-c", script}
	case "search":
		pattern := stringArg(call.Arguments, "pattern")
		if pattern == "" {
			return "", fmt.Errorf("pattern is required")
		}
		candidate := stringArg(call.Arguments, "path")
		if candidate == "" {
			candidate = "."
		} else {
			clean, err := developerWorktreeFilePath(candidate)
			if err != nil {
				return "", err
			}
			candidate = clean
		}
		script := "import os,pathlib,re,sys;root=pathlib.Path.cwd().resolve();start=(root/sys.argv[2]).resolve();rel=start.relative_to(root);\nif rel.parts and rel.parts[0]=='.git': raise ValueError('Git metadata is not available to file tools')\nrx=re.compile(sys.argv[1]);paths=[start] if start.is_file() else (pathlib.Path(b)/n for b,ds,fs in os.walk(start,followlinks=False) for n in fs);found=0\nfor p in paths:\n rel=p.relative_to(root)\n if '.git' in rel.parts or p.is_symlink() or not p.is_file(): continue\n try:\n  if p.stat().st_size>4194304: continue\n  for no,line in enumerate(p.read_text(encoding='utf-8').splitlines(),1):\n   if rx.search(line): print(f'{rel}:{no}:{line}');found+=1\n   if found>=10000: raise SystemExit(0)\n except (UnicodeDecodeError,OSError): pass"
		command, args = "python3", []string{"-c", script, pattern, candidate}
	case "git_status":
		command, args = "git", []string{"status", "--porcelain=v2", "--branch"}
	case "git_diff":
		args = []string{"diff", "--no-ext-diff"}
		if boolArg(call.Arguments, "staged") {
			args = append(args, "--cached")
		}
		command, args = "git", append(args, "--")
	case "write_file":
		candidate, err := developerWorktreeFilePath(stringArg(call.Arguments, "path"))
		if err != nil {
			return "", err
		}
		content := stringArg(call.Arguments, "content")
		if len(content) > 1<<20 {
			return "", fmt.Errorf("content exceeds 1 MiB")
		}
		script := "import base64,pathlib,sys;root=pathlib.Path.cwd().resolve();p=(root/sys.argv[1]).resolve(strict=False);rel=p.relative_to(root);\nif rel.parts and rel.parts[0]=='.git': raise ValueError('Git metadata is not available to file tools')\np.parent.mkdir(parents=True,exist_ok=True);p.write_bytes(base64.b64decode(sys.argv[2]))"
		command, args = "python3", []string{"-c", script, candidate, base64.StdEncoding.EncodeToString([]byte(content))}
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
	if timeoutSeconds <= 0 {
		timeoutSeconds = 60
	}
	toolCtx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	timeoutArgs := []string{"--signal=KILL", "--kill-after=1s", fmt.Sprintf("%ds", timeoutSeconds), command}
	timeoutArgs = append(timeoutArgs, args...)
	stdout, stderr, code, err := s.containerManager.ExecArgs(toolCtx, scope, cfg, workDir, nil, "timeout", timeoutArgs...)
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
