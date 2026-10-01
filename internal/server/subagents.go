package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

const maxSubagentDepth = service.MaxSubagentDepth

type subagentRuntimeContext struct {
	Depth         int
	TraceID       string
	ParentTraceID string
	Session       service.ChatSession
	SkillID       string
}

type subagentRuntimeContextKey struct{}

type backgroundSubagentOwner struct {
	RunID string
	Ctx   context.Context
}

type backgroundSubagentOwnerContextKey struct{}

type backgroundSubagentRun struct {
	mu sync.Mutex

	ID          string
	Status      string
	AgentID     string
	AgentName   string
	TraceID     string
	ParentTrace string
	ParentRunID string
	WorkspaceID string
	UserID      string
	Result      string
	Error       string
	StartedAt   time.Time
	CompletedAt time.Time
	Cancel      context.CancelFunc
	// Artifacts are files the run produced, delivered to media storage when it
	// was started from Chats.
	Artifacts     []chatArtifact
	ArtifactsNote string
	done          chan struct{}
}

// subagentProgressFunc receives a one-line summary of what a running subagent
// is doing, so the owning chat stream can show progress instead of a silent
// tool call that may run for minutes.
type subagentProgressFunc func(agentName, summary string)

type subagentProgressContextKey struct{}

func contextWithSubagentProgress(ctx context.Context, fn subagentProgressFunc) context.Context {
	return context.WithValue(ctx, subagentProgressContextKey{}, fn)
}

func subagentProgressFromContext(ctx context.Context) subagentProgressFunc {
	fn, _ := ctx.Value(subagentProgressContextKey{}).(subagentProgressFunc)
	return fn
}

// turnBackgroundRuns records the background subagents one agentic-loop turn
// started. The turn waits for them before its final answer, because a result
// that arrives after the turn ended has nowhere to go.
type turnBackgroundRuns struct {
	mu       sync.Mutex
	runs     []*backgroundSubagentRun
	reported map[string]bool
}

type turnBackgroundRunsContextKey struct{}

func contextWithTurnBackgroundRuns(ctx context.Context, runs *turnBackgroundRuns) context.Context {
	return context.WithValue(ctx, turnBackgroundRunsContextKey{}, runs)
}

func turnBackgroundRunsFromContext(ctx context.Context) *turnBackgroundRuns {
	runs, _ := ctx.Value(turnBackgroundRunsContextKey{}).(*turnBackgroundRuns)
	return runs
}

func (t *turnBackgroundRuns) add(run *backgroundSubagentRun) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.runs = append(t.runs, run)
}

// markReported excludes a run whose terminal result the model already read
// through agent_run_status.
func (t *turnBackgroundRuns) markReported(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.reported == nil {
		t.reported = map[string]bool{}
	}
	t.reported[id] = true
}

// unreported returns runs whose results have not reached the model yet.
func (t *turnBackgroundRuns) unreported() []*backgroundSubagentRun {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var pending []*backgroundSubagentRun
	for _, run := range t.runs {
		if !t.reported[run.ID] {
			pending = append(pending, run)
		}
	}
	return pending
}

