package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

var ErrExecutionDenied = errors.New("execution authority denied")
var ErrIsolatedWorkerUnsupported = errors.New("isolated worker execution is not supported by this deployment")

const (
	ExecutionRestricted     = "restricted"
	ExecutionIsolatedWorker = "isolated_worker"
	ExecutionTrustedHost    = "trusted_host"
)

// ExecutionPolicy is persisted independently from agent/model configuration.
// TrustedHost grants daemon-UID authority. It is NOT a tenant sandbox.
type ExecutionPolicy struct {
	WorkspaceID   string   `json:"workspace_id"`
	Mode          string   `json:"mode"`
	AllowedTools  []string `json:"allowed_tools"`
	AllowedNodes  []string `json:"allowed_nodes"`
	AllowAllTools bool     `json:"allow_all_tools"`
	AllowAllNodes bool     `json:"allow_all_nodes"`
	Version       int64    `json:"version"`
	GrantedBy     string   `json:"granted_by"`
}

// ExecutionProvenance records the initiator, never the agent's claimed identity.
// It contains no grants: current authority must be reloaded on every action.
type ExecutionProvenance struct {
	RunID             string `json:"run_id"`
	UserID            string `json:"user_id"`
	WorkspaceID       string `json:"workspace_id"`
	SessionID         string `json:"session_id,omitempty"`
	Source            string `json:"source"`
	MembershipVersion int64  `json:"membership_version"`
	PolicyVersion     int64  `json:"policy_version"`
	ServiceID         string `json:"service_id,omitempty"`
	ServiceVersion    int64  `json:"service_version,omitempty"`
}

type ExecutionAction struct {
	Kind         string // tool, node, handler, resource, file
	Name         string
	Model        string
	ResourceKind string
	ResourceID   string
	WorkspaceID  string
	Path         string // relative to the workspace root
}

// ExecutionValidation is returned by the identity adapter after consulting the
// current membership, session, policy and authoritative resource ownership.
type ExecutionValidation struct {
	Policy            ExecutionPolicy
	MembershipVersion int64
	PlatformAdmin     bool
	Allowed           bool
}

type ExecutionRevalidator func(context.Context, ExecutionProvenance, ExecutionAction) (ExecutionValidation, error)

type executionAuthority struct {
	provenance            ExecutionProvenance
	root                  string
	validate              ExecutionRevalidator
	platformCompatibility bool
}
type executionContextKey struct{}

// DeriveExecution preserves the initiator and live validator across a child or
// queued subject. It never accepts a replacement user, workspace, or grants.
func DeriveExecution(ctx context.Context, runID, source string) (context.Context, error) {
	a, ok := ctx.Value(executionContextKey{}).(executionAuthority)
	if !ok {
		return nil, ErrExecutionDenied
	}
	p := a.provenance
	p.RunID, p.Source = runID, source
	if a.platformCompatibility {
		return BindPlatformExecution(ctx, p, a.root, a.validate)
	}
	return BindExecution(ctx, p, a.root, a.validate)
}

// BindExecution must only be called by trusted entrypoint wiring after loading
// stored provenance. Model arguments must never supply root or validate.
func BindExecution(ctx context.Context, p ExecutionProvenance, root string, validate ExecutionRevalidator) (context.Context, error) {
	if p.RunID == "" || p.UserID == "" || p.WorkspaceID == "" || p.Source == "" || root == "" || validate == nil {
		return nil, fmt.Errorf("bind execution: %w", ErrExecutionDenied)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve execution root: %w", err)
	}
	root = abs
	// Installation catalog enumeration must never hitchhike into a workspace run.
	ctx = context.WithValue(ctx, executionMaintenanceKey{}, false)
	ctx = context.WithValue(ctx, executionContextKey{}, executionAuthority{provenance: p, root: root, validate: validate})
	if err := CheckExecution(ctx, ExecutionAction{Kind: "resource", Name: "execution.run", WorkspaceID: p.WorkspaceID}); err != nil {
		return nil, err
	}
	return ctx, nil
}

