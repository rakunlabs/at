package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

func (s *Server) chatOrganization(ctx context.Context, id string) (*service.Organization, error) {
	if s.organizationStore == nil || s.orgAgentStore == nil {
		return nil, fmt.Errorf("organization store not configured")
	}
	flags, err := s.featureFlags(ctx)
	if err != nil {
		return nil, err
	}
	if !featureEnabledIn(service.FeatureOrganizations, flags) {
		return nil, fmt.Errorf("organizations feature is disabled")
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "organizations.run", ResourceID: id}); err != nil {
		return nil, err
	}
	org, err := s.organizationStore.GetOrganization(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load chat organization: %w", err)
	}
	if org == nil || org.HeadAgentID == "" {
		return nil, fmt.Errorf("organization is unavailable or has no head agent")
	}
	member, err := s.orgAgentStore.GetOrganizationAgentByPair(ctx, org.ID, org.HeadAgentID)
	if err != nil {
		return nil, fmt.Errorf("load organization head membership: %w", err)
	}
	if member == nil || member.Status != "active" {
		return nil, fmt.Errorf("organization head is not an active member")
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "agents.run", ResourceID: org.HeadAgentID}); err != nil {
		return nil, err
	}
	return org, nil
}

// The production agent itself is unchanged. Only its conversational view loses
// production tools; an explicit start runs the original agent and its pipeline.
func organizationChatAgent(agent *service.Agent, org *service.Organization) *service.Agent {
	copy := *agent
	copy.Config.MCPs, copy.Config.MCPSets, copy.Config.Skills, copy.Config.BuiltinTools = nil, nil, nil, nil
	copy.Config.SystemPrompt = fmt.Sprintf(`You are the conversational representative of organization %s. Its mission: %s.
Discuss ideas, answer questions and clarify requirements in the user's language. Ordinary conversation must not create a task.
Your production role below is reference material for understanding the team's capabilities, NOT an instruction to produce something on every chat message:
<production_role>%s</production_role>
When the user explicitly asks to execute/produce a deliverable, call organization_start_task with a self-contained brief preserving their constraints. The original head agent will run the required specialist stages in the background. Never substitute a chat answer for actual production or claim it has finished before checking.
Use organization_task_status for progress and results. It refers to this conversation's selected task. Do not poll, sleep or wait for production in the chat loop; report the status and let the user return later.
Use organization_task_feedback to record changes against that task; feedback does not restart production. Explain that accurately, especially if work is already running or completed.
Starting again without new_task returns the existing task. Set new_task=true only if the user explicitly requests a separate new deliverable. Do not duplicate work for status questions or revisions.
`, org.Name, org.Description, agent.Config.SystemPrompt)
	return &copy
}

func organizationChatTools() []service.Tool {
	return []service.Tool{
		{Name: "organization_start_task", Description: "Start explicitly requested work for this conversation's organization. Reuses its selected task unless new_task=true is explicitly requested. Returns immediately; execution happens in the background.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string"}, "brief": map[string]any{"type": "string"}, "new_task": map[string]any{"type": "boolean"}}, "required": []string{"title", "brief"}}},
		{Name: "organization_task_status", Description: "Read the selected task's current status and result. Does not start work.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
		{Name: "organization_task_feedback", Description: "Record feedback on the selected task without starting another task or automatically rerunning production.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"feedback": map[string]any{"type": "string"}}, "required": []string{"feedback"}}},
	}
}

func isOrganizationChatTool(name string) bool {
	return name == "organization_start_task" || name == "organization_task_status" || name == "organization_task_feedback"
}