// waitAndReport blocks until every unreported run is terminal (or ctx ends),
// marks them reported and renders their results for the model.
func (t *turnBackgroundRuns) waitAndReport(ctx context.Context) (string, error) {
	pending := t.unreported()
	for _, run := range pending {
		select {
		case <-run.done:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	var b strings.Builder
	b.WriteString("Background subagent runs you started have finished. Their results:\n")
	for _, run := range pending {
		t.markReported(run.ID)
		run.mu.Lock()
		fmt.Fprintf(&b, "\n## %s (run %s) — %s\n", run.AgentName, run.ID, run.Status)
		switch {
		case run.Error != "":
			b.WriteString("Error: " + run.Error + "\n")
		case run.Result != "":
			b.WriteString(run.Result + "\n")
		default:
			b.WriteString("(no result)\n")
		}
		run.mu.Unlock()
	}
	return b.String(), nil
}

func summarizeSubagentEvent(event AgenticEvent) string {
	switch event.Type {
	case "tool_call":
		return "running " + event.ToolName
	case "tool_result":
		return event.ToolName + " finished"
	case "content":
		if event.Final {
			return "finished"
		}
		text := strings.Join(strings.Fields(event.Content), " ")
		if len(text) > 200 {
			text = text[:200] + "…"
		}
		return text
	case "error":
		return "error: " + event.Error
	}
	return ""
}

func contextWithBackgroundSubagentOwner(ctx context.Context, runID string, owner context.Context) context.Context {
	return context.WithValue(ctx, backgroundSubagentOwnerContextKey{}, backgroundSubagentOwner{RunID: runID, Ctx: owner})
}

func backgroundSubagentOwnerFromContext(ctx context.Context) (backgroundSubagentOwner, bool) {
	owner, ok := ctx.Value(backgroundSubagentOwnerContextKey{}).(backgroundSubagentOwner)
	return owner, ok && owner.RunID != "" && owner.Ctx != nil
}

func contextWithSubagentRuntime(ctx context.Context, runtime subagentRuntimeContext) context.Context {
	return context.WithValue(ctx, subagentRuntimeContextKey{}, runtime)
}

func subagentRuntimeFromContext(ctx context.Context) (subagentRuntimeContext, bool) {
	runtime, ok := ctx.Value(subagentRuntimeContextKey{}).(subagentRuntimeContext)
	return runtime, ok
}

func subagentDepthFromContext(ctx context.Context) int {
	runtime, _ := subagentRuntimeFromContext(ctx)
	return runtime.Depth
}

type chatTraceIDContextKey struct{}

type subagentSkillContextKey struct{}

func contextWithSubagentSkill(ctx context.Context, skillID string) context.Context {
	return context.WithValue(ctx, subagentSkillContextKey{}, skillID)
}

func subagentSkillFromContext(ctx context.Context) string {
	skillID, _ := ctx.Value(subagentSkillContextKey{}).(string)
	return skillID
}

func contextWithChatTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, chatTraceIDContextKey{}, traceID)
}

func chatTraceIDFromContext(ctx context.Context) string {
	traceID, _ := ctx.Value(chatTraceIDContextKey{}).(string)
	return traceID
}

func (s *Server) resolveSubagent(ctx context.Context, ref string) (*service.Agent, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("agent is required")
	}
	if agent, err := s.agentStore.GetAgent(ctx, ref); err != nil {
		return nil, err
	} else if agent != nil {
		return agent, nil
	}
	list, err := s.agentStore.ListAgents(ctx, nil)
	if err != nil {
		return nil, err
	}
	if list == nil {
		return nil, fmt.Errorf("agent %q not found", ref)
	}
	var matched *service.Agent
	for i := range list.Data {
		if strings.EqualFold(list.Data[i].Name, ref) {
			if matched != nil {
				return nil, fmt.Errorf("agent name %q is ambiguous; use its ID", ref)
			}
			candidate := list.Data[i]
			matched = &candidate
		}
	}
	if matched == nil {
		return nil, fmt.Errorf("agent %q not found", ref)
	}
	return matched, nil
}

