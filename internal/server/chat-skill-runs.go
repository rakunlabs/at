package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// Chats (/chats) runs its agentic loop in the browser, so a documentation skill
// bound to an agent (context: fork) had no way to execute there: the skill was
// only pasted into the system prompt, and the model could neither start the
// agent nor wait for it. These endpoints are that missing execution path. The
// browser offers them through the same `load_skill` / `agent_run_status` tools
// the Sessions loop uses; the server runs the skill's agent through the same
// isolated runner.

type chatSkillRunRequest struct {
	Skill      string `json:"skill"`
	Task       string `json:"task"`
	Context    string `json:"context,omitempty"`
	Background bool   `json:"background,omitempty"`
	TraceID    string `json:"trace_id,omitempty"`
}

// chatSkillRunMaxWait bounds one status long-poll. The browser polls again
// until the run is terminal, so a long subagent never holds one request open
// past typical proxy idle limits.
const chatSkillRunMaxWait = 25 * time.Second

func (s *Server) chatForkSkill(ctx context.Context, ref string) (*service.Skill, error) {
	if s.skillStore == nil {
		return nil, fmt.Errorf("skills are not configured")
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("skill is required")
	}
	skill, err := s.getSkillByIDOrName(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("load skill: %w", err)
	}
	if skill == nil {
		return nil, fmt.Errorf("skill %q not found", ref)
	}
	if skill.Context != "fork" || strings.TrimSpace(skill.Agent) == "" {
		return nil, fmt.Errorf("skill %q is not bound to an agent; its instructions are already in the system prompt", skill.Name)
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "skills.use", ResourceID: skill.ID}); err != nil {
		return nil, err
	}
	return skill, nil
}

// ChatSkillRunAPI handles POST /api/v1/chats/skill-runs.
// Foreground runs answer with the subagent's result; background runs answer
// immediately with a run ID for ChatSkillRunStatusAPI.
func (s *Server) ChatSkillRunAPI(w http.ResponseWriter, r *http.Request) {
	var req chatSkillRunRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Task) == "" {
		httpResponse(w, "task is required", http.StatusBadRequest)
		return
	}
	ctx, err := s.bindRuntimePrincipal(r.Context(), "chat")
	if err != nil {
		httpResponse(w, "runtime identity unavailable", http.StatusForbidden)
		return
	}
	skill, err := s.chatForkSkill(ctx, req.Skill)
	if err != nil {
		httpResponseJSON(w, builtinCallResponse{Error: err.Error()}, http.StatusOK)
		return
	}
	child, err := s.resolveSubagent(ctx, skill.Agent)
	if err != nil {
		httpResponseJSON(w, builtinCallResponse{Error: err.Error()}, http.StatusOK)
		return
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "agents.run", ResourceID: child.ID}); err != nil {
		httpResponseJSON(w, builtinCallResponse{Error: err.Error()}, http.StatusOK)
		return
	}
	if len(child.Config.ConfirmationRequiredTools) > 0 {
		httpResponseJSON(w, builtinCallResponse{Error: fmt.Sprintf("agent %q requires interactive tool confirmation, which Chats cannot relay", child.Name)}, http.StatusOK)
		return
	}
	task := strings.TrimSpace(req.Task)
	if extra := strings.TrimSpace(req.Context); extra != "" {
		task += "\n\nContext and constraints:\n" + extra
	}
	ctx = contextWithSubagentSkill(ctx, skill.ID)
	if req.TraceID != "" {
		ctx = contextWithChatTraceID(ctx, req.TraceID)
	}
	workDir, workRel, err := chatRunWorkDir(ctx)
	if err != nil {
		httpResponseJSON(w, builtinCallResponse{Error: fmt.Sprintf("prepare run directory: %v", err)}, http.StatusOK)
		return
	}
	ctx, task = withChatRunWorkDir(ctx, task, workDir, workRel)
	collect := func(ctx context.Context) chatArtifactCollection { return s.collectChatArtifacts(ctx, workDir) }

	var result string
	if req.Background {
		ctx = contextWithBackgroundArtifacts(ctx, collect)
		result, err = s.startBackgroundSubagent(ctx, child, task, 0)
	} else {
		result, err = s.runSubagentForeground(ctx, child, task, 0, "")
		// A failed run may still have produced files; deliver them either way.
		result = withArtifacts(result, collect(ctx))
	}
	resp := builtinCallResponse{Result: result}
	if err != nil {
		resp.Error = err.Error()
	}
	httpResponseJSON(w, resp, http.StatusOK)
}

// withArtifacts adds the collected files to a JSON object result. A result
// that is not an object (or empty, after a failure) becomes one.
func withArtifacts(result string, collected chatArtifactCollection) string {
	if len(collected.Artifacts) == 0 && collected.Note == "" {
		return result
	}
	payload := map[string]any{}
	if strings.TrimSpace(result) != "" && json.Unmarshal([]byte(result), &payload) != nil {
		payload = map[string]any{"result": result}
	}
	if len(collected.Artifacts) > 0 {
		payload["artifacts"] = collected.Artifacts
	}
	if collected.Note != "" {
		payload["artifacts_note"] = collected.Note
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return result
	}
	return string(data)
}

type backgroundArtifactsContextKey struct{}

type backgroundArtifactCollector func(context.Context) chatArtifactCollection

func contextWithBackgroundArtifacts(ctx context.Context, collect backgroundArtifactCollector) context.Context {
	return context.WithValue(ctx, backgroundArtifactsContextKey{}, collect)
}

func backgroundArtifactsFromContext(ctx context.Context) backgroundArtifactCollector {
	collect, _ := ctx.Value(backgroundArtifactsContextKey{}).(backgroundArtifactCollector)
	return collect
}

// ChatSkillRunStatusAPI handles GET /api/v1/chats/skill-runs/{id}?wait=<seconds>.
// With wait it long-polls until the run is terminal or the wait elapses.
func (s *Server) ChatSkillRunStatusAPI(w http.ResponseWriter, r *http.Request) {
	ctx, err := s.bindRuntimePrincipal(r.Context(), "chat")
	if err != nil {
		httpResponse(w, "runtime identity unavailable", http.StatusForbidden)
		return
	}
	run, err := s.backgroundSubagentForContext(ctx, r.PathValue("id"))
	if err != nil {
		httpResponse(w, err.Error(), http.StatusNotFound)
		return
	}
	if seconds, _ := time.ParseDuration(r.URL.Query().Get("wait") + "s"); seconds > 0 {
		if seconds > chatSkillRunMaxWait {
			seconds = chatSkillRunMaxWait
		}
		timer := time.NewTimer(seconds)
		select {
		case <-run.done:
		case <-timer.C:
		case <-r.Context().Done():
		}
		timer.Stop()
	}
	payload, err := marshalBackgroundSubagent(run)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(payload))
}
