package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/agentloop"
	"github.com/rakunlabs/at/internal/service/workflow"
)

const (
	consultAgentTool       = "consult_agent"
	maxOrgConsultations    = 8
	consultationTimeout    = 90 * time.Second
	consultationInputBytes = 16384
)

type consultationBudgetKey struct{}

const orgCollaborationPrompt = `

## Collaboration
Use consult_agent for a focused question, second opinion or review of supplied text; it creates no task and cannot execute tools. Active peers, managers and reports may advise each other.
Use delegate_to_* for a concrete, independently trackable deliverable requiring execution. Follow any required specialist stages, stage order, artifact checks and failure contracts in your role instructions. Never replace a required production stage with consultation or do a specialist's work yourself when your role forbids it. Consultation cannot research, read/write files, generate media or render artifacts, and is never evidence that such work was performed.
Otherwise, do simple work yourself; neither consultation nor delegation is mandatory. You retain ownership: evaluate advice, reconcile disagreements and deliver the final result. A failed consultation is not completed work; handle it according to your role's failure contract.
`

// Successful results may be a machine-readable JSON object or an exact artifact
// path. Preserve them verbatim. Failed stages retain leading control markers
// such as [BLOCKED] and [ITERATION_LIMIT], which production prompts depend on.
func delegationTaskResult(task *service.Task) string {
	if task.Status == service.TaskStatusDone {
		return task.Result
	}
	status := fmt.Sprintf("Task %s status: %s (not a successful delivery).", task.ID, task.Status)
	if strings.HasPrefix(task.Result, "[") {
		return task.Result + "\n\n" + status
	}
	return "[BLOCKED] " + status + "\n" + task.Result
}

// Shared by the whole delegation tree, including concurrent children. A
// consultant gets no tools, so it cannot consult back or create another task.
func withConsultationBudget(ctx context.Context) context.Context {
	if _, ok := ctx.Value(consultationBudgetKey{}).(*atomic.Int32); ok {
		return ctx
	}
	return context.WithValue(ctx, consultationBudgetKey{}, &atomic.Int32{})
}

func (s *Server) orgConsultationTool(ctx context.Context, orgID, callerID string) *service.Tool {
	members, err := s.orgAgentStore.ListOrganizationAgents(ctx, orgID)
	if err != nil {
		slog.Warn("load consultation roster failed", "org_id", orgID, "error", err)
		return nil
	}
	var ids []string
	var roster []string
	for _, member := range members {
		if member.AgentID == callerID || member.Status != "active" {
			continue
		}
		if workflow.AuthorizeToolHandler(ctx, consultAgentTool, "agent", "", member.AgentID) != nil {
			continue
		}
		agent, err := s.agentStore.GetAgent(ctx, member.AgentID)
		if err != nil || agent == nil {
			continue
		}
		ids = append(ids, agent.ID)
		roster = append(roster, fmt.Sprintf("%s: %s (%s) — %s", agent.ID, agent.Name, member.Title, agent.Config.Description))
	}
	if len(ids) == 0 {
		return nil
	}
	return &service.Tool{
		Name:        consultAgentTool,
		Description: "Ask an organization teammate for brief advice or review without creating a task. One text-only response; no tools, files, research or delegated execution. Include all relevant material in context. You retain responsibility for the task. At most 8 consultations across this entire task run. Teammates:\n" + strings.Join(roster, "\n"),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_id": map[string]any{"type": "string", "enum": ids},
				"question": map[string]any{"type": "string", "description": "A focused question or review request."},
				"context":  map[string]any{"type": "string", "description": "Relevant text, decisions and constraints. The teammate cannot see your history or read files."},
			},
			"required": []string{"agent_id", "question"},
		},
	}
}