// execAgentRun is the ephemeral counterpart to organization delegation. It
// creates no Task or OrganizationDelegation row: the child receives a fresh
// context and returns one final result to its parent tool call.
func (s *Server) execAgentRun(ctx context.Context, args map[string]any) (string, error) {
	if s.agentStore == nil {
		return "", fmt.Errorf("agent runtime is not configured")
	}
	depth := subagentDepthFromContext(ctx)
	if depth >= maxSubagentDepth {
		return "", fmt.Errorf("subagent depth limit reached (%d)", maxSubagentDepth)
	}
	parentID := agentIDFromContext(ctx)
	if parentID == "" {
		return "", fmt.Errorf("agent_run requires a bound parent agent")
	}
	parent, err := s.agentStore.GetAgent(ctx, parentID)
	if err != nil {
		return "", fmt.Errorf("load parent agent: %w", err)
	}
	if parent == nil {
		return "", fmt.Errorf("parent agent %q not found", parentID)
	}
	child, err := s.resolveSubagent(ctx, stringArg(args, "agent"))
	if err != nil {
		return "", err
	}
	if child.ID == parent.ID {
		return "", fmt.Errorf("an agent cannot launch itself as a subagent")
	}
	if !service.AgentAllowsSubagent(parent, child) {
		return "", fmt.Errorf("agent %q is not in %q's subagent allowlist", child.Name, parent.Name)
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "agents.run", ResourceID: child.ID}); err != nil {
		return "", err
	}
	if len(child.Config.ConfirmationRequiredTools) > 0 {
		return "", fmt.Errorf("subagent %q requires interactive tool confirmation; ephemeral subagents cannot relay confirmations", child.Name)
	}
	task := strings.TrimSpace(stringArg(args, "task"))
	if task == "" {
		return "", fmt.Errorf("task is required")
	}
	if extra := strings.TrimSpace(stringArg(args, "context")); extra != "" {
		task += "\n\nContext and constraints:\n" + extra
	}
	if background, _ := args["background"].(bool); background {
		return s.startBackgroundSubagent(ctx, child, task, depth)
	}
	return s.runSubagentForeground(ctx, child, task, depth, "")
}

func (s *Server) workflowAgentRunner(ctx context.Context, parentAgentID, name string, args map[string]any) (string, error) {
	ctx = contextWithAgentID(ctx, parentAgentID)
	if skillID := strings.TrimSpace(stringArg(args, "_skill_id")); skillID != "" {
		ctx = contextWithSubagentSkill(ctx, skillID)
	}
	if traceID := strings.TrimSpace(stringArg(args, "_parent_trace_id")); traceID != "" {
		ctx = contextWithChatTraceID(ctx, traceID)
	}
	return s.dispatchBuiltinTool(ctx, name, args)
}

