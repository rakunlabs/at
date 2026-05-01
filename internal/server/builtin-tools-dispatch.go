package server

import "context"

// dispatchBuiltinTool dispatches a tool call to the appropriate executor by name.
func (s *Server) dispatchBuiltinTool(ctx context.Context, name string, args map[string]any) (string, error) {
	switch name {
	// Original tools.
	case "http_request":
		return s.execHTTPRequest(ctx, args)
	case "bash_execute":
		return s.execBash(ctx, args)
	case "js_execute":
		return s.execJS(ctx, args)
	case "url_fetch":
		return s.execURLFetch(ctx, args)

	// File tools.
	case "file_read":
		return s.execFileRead(ctx, args)
	case "file_write":
		return s.execFileWrite(ctx, args)
	case "file_edit":
		return s.execFileEdit(ctx, args)
	case "file_multiedit":
		return s.execFileMultiEdit(ctx, args)
	case "file_patch":
		return s.execFilePatch(ctx, args)
	case "file_glob":
		return s.execFileGlob(ctx, args)
	case "file_grep":
		return s.execFileGrep(ctx, args)
	case "file_list":
		return s.execFileList(ctx, args)

	// Task management tools.
	case "todo_write":
		return s.execTodoWrite(ctx, args)
	case "todo_read":
		return s.execTodoRead(ctx, args)
	case "batch_execute":
		return s.execBatchExecute(ctx, args)

	// LSP tool.
	case "lsp_query":
		return s.execLSPQuery(ctx, args)

	// Workflow & trigger management tools.
	case "workflow_list":
		return s.execWorkflowList(ctx, args)
	case "workflow_get":
		return s.execWorkflowGet(ctx, args)
	case "workflow_create":
		return s.execWorkflowCreate(ctx, args)
	case "workflow_update":
		return s.execWorkflowUpdate(ctx, args)
	case "workflow_delete":
		return s.execWorkflowDelete(ctx, args)
	case "workflow_run":
		return s.execWorkflowRun(ctx, args)
	case "trigger_list":
		return s.execTriggerList(ctx, args)
	case "trigger_create":
		return s.execTriggerCreate(ctx, args)
	case "trigger_get":
		return s.execTriggerGet(ctx, args)
	case "trigger_update":
		return s.execTriggerUpdate(ctx, args)
	case "trigger_delete":
		return s.execTriggerDelete(ctx, args)

	// User preference tools.
	case "set_user_preference":
		return s.execSetUserPreference(ctx, args)
	case "get_user_preferences":
		return s.execGetUserPreferences(ctx, args)

	// Persistent task tools.
	case "task_create":
		return s.execTaskCreate(ctx, args)
	case "task_list":
		return s.execTaskList(ctx, args)
	case "task_get":
		return s.execTaskGet(ctx, args)
	case "task_update":
		return s.execTaskUpdate(ctx, args)
	case "task_add_comment":
		return s.execTaskAddComment(ctx, args)
	case "task_process":
		return s.execTaskProcess(ctx, args)

	// Organization tools.
	case "org_create":
		return s.execOrgCreate(ctx, args)
	case "org_list":
		return s.execOrgList(ctx, args)
	case "org_get":
		return s.execOrgGet(ctx, args)
	case "org_add_agent":
		return s.execOrgAddAgent(ctx, args)
	case "org_task_intake":
		return s.execOrgTaskIntake(ctx, args)

	// Agent tools.
	case "agent_create":
		return s.execAgentCreate(ctx, args)
	case "agent_list":
		return s.execAgentList(ctx, args)
	case "agent_get":
		return s.execAgentGet(ctx, args)
	case "agent_update":
		return s.execAgentUpdate(ctx, args)

	// Skill tools.
	case "skill_list":
		return s.execSkillList(ctx, args)
	case "skill_install_template":
		return s.execSkillInstallTemplate(ctx, args)

	// Provider tools.
	case "provider_list":
		return s.execProviderList(ctx, args)
	case "provider_get":
		return s.execProviderGet(ctx, args)

	// Approval tools.
	case "approval_list_pending":
		return s.execApprovalListPending(ctx, args)
	case "approval_decide":
		return s.execApprovalDecide(ctx, args)

	// Bot config management tools.
	case "bot_list":
		return s.execBotList(ctx, args)
	case "bot_get":
		return s.execBotGet(ctx, args)
	case "bot_update":
		return s.execBotUpdate(ctx, args)

	default:
		return "", nil
	}
}