// BindPlatformExecution is an explicit trusted-installation compatibility
// admission, never a missing-context fallback. Boot/auth wiring must opt in.
// It retains scope, liveness, version and registered-implementation checks, but
// lets a live platform administrator use the installation's registered tools.
// This authority is not serializable in model/config/HTTP policy JSON.
func BindPlatformExecution(ctx context.Context, p ExecutionProvenance, root string, validate ExecutionRevalidator) (context.Context, error) {
	if p.ServiceID != "" {
		return nil, ErrExecutionDenied
	}
	ctx, err := BindExecution(ctx, p, root, validate)
	if err != nil {
		return nil, err
	}
	a := ctx.Value(executionContextKey{}).(executionAuthority)
	a.platformCompatibility = true
	ctx = context.WithValue(ctx, executionContextKey{}, a)
	if err := CheckExecution(ctx, ExecutionAction{Kind: "resource", Name: "execution.run"}); err != nil {
		return nil, err
	}
	return ctx, nil
}

func ExecutionFromContext(ctx context.Context) (ExecutionProvenance, string, bool) {
	a, ok := ctx.Value(executionContextKey{}).(executionAuthority)
	return a.provenance, a.root, ok
}

// CheckExecution is deliberately fail-closed for an absent identity adapter,
// unknown actions and stale runs, including background executions.
func CheckExecution(ctx context.Context, action ExecutionAction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a, ok := ctx.Value(executionContextKey{}).(executionAuthority)
	if !ok || a.validate == nil {
		return ErrExecutionDenied
	}
	if action.WorkspaceID == "" {
		action.WorkspaceID = a.provenance.WorkspaceID
	}
	if action.WorkspaceID != a.provenance.WorkspaceID {
		return ErrExecutionDenied
	}
	v, err := a.validate(ctx, a.provenance, action)
	if err != nil {
		return fmt.Errorf("revalidate execution: %w: %w", ErrExecutionDenied, err)
	}
	if !v.Allowed || v.MembershipVersion != a.provenance.MembershipVersion || v.Policy.WorkspaceID != a.provenance.WorkspaceID || v.Policy.Version != a.provenance.PolicyVersion {
		return ErrExecutionDenied
	}
	if err := ValidateExecutionMode(v.Policy.Mode); err != nil {
		return err
	}
	if a.platformCompatibility && (!v.PlatformAdmin || v.Policy.Mode != ExecutionTrustedHost || v.Policy.GrantedBy == "") {
		return ErrExecutionDenied
	}
	host := false
	switch action.Kind {
	case "tool":
		var known bool
		host, known = ExecutionToolClass(action.Name)
		if !known || (!a.platformCompatibility && !v.Policy.AllowAllTools && !slices.Contains(v.Policy.AllowedTools, action.Name)) {
			return ErrExecutionDenied
		}
	case "node":
		var known bool
		host, known = ExecutionNodeClass(action.Name)
		if !known || (!a.platformCompatibility && !v.Policy.AllowAllNodes && !slices.Contains(v.Policy.AllowedNodes, action.Name)) {
			return ErrExecutionDenied
		}
	case "skill_tool", "mcp_tool", "delegate":
		if action.ResourceID == "" || action.Name == "" || (!a.platformCompatibility && !v.Policy.AllowAllTools && !slices.Contains(v.Policy.AllowedTools, action.Kind+":"+action.ResourceID+":"+action.Name)) {
			return ErrExecutionDenied
		}
		host = action.Kind != "delegate"
	case "inline_tool":
		if action.Name == "" || (!a.platformCompatibility && !v.Policy.AllowAllTools && !slices.Contains(v.Policy.AllowedTools, "inline_tool:"+action.Name)) {
			return ErrExecutionDenied
		}
		host = true
	case "handler":
		if action.Name != "bash" && action.Name != "javascript" {
			return ErrExecutionDenied
		}
		host = true // Goja's helpers expose arbitrary network and credentials.
	case "file":
		if action.Name != "files.read" && action.Name != "files.write" && action.Name != "files.host" {
			return ErrExecutionDenied
		}
		host = action.Name == "files.host"
		if host && !v.PlatformAdmin {
			return ErrExecutionDenied
		}
	case "resource":
		switch action.Name {
		case "execution.run", "providers.use", "skills.use", "variables.read", "connections.use", "agents.run", "workflows.run", "mcp.use", "mcp_servers.use", "node_configs.use", "tasks.run", "organizations.run", "chats.run", "triggers.use", "bots.use":
		default:
			return ErrExecutionDenied
		}
	default:
		return ErrExecutionDenied
	}
	if host {
		if v.Policy.Mode != ExecutionTrustedHost || v.Policy.GrantedBy == "" {
			return ErrExecutionDenied
		}
		hostAction := action
		hostAction.Kind, hostAction.Name = "resource", "execution.host"
		hostAction.ResourceKind = action.Kind
		hv, err := a.validate(ctx, a.provenance, hostAction)
		if err != nil {
			return fmt.Errorf("revalidate host authority: %w", err)
		}
		if !hv.Allowed || hv.Policy.Mode != ExecutionTrustedHost || hv.Policy.Version != v.Policy.Version || hv.Policy.WorkspaceID != action.WorkspaceID || hv.MembershipVersion != a.provenance.MembershipVersion {
			return ErrExecutionDenied
		}
	}
	return ctx.Err()
}