func (s *Server) runSubagentForeground(ctx context.Context, child *service.Agent, task string, depth int, traceID string) (string, error) {
	provenance, _, _ := service.ExecutionFromContext(ctx)
	childTraceID := traceID
	if childTraceID == "" {
		childTraceID = ulid.Make().String()
	}
	runtime := subagentRuntimeContext{
		Depth:         depth + 1,
		TraceID:       childTraceID,
		ParentTraceID: chatTraceIDFromContext(ctx),
		SkillID:       subagentSkillFromContext(ctx),
		Session: service.ChatSession{
			ID:          "subagent_" + ulid.Make().String(),
			WorkspaceID: provenance.WorkspaceID,
			OwnerUserID: provenance.UserID,
			AgentID:     child.ID,
			Name:        "Subagent: " + child.Name,
		},
	}
	childCtx := contextWithSubagentRuntime(ctx, runtime)
	childCtx = contextWithChatTraceID(childCtx, childTraceID)

	var final, runError string
	progress := subagentProgressFromContext(ctx)
	err := s.RunAgenticLoop(childCtx, runtime.Session.ID, task, func(event AgenticEvent) {
		if event.Type == "content" && event.Final {
			final = event.Content
		}
		if event.Type == "error" {
			runError = event.Error
		}
		if progress != nil {
			if summary := summarizeSubagentEvent(event); summary != "" {
				progress(child.Name, summary)
			}
		}
	})
	if err != nil {
		return "", fmt.Errorf("run subagent %q: %w", child.Name, err)
	}
	if runError != "" {
		return "", fmt.Errorf("subagent %q failed: %s", child.Name, runError)
	}
	if strings.TrimSpace(final) == "" {
		return "", fmt.Errorf("subagent %q returned no final result", child.Name)
	}
	payload, err := json.Marshal(map[string]any{
		"agent_id":        child.ID,
		"agent_name":      child.Name,
		"result":          final,
		"trace_id":        childTraceID,
		"parent_trace_id": runtime.ParentTraceID,
	})
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func (s *Server) startBackgroundSubagent(ctx context.Context, child *service.Agent, task string, depth int) (string, error) {
	provenance, _, ok := service.ExecutionFromContext(ctx)
	if !ok {
		return "", service.ErrExecutionDenied
	}
	maxBackground := service.DefaultMaxBackgroundSubagentsPerOwner
	if s.agentRuntimeSettingsStore != nil {
		settings, err := s.agentRuntimeSettingsStore.GetAgentRuntimeSettings(ctx)
		if err != nil {
			return "", fmt.Errorf("load agent runtime settings: %w", err)
		}
		maxBackground = settings.MaxBackgroundSubagentsPerOwner
	}
	run := &backgroundSubagentRun{
		ID: "subrun_" + ulid.Make().String(), Status: "queued",
		AgentID: child.ID, AgentName: child.Name, TraceID: ulid.Make().String(),
		ParentTrace: chatTraceIDFromContext(ctx), WorkspaceID: provenance.WorkspaceID,
		UserID: provenance.UserID, done: make(chan struct{}),
	}
	// The owning turn's stream and wait list must not leak into a run that can
	// outlive it; the turn subscribes explicitly through turnBackgroundRuns.
	parent := contextWithSubagentProgress(context.WithoutCancel(ctx), nil)
	parent = contextWithTurnBackgroundRuns(parent, nil)
	collectArtifacts := backgroundArtifactsFromContext(ctx)
	parent = contextWithBackgroundArtifacts(parent, nil)
	runCtx, cancel := context.WithCancel(parent)
	run.Cancel = cancel
	var stopOwner func() bool
	if owner, ok := backgroundSubagentOwnerFromContext(ctx); ok {
		run.ParentRunID = owner.RunID
		stopOwner = context.AfterFunc(owner.Ctx, cancel)
	}
	runCtx = contextWithBackgroundSubagentOwner(runCtx, run.ID, runCtx)
	var stopServer func() bool
	if s.ctx != nil {
		stopServer = context.AfterFunc(s.ctx, cancel)
	}
	s.subagentMu.Lock()
	active := 0
	s.activeSubagents.Range(func(_, value any) bool {
		other, ok := value.(*backgroundSubagentRun)
		if !ok || other.WorkspaceID != run.WorkspaceID || other.UserID != run.UserID {
			return true
		}
		other.mu.Lock()
		terminal := other.Status == "completed" || other.Status == "failed" || other.Status == "cancelled"
		other.mu.Unlock()
		if !terminal {
			active++
		}
		return active < maxBackground
	})
	if active >= maxBackground {
		s.subagentMu.Unlock()
		cancel()
		if stopServer != nil {
			stopServer()
		}
		if stopOwner != nil {
			stopOwner()
		}
		return "", fmt.Errorf("background subagent limit reached (%d active runs)", maxBackground)
	}
	s.activeSubagents.Store(run.ID, run)
	s.subagentMu.Unlock()

	go func() {
		defer cancel()
		if stopServer != nil {
			defer stopServer()
		}
		if stopOwner != nil {
			defer stopOwner()
		}
		run.mu.Lock()
		if run.Status != "cancelling" {
			run.Status = "running"
		}
		run.StartedAt = time.Now().UTC()
		run.mu.Unlock()

		payload, err := s.runSubagentForeground(runCtx, child, task, depth, run.TraceID)
		var collected chatArtifactCollection
		if collectArtifacts != nil {
			// Cancelled runs may have written files too; storage must not be
			// skipped because the run's own context is already done.
			collected = collectArtifacts(context.WithoutCancel(runCtx))
		}
		run.mu.Lock()
		run.Artifacts, run.ArtifactsNote = collected.Artifacts, collected.Note
		switch {
		case errors.Is(runCtx.Err(), context.Canceled):
			run.Status = "cancelled"
		case err != nil:
			run.Status = "failed"
			run.Error = err.Error()
		default:
			run.Status = "completed"
			var result struct {
				Result string `json:"result"`
			}
			if json.Unmarshal([]byte(payload), &result) == nil {
				run.Result = result.Result
			} else {
				run.Result = payload
			}
		}
		run.CompletedAt = time.Now().UTC()
		run.mu.Unlock()
		close(run.done)
		time.AfterFunc(time.Hour, func() { s.activeSubagents.Delete(run.ID) })
	}()

	if turn := turnBackgroundRunsFromContext(ctx); turn != nil {
		turn.add(run)
	}
	return marshalBackgroundSubagent(run)
}

func (s *Server) backgroundSubagentForContext(ctx context.Context, id string) (*backgroundSubagentRun, error) {
	value, ok := s.activeSubagents.Load(strings.TrimSpace(id))
	if !ok {
		return nil, fmt.Errorf("background subagent run %q not found", id)
	}
	run, ok := value.(*backgroundSubagentRun)
	if !ok {
		return nil, fmt.Errorf("invalid background subagent run")
	}
	provenance, _, bound := service.ExecutionFromContext(ctx)
	if !bound || provenance.WorkspaceID != run.WorkspaceID || provenance.UserID != run.UserID {
		return nil, service.ErrExecutionDenied
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "agents.run", ResourceID: run.AgentID}); err != nil {
		return nil, err
	}
	return run, nil
}

