package server

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/rakunlabs/at/internal/service"
)

type builtinExecutionClass uint8

const (
	builtinNonHost builtinExecutionClass = iota + 1
	builtinHost
	builtinHostFiles // Host execution plus installation-admin host-file admission.
)

type builtinExecutor func(*Server, context.Context, map[string]any) (string, error)

type builtinToolBinding struct {
	Name    string
	Execute builtinExecutor
	Class   builtinExecutionClass
}

type builtinToolRegistration struct {
	builtinToolDef
	Execute builtinExecutor
	Class   builtinExecutionClass
}

type builtinRegistry struct {
	byName      map[string]builtinToolRegistration
	definitions []builtinToolDef
}

// Schema payloads stay separate for readability, but no consumer can advertise
// or execute them independently. Missing/duplicate/mismatched entries fail closed.
var builtinToolRegistry builtinRegistry
var builtinToolRegistryErr error

// Ordered, wire-only compatibility view used by API, MCP and workflow discovery.
var builtinTools []builtinToolDef

func init() {
	builtinToolRegistry, builtinToolRegistryErr = newBuiltinRegistry(builtinToolSchemas, builtinToolBindings)
	if builtinToolRegistryErr != nil {
		slog.Error("built-in tool registry unavailable", "error", builtinToolRegistryErr)
		return
	}
	builtinTools = builtinToolRegistry.definitions
	for name, tool := range builtinToolRegistry.byName {
		service.RegisterExecutionCapability("tool", name, tool.Class != builtinNonHost)
	}
}

func newBuiltinRegistry(definitions []builtinToolDef, bindings []builtinToolBinding) (builtinRegistry, error) {
	registry := builtinRegistry{byName: make(map[string]builtinToolRegistration, len(definitions))}
	executors := make(map[string]builtinToolBinding, len(bindings))
	for _, binding := range bindings {
		if binding.Name == "" || binding.Execute == nil {
			return builtinRegistry{}, fmt.Errorf("built-in %q has no name or executor", binding.Name)
		}
		switch binding.Class {
		case builtinNonHost, builtinHost, builtinHostFiles:
		default:
			return builtinRegistry{}, fmt.Errorf("built-in %q has no valid execution class", binding.Name)
		}
		if _, exists := executors[binding.Name]; exists {
			return builtinRegistry{}, fmt.Errorf("duplicate built-in executor %q", binding.Name)
		}
		executors[binding.Name] = binding
	}
	for _, def := range definitions {
		if def.Name == "" || def.Description == "" || def.InputSchema == nil {
			return builtinRegistry{}, fmt.Errorf("built-in %q has an incomplete schema", def.Name)
		}
		if _, exists := registry.byName[def.Name]; exists {
			return builtinRegistry{}, fmt.Errorf("duplicate built-in schema %q", def.Name)
		}
		binding, exists := executors[def.Name]
		if !exists {
			return builtinRegistry{}, fmt.Errorf("built-in schema %q has no executor", def.Name)
		}
		registry.byName[def.Name] = builtinToolRegistration{builtinToolDef: def, Execute: binding.Execute, Class: binding.Class}
		registry.definitions = append(registry.definitions, def)
		delete(executors, def.Name)
	}
	if len(executors) != 0 {
		return builtinRegistry{}, fmt.Errorf("%d built-in executors have no schema", len(executors))
	}
	return registry, nil
}