func (s *Server) executeOrganizationChatTool(ctx context.Context, session *service.ChatSession, name string, args map[string]any) (string, error) {
	if !session.Config.OrganizationChat || !isOrganizationChatTool(name) || s.taskStore == nil {
		return "", fmt.Errorf("organization chat tool is unavailable")
	}
	org, err := s.chatOrganization(ctx, session.OrganizationID)
	if err != nil {
		return "", err
	}
	flags, err := s.featureFlags(ctx)
	if err != nil || !featureEnabledIn(service.FeatureTasks, flags) || !featureEnabledIn(service.FeatureBuiltinTools, flags) {
		return "", fmt.Errorf("task tools are unavailable")
	}
	permission := map[string]string{"organization_start_task": "org_task_intake", "organization_task_status": "task_get", "organization_task_feedback": "task_add_comment"}[name]
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "tool", Name: permission}); err != nil {
		return "", err
	}
	current, err := s.chatSessionStore.GetChatSession(ctx, session.ID)
	if err != nil || current == nil {
		return "", fmt.Errorf("chat session is no longer available")
	}
	var task *service.Task
	if current.Config.ActiveTaskID != "" {
		task, err = s.taskStore.GetTask(ctx, current.Config.ActiveTaskID)
		if err != nil {
			return "", fmt.Errorf("load selected task: %w", err)
		}
		if task == nil || task.OrganizationID != org.ID {
			return "", fmt.Errorf("selected task is no longer available in this organization")
		}
	}
	if name == "organization_start_task" && (task == nil || boolValue(args["new_task"])) {
		if task != nil && (task.Status == service.TaskStatusTodo || task.Status == service.TaskStatusInProgress || s.isDelegationActive(task.ID)) {
			return "", fmt.Errorf("a task is already running in this conversation; follow its progress before starting another")
		}
		title, _ := args["title"].(string)
		brief, _ := args["brief"].(string)
		if strings.TrimSpace(title) == "" || strings.TrimSpace(brief) == "" || len(title) > 500 || len(brief) > 32768 {
			return "", fmt.Errorf("provide a title (up to 500 bytes) and self-contained brief (up to 32 KiB)")
		}
		creator, ok := s.chatSessionStore.(service.OrganizationChatTaskCreator)
		if !ok {
			return "", fmt.Errorf("organization chat task creation is unavailable")
		}
		identifier := ""
		if org.IssuePrefix != "" {
			counter, err := s.organizationStore.IncrementIssueCounter(ctx, org.ID)
			if err != nil {
				return "", fmt.Errorf("generate task identifier: %w", err)
			}
			identifier = fmt.Sprintf("%s-%d", org.IssuePrefix, counter)
		}
		var created bool
		task, created, err = creator.CreateOrganizationChatTask(ctx, session.ID, service.Task{OrganizationID: org.ID, AssignedAgentID: org.HeadAgentID, Title: title, Description: brief, Status: service.TaskStatusTodo, Identifier: identifier}, boolValue(args["new_task"]))
		if err != nil {
			return "", fmt.Errorf("create organization task: %w", err)
		}
		session.Config.ActiveTaskID = task.ID
		if created {
			if err := s.startDelegationRun(context.WithoutCancel(ctx), org, task, org.HeadAgentID, 0, nil); err != nil {
				s.persistDelegationFailure(ctx, task, err)
				return "", fmt.Errorf("start organization task: %w", err)
			}
		}
	}
	if task == nil {
		return `{"status":"no_task","message":"No work has been started in this conversation."}`, nil
	}
	if name == "organization_task_feedback" {
		feedback, _ := args["feedback"].(string)
		if strings.TrimSpace(feedback) == "" || len(feedback) > 16384 {
			return "", fmt.Errorf("provide feedback up to 16 KiB")
		}
		if _, err := s.execTaskAddComment(ctx, map[string]any{"task_id": task.ID, "body": feedback}); err != nil {
			return "", err
		}
		return fmt.Sprintf(`{"task_id":%q,"feedback_saved":true,"production_restarted":false}`, task.ID), nil
	}
	result, err := json.Marshal(map[string]any{"task_id": task.ID, "title": task.Title, "status": task.Status, "result": task.Result, "task_url": "#/tasks/" + task.ID})
	return string(result), err
}