func (s *Server) execAgentRunStatus(ctx context.Context, args map[string]any) (string, error) {
	run, err := s.backgroundSubagentForContext(ctx, stringArg(args, "run_id"))
	if err != nil {
		return "", err
	}
	if turn := turnBackgroundRunsFromContext(ctx); turn != nil {
		select {
		case <-run.done:
			turn.markReported(run.ID)
		default:
		}
	}
	return marshalBackgroundSubagent(run)
}

func (s *Server) execAgentRunCancel(ctx context.Context, args map[string]any) (string, error) {
	run, err := s.backgroundSubagentForContext(ctx, stringArg(args, "run_id"))
	if err != nil {
		return "", err
	}
	run.mu.Lock()
	if run.Status == "queued" || run.Status == "running" {
		run.Status = "cancelling"
		run.Cancel()
	}
	run.mu.Unlock()
	return marshalBackgroundSubagent(run)
}

func marshalBackgroundSubagent(run *backgroundSubagentRun) (string, error) {
	run.mu.Lock()
	defer run.mu.Unlock()
	payload := map[string]any{
		"run_id": run.ID, "status": run.Status, "agent_id": run.AgentID,
		"agent_name": run.AgentName, "trace_id": run.TraceID,
		"parent_trace_id": run.ParentTrace,
	}
	if run.ParentRunID != "" {
		payload["parent_run_id"] = run.ParentRunID
	}
	if run.Result != "" {
		payload["result"] = run.Result
	}
	if run.Error != "" {
		payload["error"] = run.Error
	}
	if len(run.Artifacts) > 0 {
		payload["artifacts"] = run.Artifacts
	}
	if run.ArtifactsNote != "" {
		payload["artifacts_note"] = run.ArtifactsNote
	}
	if !run.StartedAt.IsZero() {
		payload["started_at"] = run.StartedAt.Format(time.RFC3339)
	}
	if !run.CompletedAt.IsZero() {
		payload["completed_at"] = run.CompletedAt.Format(time.RFC3339)
	}
	data, err := json.Marshal(payload)
	return string(data), err
}

func subagentObservationMetadata(ctx context.Context) map[string]any {
	runtime, ok := subagentRuntimeFromContext(ctx)
	if !ok {
		return nil
	}
	return map[string]any{
		"subagent":        true,
		"subagent_depth":  runtime.Depth,
		"parent_trace_id": runtime.ParentTraceID,
	}
}

func agentRunToolMetadata(name, output string) map[string]any {
	if (name != "agent_run" && name != workflow.LoadSkillToolName) || output == "" {
		return nil
	}
	var result struct {
		AgentID   string `json:"agent_id"`
		TraceID   string `json:"trace_id"`
		AgentName string `json:"agent_name"`
	}
	if json.Unmarshal([]byte(output), &result) != nil || result.TraceID == "" {
		return nil
	}
	return map[string]any{
		"child_trace_id":   result.TraceID,
		"child_agent_id":   result.AgentID,
		"child_agent_name": result.AgentName,
	}
}