// Each executor declares its class explicitly; there is no guessed/default host
// class. These entries are joined with schemas into the sole runtime registry.
var builtinToolBindings = []builtinToolBinding{
	{Name: "http_request", Execute: (*Server).execHTTPRequest, Class: builtinHost},
	{Name: "bash_execute", Execute: (*Server).execBash, Class: builtinHost},
	{Name: "js_execute", Execute: (*Server).execJS, Class: builtinHost},
	{Name: "url_fetch", Execute: (*Server).execURLFetch, Class: builtinHost},
	{Name: "file_read", Execute: (*Server).execScopedFileRead, Class: builtinNonHost},
	{Name: "file_write", Execute: (*Server).execScopedFileWrite, Class: builtinNonHost},
	{Name: "file_edit", Execute: (*Server).execFileEdit, Class: builtinHostFiles},
	{Name: "file_multiedit", Execute: (*Server).execFileMultiEdit, Class: builtinHostFiles},
	{Name: "file_patch", Execute: (*Server).execFilePatch, Class: builtinHostFiles},
	{Name: "file_glob", Execute: (*Server).execFileGlob, Class: builtinHostFiles},
	{Name: "file_grep", Execute: (*Server).execFileGrep, Class: builtinHostFiles},
	{Name: "file_list", Execute: (*Server).execScopedFileList, Class: builtinNonHost},
	{Name: "todo_write", Execute: (*Server).execTodoWrite, Class: builtinNonHost},
	{Name: "todo_read", Execute: (*Server).execTodoRead, Class: builtinNonHost},
	{Name: "batch_execute", Execute: (*Server).execBatchExecute, Class: builtinNonHost},
	{Name: "lsp_query", Execute: (*Server).execLSPQuery, Class: builtinHost},
	{Name: "workflow_list", Execute: (*Server).execWorkflowList, Class: builtinHost},
	{Name: "workflow_get", Execute: (*Server).execWorkflowGet, Class: builtinHost},
	{Name: "workflow_create", Execute: (*Server).execWorkflowCreate, Class: builtinHost},
	{Name: "workflow_update", Execute: (*Server).execWorkflowUpdate, Class: builtinHost},
	{Name: "workflow_activate", Execute: (*Server).execWorkflowActivate, Class: builtinHost},
	{Name: "workflow_delete", Execute: (*Server).execWorkflowDelete, Class: builtinHost},
	{Name: "workflow_run", Execute: (*Server).execWorkflowRun, Class: builtinHost},
	{Name: "trigger_list", Execute: (*Server).execTriggerList, Class: builtinHost},
	{Name: "trigger_create", Execute: (*Server).execTriggerCreate, Class: builtinHost},
	{Name: "trigger_get", Execute: (*Server).execTriggerGet, Class: builtinHost},
	{Name: "trigger_update", Execute: (*Server).execTriggerUpdate, Class: builtinHost},
	{Name: "trigger_delete", Execute: (*Server).execTriggerDelete, Class: builtinHost},
	{Name: "decide", Execute: (*Server).execDecide, Class: builtinNonHost},
	{Name: "current_time", Execute: (*Server).execCurrentTime, Class: builtinNonHost},
	{Name: "whoami", Execute: (*Server).execWhoami, Class: builtinNonHost},
	{Name: "set_user_preference", Execute: (*Server).execSetUserPreference, Class: builtinNonHost},
	{Name: "get_user_preferences", Execute: (*Server).execGetUserPreferences, Class: builtinNonHost},
	{Name: "task_create", Execute: (*Server).execTaskCreate, Class: builtinHost},
	{Name: "task_list", Execute: (*Server).execTaskList, Class: builtinHost},
	{Name: "task_get", Execute: (*Server).execTaskGet, Class: builtinHost},
	{Name: "task_update", Execute: (*Server).execTaskUpdate, Class: builtinHost},
	{Name: "task_add_comment", Execute: (*Server).execTaskAddComment, Class: builtinHost},
	{Name: "task_process", Execute: (*Server).execTaskProcess, Class: builtinHost},
	{Name: "task_wait", Execute: (*Server).execTaskWait, Class: builtinHost},
	{Name: "task_current", Execute: (*Server).execTaskCurrent, Class: builtinHost},
	{Name: "task_children", Execute: (*Server).execTaskChildren, Class: builtinHost},
	{Name: "task_create_child", Execute: (*Server).execTaskCreateChild, Class: builtinHost},
	{Name: "task_update_current", Execute: (*Server).execTaskUpdateCurrent, Class: builtinHost},
	{Name: "task_comment_current", Execute: (*Server).execTaskCommentCurrent, Class: builtinHost},
	{Name: "task_complete", Execute: (*Server).execTaskComplete, Class: builtinHost},
	{Name: "task_block", Execute: (*Server).execTaskBlock, Class: builtinHost},
	{Name: "llm_trace_list", Execute: (*Server).execLLMTraceList, Class: builtinHost},
	{Name: "llm_trace_get", Execute: (*Server).execLLMTraceGet, Class: builtinHost},
	{Name: "llm_observation_get", Execute: (*Server).execLLMObservationGet, Class: builtinHost},
	{Name: "org_create", Execute: (*Server).execOrgCreate, Class: builtinHost},
	{Name: "org_list", Execute: (*Server).execOrgList, Class: builtinHost},
	{Name: "org_get", Execute: (*Server).execOrgGet, Class: builtinHost},
	{Name: "org_add_agent", Execute: (*Server).execOrgAddAgent, Class: builtinHost},
	{Name: "org_task_intake", Execute: (*Server).execOrgTaskIntake, Class: builtinHost},
	{Name: "agent_create", Execute: (*Server).execAgentCreate, Class: builtinHost},
	{Name: "agent_list", Execute: (*Server).execAgentList, Class: builtinHost},
	{Name: "agent_get", Execute: (*Server).execAgentGet, Class: builtinHost},
	{Name: "agent_update", Execute: (*Server).execAgentUpdate, Class: builtinHost},
	{Name: "agent_run", Execute: (*Server).execAgentRun, Class: builtinNonHost},
	{Name: "agent_run_status", Execute: (*Server).execAgentRunStatus, Class: builtinNonHost},
	{Name: "agent_run_cancel", Execute: (*Server).execAgentRunCancel, Class: builtinNonHost},
	{Name: "skill_list", Execute: (*Server).execSkillList, Class: builtinHost},
	{Name: "skill_get", Execute: (*Server).execSkillGet, Class: builtinHost},
	{Name: "skill_install_template", Execute: (*Server).execSkillInstallTemplate, Class: builtinHost},
	{Name: "skill_create", Execute: (*Server).execSkillCreate, Class: builtinHost},
	{Name: "skill_update", Execute: (*Server).execSkillUpdate, Class: builtinHost},
	{Name: "skill_delete", Execute: (*Server).execSkillDelete, Class: builtinHost},
	{Name: "skill_export", Execute: (*Server).execSkillExport, Class: builtinHost},
	{Name: "skill_import", Execute: (*Server).execSkillImport, Class: builtinHost},
	{Name: "skill_import_url", Execute: (*Server).execSkillImportURL, Class: builtinHost},
	{Name: "skill_import_skillmd", Execute: (*Server).execSkillImportSkillMD, Class: builtinHost},
	{Name: "mcp_server_list", Execute: (*Server).execMCPServerList, Class: builtinHost},
	{Name: "mcp_server_get", Execute: (*Server).execMCPServerGet, Class: builtinHost},
	{Name: "mcp_server_create", Execute: (*Server).execMCPServerCreate, Class: builtinHost},
	{Name: "mcp_server_update", Execute: (*Server).execMCPServerUpdate, Class: builtinHost},
	{Name: "mcp_server_delete", Execute: (*Server).execMCPServerDelete, Class: builtinHost},
	{Name: "mcp_set_list", Execute: (*Server).execMCPSetList, Class: builtinHost},
	{Name: "mcp_set_get", Execute: (*Server).execMCPSetGet, Class: builtinHost},
	{Name: "mcp_set_create", Execute: (*Server).execMCPSetCreate, Class: builtinHost},
	{Name: "mcp_set_update", Execute: (*Server).execMCPSetUpdate, Class: builtinHost},
	{Name: "mcp_set_delete", Execute: (*Server).execMCPSetDelete, Class: builtinHost},
	{Name: "provider_list", Execute: (*Server).execProviderList, Class: builtinHost},
	{Name: "provider_get", Execute: (*Server).execProviderGet, Class: builtinHost},
	{Name: "approval_list_pending", Execute: (*Server).execApprovalListPending, Class: builtinHost},
	{Name: "approval_decide", Execute: (*Server).execApprovalDecide, Class: builtinHost},
	{Name: "bot_list", Execute: (*Server).execBotList, Class: builtinHost},
	{Name: "bot_get", Execute: (*Server).execBotGet, Class: builtinHost},
	{Name: "bot_update", Execute: (*Server).execBotUpdate, Class: builtinHost},
	{Name: "bot_create", Execute: (*Server).execBotCreate, Class: builtinHost},
	{Name: "bot_delete", Execute: (*Server).execBotDelete, Class: builtinHost},
	{Name: "bot_start", Execute: (*Server).execBotStart, Class: builtinHost},
	{Name: "bot_stop", Execute: (*Server).execBotStop, Class: builtinHost},
	{Name: "bot_status", Execute: (*Server).execBotStatus, Class: builtinHost},
	{Name: "provider_create", Execute: (*Server).execProviderCreate, Class: builtinHost},
	{Name: "provider_update", Execute: (*Server).execProviderUpdate, Class: builtinHost},
	{Name: "provider_set_model_limit", Execute: (*Server).execProviderSetModelLimit, Class: builtinHost},
	{Name: "provider_set_model_capability", Execute: (*Server).execProviderSetModelCapability, Class: builtinHost},
	{Name: "provider_delete", Execute: (*Server).execProviderDelete, Class: builtinHost},
	{Name: "provider_discover_models", Execute: (*Server).execProviderDiscoverModels, Class: builtinHost},
	{Name: "apitoken_list", Execute: (*Server).execAPITokenList, Class: builtinHost},
	{Name: "apitoken_create", Execute: (*Server).execAPITokenCreate, Class: builtinHost},
	{Name: "apitoken_update", Execute: (*Server).execAPITokenUpdate, Class: builtinHost},
	{Name: "apitoken_delete", Execute: (*Server).execAPITokenDelete, Class: builtinHost},
	{Name: "apitoken_get_usage", Execute: (*Server).execAPITokenGetUsage, Class: builtinHost},
	{Name: "apitoken_reset_usage", Execute: (*Server).execAPITokenResetUsage, Class: builtinHost},
	{Name: "variable_list", Execute: (*Server).execVariableList, Class: builtinHost},
	{Name: "variable_get", Execute: (*Server).execVariableGet, Class: builtinHost},
	{Name: "variable_create", Execute: (*Server).execVariableCreate, Class: builtinHost},
	{Name: "variable_update", Execute: (*Server).execVariableUpdate, Class: builtinHost},
	{Name: "variable_delete", Execute: (*Server).execVariableDelete, Class: builtinHost},
	{Name: "connection_list", Execute: (*Server).execConnectionList, Class: builtinHost},
	{Name: "connection_get", Execute: (*Server).execConnectionGet, Class: builtinHost},
	{Name: "connection_create", Execute: (*Server).execConnectionCreate, Class: builtinHost},
	{Name: "connection_update", Execute: (*Server).execConnectionUpdate, Class: builtinHost},
	{Name: "connection_delete", Execute: (*Server).execConnectionDelete, Class: builtinHost},
	{Name: "connection_import_from_variables", Execute: (*Server).execConnectionImportFromVariables, Class: builtinHost},
	{Name: "node_config_list", Execute: (*Server).execNodeConfigList, Class: builtinHost},
	{Name: "node_config_get", Execute: (*Server).execNodeConfigGet, Class: builtinHost},
	{Name: "node_config_create", Execute: (*Server).execNodeConfigCreate, Class: builtinHost},
	{Name: "node_config_update", Execute: (*Server).execNodeConfigUpdate, Class: builtinHost},
	{Name: "node_config_delete", Execute: (*Server).execNodeConfigDelete, Class: builtinHost},
	{Name: "guide_list", Execute: (*Server).execGuideList, Class: builtinNonHost},
	{Name: "guide_get", Execute: (*Server).execGuideGet, Class: builtinNonHost},
	{Name: "guide_create", Execute: (*Server).execGuideCreate, Class: builtinHost},
	{Name: "guide_update", Execute: (*Server).execGuideUpdate, Class: builtinHost},
	{Name: "guide_delete", Execute: (*Server).execGuideDelete, Class: builtinHost},
	{Name: "agent_delete", Execute: (*Server).execAgentDelete, Class: builtinHost},
	{Name: "org_update", Execute: (*Server).execOrgUpdate, Class: builtinHost},
	{Name: "org_delete", Execute: (*Server).execOrgDelete, Class: builtinHost},
	{Name: "org_list_agents", Execute: (*Server).execOrgListAgents, Class: builtinHost},
	{Name: "org_update_agent", Execute: (*Server).execOrgUpdateAgent, Class: builtinHost},
	{Name: "org_remove_agent", Execute: (*Server).execOrgRemoveAgent, Class: builtinHost},
	{Name: "task_delete", Execute: (*Server).execTaskDelete, Class: builtinHost},
	{Name: "task_cancel", Execute: (*Server).execTaskCancel, Class: builtinHost},
	{Name: "active_delegation_list", Execute: (*Server).execActiveDelegationList, Class: builtinHost},
}