func (s *Server) consultOrgAgent(ctx context.Context, org *service.Organization, task *service.Task, callerID string, args map[string]any, observation agentloop.ObservationContext, parentObservationID string) (string, error) {
	target, _ := args["agent_id"].(string)
	question, _ := args["question"].(string)
	background, _ := args["context"].(string)
	if target == "" || target == callerID || strings.TrimSpace(question) == "" {
		return "", fmt.Errorf("consultation requires another active teammate and a non-empty question")
	}
	if len(question)+len(background) > consultationInputBytes {
		return "", fmt.Errorf("consultation question and context exceed %d bytes; send a focused excerpt", consultationInputBytes)
	}
	ctx, cancel := context.WithTimeout(ctx, consultationTimeout)
	defer cancel()
	if err := workflow.AuthorizeToolHandler(ctx, consultAgentTool, "agent", "", target); err != nil {
		return "", err
	}
	member, err := s.orgAgentStore.GetOrganizationAgentByPair(ctx, org.ID, target)
	if err != nil {
		return "", fmt.Errorf("load consultation teammate: %w", err)
	}
	if member == nil || member.Status != "active" {
		return "", fmt.Errorf("consultation target is not an active member of this organization")
	}
	agent, err := s.agentStore.GetAgent(ctx, target)
	if err != nil {
		return "", fmt.Errorf("load consultant: %w", err)
	}
	if agent == nil {
		return "", fmt.Errorf("consultant not found")
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "providers.use", ResourceID: agent.Config.Provider}); err != nil {
		return "", err
	}
	info, err := s.getExecutionProviderInfo(ctx, agent.Config.Provider)
	if err != nil {
		return "", fmt.Errorf("resolve consultant provider: %w", err)
	}
	if err := s.checkOrganizationBudget(ctx, org); err != nil {
		return "", err
	}
	if check := s.checkBudgetFunc(); check != nil {
		if err := check(ctx, target); err != nil {
			return "", err
		}
	}
	budget, ok := ctx.Value(consultationBudgetKey{}).(*atomic.Int32)
	if !ok || budget.Add(1) > maxOrgConsultations {
		return "", fmt.Errorf("consultation budget exhausted; decide from the available advice or delegate a concrete deliverable")
	}
	model := agent.Config.Model
	if model == "" {
		model = info.defaultModel
	}
	messages := []service.Message{
		{Role: "system", Content: agent.Config.SystemPrompt + "\n\nYou are advising a teammate on an existing task, not executing it. Give one concise answer based only on the supplied material. No tools are available. Do not claim to have read files, run tools, created tasks or completed work. State missing information and uncertainty. The requesting agent owns the decision and final delivery."},
		{Role: "user", Content: question + "\n\nContext supplied by teammate:\n" + background},
	}
	resp, windowed, latency, callErr := agentloop.CallProvider(ctx, s.loopGov, service.ScopedExecutionProvider(info.provider, agent.Config.Provider), model, target, task.ID, messages, nil, agent.Config.ReasoningEffort)
	if callErr == nil {
		switch {
		case resp == nil:
			callErr = fmt.Errorf("consultant returned no response")
		case resp.Refusal != "":
			callErr = fmt.Errorf("consultant refused: %s", resp.Refusal)
		case isOutputLimitFinishReason(resp.FinishReason) || resp.FinishReason == "content_filter" || len(resp.ToolCalls) > 0:
			callErr = fmt.Errorf("consultant did not return a complete text answer (finish_reason=%s)", resp.FinishReason)
		case strings.TrimSpace(resp.Content) == "":
			callErr = fmt.Errorf("consultant returned an empty answer")
		}
	}
	// Record even on cancellation, without giving cleanup an unlimited lifetime.
	recordCtx, recordCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer recordCancel()
	usage := workflow.UsageEvent{AgentID: target, TaskID: task.ID, OrganizationID: org.ID, Provider: agent.Config.Provider, Model: model, LatencyMs: latency, Status: "ok"}
	if resp != nil {
		usage.Usage = resp.Usage
	}
	if callErr != nil {
		usage.Status, usage.ErrorMessage, usage.ErrorCode = "error", callErr.Error(), classifyHTTPError(callErr)
	}
	if record := s.recordUsageFunc(); record != nil {
		if err := record(recordCtx, usage); err != nil {
			slog.Warn("record consultation usage failed", "task_id", task.ID, "error", err)
		}
	}
	observation.AgentID, observation.Provider, observation.Model, observation.ReasoningEffort = target, agent.Config.Provider, model, agent.Config.ReasoningEffort
	if record := s.recordObservationFunc(); record != nil {
		obs := agentloop.NewGenerationObservation(agentloop.GenerationObservationParams{Context: observation, Messages: windowed, Response: resp, LatencyMs: latency, Err: callErr, ErrorCode: usage.ErrorCode, Metadata: map[string]any{"interaction": "consultation", "requesting_agent_id": callerID}})
		obs.ParentObservationID = parentObservationID
		obs.Name = consultAgentTool
		record(recordCtx, obs)
	}
	if callErr != nil {
		return "", callErr
	}
	return resp.Content, nil
}