func ValidateExecutionMode(mode string) error {
	switch mode {
	case "", ExecutionRestricted, ExecutionTrustedHost:
		return nil
	case ExecutionIsolatedWorker:
		return ErrIsolatedWorkerUnsupported
	default:
		return fmt.Errorf("unknown execution mode %q", mode)
	}
}

// Explicit registries: adding a new implementation does not grant authority.
// Legacy file helpers and network tools are host-only until individually rooted.
func ExecutionToolClass(name string) (host, known bool) {
	switch name {
	case "file_read", "file_write", "file_list", "batch_execute":
		return false, true
	case "bash_execute", "js_execute", "http_request", "url_fetch", "file_edit", "file_multiedit", "file_patch", "file_glob", "file_grep", "lsp_query", "transcribe_local":
		return true, true
	default:
		return registeredExecutionClass("tool", name)
	}
}

func ExecutionNodeClass(name string) (host, known bool) {
	switch name {
	case "input", "output", "template", "log", "http_trigger", "cron_trigger", "agent_config", "skill_config", "mcp_config", "llm_call", "agent_call", "workflow_call":
		return false, true
	case "exec", "script", "conditional", "loop", "http_request", "email", "chat_reply", "embedding", "audio_transcribe", "audio_generate", "vision_analyze", "image_generate":
		return true, true
	default:
		return registeredExecutionClass("node", name)
	}
}

var executionClasses = struct {
	sync.RWMutex
	values map[string]bool
}{values: map[string]bool{}}

// RegisterExecutionCapability is for trusted compiled registration code, never
// runtime configuration. The conservative default for an unreviewed builtin is
// host authority; merely adding a name to a policy cannot register an executor.
func RegisterExecutionCapability(kind, name string, host bool) {
	if (kind != "node" && kind != "tool") || name == "" {
		return
	}
	executionClasses.Lock()
	defer executionClasses.Unlock()
	key := kind + ":" + name
	if _, exists := executionClasses.values[key]; !exists {
		executionClasses.values[key] = host
	}
}
func registeredExecutionClass(kind, name string) (bool, bool) {
	executionClasses.RLock()
	defer executionClasses.RUnlock()
	host, known := executionClasses.values[kind+":"+name]
	return host, known
}

// ExecutionCapabilityNames supplies the settings picker from compiled runtime
// registrations, plus the core implementations classified directly above.
func ExecutionCapabilityNames(kind string) []string {
	var names []string
	switch kind {
	case "tool":
		names = strings.Fields("file_read file_write file_list batch_execute bash_execute js_execute http_request url_fetch file_edit file_multiedit file_patch file_glob file_grep lsp_query transcribe_local")
	case "node":
		names = strings.Fields("input output template log http_trigger cron_trigger agent_config skill_config mcp_config llm_call agent_call workflow_call exec script conditional loop http_request email chat_reply embedding audio_transcribe audio_generate vision_analyze image_generate")
	default:
		return nil
	}
	executionClasses.RLock()
	for key := range executionClasses.values {
		if name, ok := strings.CutPrefix(key, kind+":"); ok {
			names = append(names, name)
		}
	}
	executionClasses.RUnlock()
	slices.Sort(names)
	return slices.Compact(names)
}

type ExecutionStorer interface {
	GetExecutionPolicy(context.Context, string) (*ExecutionPolicy, error)
	SaveExecutionPolicy(context.Context, ExecutionPolicy) error
	CreateExecutionProvenance(context.Context, ExecutionProvenance) error
	GetExecutionProvenance(context.Context, string) (*ExecutionProvenance, error)
}

// ExecutionResourceResolver resolves canonical IDs using authoritative workspace
// ownership before global legacy registries may be consulted.
type ExecutionResourceResolver interface {
	ResolveExecutionResource(context.Context, string, string) (AccessResource, error)
}

type ExecutionTaskCreator interface {
	CreateExecutionChildTask(context.Context, Task) (*Task, error)
	CreateExecutionTask(context.Context, Task) (*Task, error)
}

type ExecutionChatStorer interface {
	GetExecutionBotSession(context.Context, string, string, string, string) (*ChatSession, error)
	CreateExecutionBotSession(context.Context, ChatSession) (*ChatSession, error)
}

// PrepareExecutionPolicyChange revalidates the platform administrator rather
// than trusting a role or a GrantedBy supplied in a workspace update payload.
func PrepareExecutionPolicyChange(ctx context.Context, policy ExecutionPolicy) (ExecutionPolicy, error) {
	a, ok := ctx.Value(executionContextKey{}).(executionAuthority)
	if !ok || a.validate == nil || policy.WorkspaceID != a.provenance.WorkspaceID {
		return ExecutionPolicy{}, ErrExecutionDenied
	}
	v, err := a.validate(ctx, a.provenance, ExecutionAction{Kind: "resource", Name: "execution.policy.manage", WorkspaceID: policy.WorkspaceID})
	if err != nil {
		return ExecutionPolicy{}, err
	}
	if !v.Allowed || !v.PlatformAdmin || v.MembershipVersion != a.provenance.MembershipVersion || v.Policy.WorkspaceID != policy.WorkspaceID || v.Policy.Version != policy.Version {
		return ExecutionPolicy{}, ErrExecutionDenied
	}
	if err := ValidateExecutionMode(policy.Mode); err != nil {
		return ExecutionPolicy{}, err
	}
	if policy.Mode == "" {
		policy.Mode = ExecutionRestricted
	}
	for _, name := range policy.AllowedTools {
		if strings.HasPrefix(name, "inline_tool:") && len(name) > len("inline_tool:") {
			continue
		}
		if _, known := ExecutionToolClass(name); !known {
			parts := strings.SplitN(name, ":", 3)
			if len(parts) != 3 || parts[1] == "" || parts[2] == "" || (parts[0] != "skill_tool" && parts[0] != "mcp_tool" && parts[0] != "delegate") {
				return ExecutionPolicy{}, fmt.Errorf("unknown tool %q", name)
			}
		}
	}
	for _, name := range policy.AllowedNodes {
		if _, known := ExecutionNodeClass(name); !known {
			return ExecutionPolicy{}, fmt.Errorf("unknown node %q", name)
		}
	}
	policy.GrantedBy = a.provenance.UserID
	return policy, ctx.Err()
}
