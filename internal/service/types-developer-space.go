package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	// DefaultDeveloperImage is the base image of a space that has not chosen
	// one. It is a stock distribution image: the container manager derives a
	// runtime image from it that adds bash/git/python3/coreutils.
	DefaultDeveloperImage = "debian:13.7-slim"

	DeveloperSpacePending = "pending"
	DeveloperSpaceReady   = "ready"
	DeveloperSpaceStopped = "stopped"
	DeveloperSpaceError   = "error"

	DeveloperModePlan   = "plan"
	DeveloperModeBuild  = "build"
	DeveloperModeReview = "review"

	DeveloperSessionIdle              = "idle"
	DeveloperSessionRunning           = "running"
	DeveloperSessionWaitingPermission = "waiting_permission"
	DeveloperSessionWaitingQuestion   = "waiting_question"
	DeveloperSessionCompleted         = "completed"
	DeveloperSessionFailed            = "failed"
	DeveloperSessionCancelled         = "cancelled"
)

var (
	ErrDeveloperSpaceNotFound = errors.New("developer space not found")
	ErrDeveloperSessionBusy   = errors.New("developer session is already running or waiting")

	developerMemoryPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?[bkmgBKMG]?$`)
	containerImagePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]*$`)
)

// ValidContainerImage accepts a Docker image reference (`debian:13.7-slim`,
// `ghcr.io/org/tool:1.2`, `repo@sha256:…`). It is deliberately strict: the
// value becomes a Dockerfile FROM line and a docker argument, so whitespace,
// a leading dash or any other syntax must never get through.
func ValidContainerImage(ref string) bool {
	return len(ref) <= 255 && containerImagePattern.MatchString(ref)
}

// DeveloperToolRule follows the same ordered, last-match-wins model used by
// mature coding agents while remaining native to AT.
type DeveloperToolRule struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
	Effect   string `json:"effect"`
}

type DeveloperAgentProfile struct {
	SystemPrompt       string              `json:"system_prompt,omitempty"`
	MaxIterations      int                 `json:"max_iterations,omitempty"`
	ToolTimeoutSeconds int                 `json:"tool_timeout_seconds,omitempty"`
	Rules              []DeveloperToolRule `json:"rules,omitempty"`
}

type DeveloperSpaceConfig struct {
	Plan   DeveloperAgentProfile `json:"plan,omitempty"`
	Build  DeveloperAgentProfile `json:"build,omitempty"`
	Review DeveloperAgentProfile `json:"review,omitempty"`
}

// DefaultDeveloperSpaceConfig is deliberately conservative. Plan and Review
// cannot edit; Build may edit but must ask before arbitrary shell execution,
// and every profile blocks pushes until a separate user-confirmed operation.
func DefaultDeveloperSpaceConfig() DeveloperSpaceConfig {
	inspect := []DeveloperToolRule{
		{Action: "read", Resource: "*", Effect: "allow"},
		{Action: "glob", Resource: "*", Effect: "allow"},
		{Action: "grep", Resource: "*", Effect: "allow"},
		{Action: "git", Resource: "status", Effect: "allow"},
		{Action: "git", Resource: "diff", Effect: "allow"},
		{Action: "edit", Resource: "*", Effect: "deny"},
		{Action: "shell", Resource: "*", Effect: "deny"},
		{Action: "git", Resource: "push", Effect: "deny"},
	}
	build := []DeveloperToolRule{
		{Action: "read", Resource: "*", Effect: "allow"},
		{Action: "glob", Resource: "*", Effect: "allow"},
		{Action: "grep", Resource: "*", Effect: "allow"},
		{Action: "edit", Resource: "*", Effect: "allow"},
		{Action: "git", Resource: "status", Effect: "allow"},
		{Action: "git", Resource: "diff", Effect: "allow"},
		{Action: "shell", Resource: "*", Effect: "ask"},
		{Action: "git", Resource: "push", Effect: "deny"},
	}
	return DeveloperSpaceConfig{
		Plan: DeveloperAgentProfile{
			SystemPrompt:       "Inspect the repository and produce a concrete implementation plan. Do not modify files. Name affected files, risks, and validation steps.",
			MaxIterations:      40,
			ToolTimeoutSeconds: 60,
			Rules:              inspect,
		},
		Build: DeveloperAgentProfile{
			SystemPrompt:       "Implement the requested change in the active worktree. Keep edits focused, run relevant validation, and finish with changed files, test results, and unresolved risks.",
			MaxIterations:      120,
			ToolTimeoutSeconds: 60,
			Rules:              build,
		},
		Review: DeveloperAgentProfile{
			SystemPrompt:       "Review the active worktree against its base. Do not modify files. Report correctness, security, and regression findings in severity order with file references.",
			MaxIterations:      60,
			ToolTimeoutSeconds: 60,
			Rules:              inspect,
		},
	}
}

type DeveloperSpace struct {
	ID             string               `json:"id"`
	WorkspaceID    string               `json:"workspace_id"`
	OwnerUserID    string               `json:"owner_user_id"`
	Name           string               `json:"name"`
	Status         string               `json:"status"`
	Image          string               `json:"image,omitempty"`
	CPULimit       string               `json:"cpu_limit,omitempty"`
	MemoryLimit    string               `json:"memory_limit,omitempty"`
	DiskLimitBytes int64                `json:"disk_limit_bytes,omitempty"`
	Config         DeveloperSpaceConfig `json:"config"`
	Error          string               `json:"error,omitempty"`
	LastActiveAt   string               `json:"last_active_at,omitempty"`
	CreatedAt      string               `json:"created_at"`
	UpdatedAt      string               `json:"updated_at"`
}

type DeveloperSession struct {
	ID          string `json:"id"`
	SpaceID     string `json:"space_id"`
	ProjectPath string `json:"project_path"`
	WorkspaceID string `json:"workspace_id"`
	OwnerUserID string `json:"owner_user_id"`
	Title       string `json:"title,omitempty"`
	Mode        string `json:"mode"`
	// AgentID runs the session as one of the workspace's agents: its system
	// prompt, skills, MCP sets, built-in tools and confirmation list. Empty
	// uses the built-in coding profile named by Mode. Provider/Model stay
	// per session, so the agent's model is only the starting choice.
	AgentID    string         `json:"agent_id,omitempty"`
	Status     string         `json:"status"`
	Provider   string         `json:"provider,omitempty"`
	Model      string         `json:"model,omitempty"`
	Config     map[string]any `json:"config,omitempty"`
	Error      string         `json:"error,omitempty"`
	StartedAt  string         `json:"started_at,omitempty"`
	FinishedAt string         `json:"finished_at,omitempty"`
	CreatedAt  string         `json:"created_at"`
	UpdatedAt  string         `json:"updated_at"`
}

// DeveloperSessionSettings are the per-session choices that may change
// between runs.
type DeveloperSessionSettings struct {
	Mode     string
	AgentID  string
	Provider string
	Model    string
}

type DeveloperSessionMessage struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	WorkspaceID string `json:"workspace_id"`
	OwnerUserID string `json:"owner_user_id"`
	Role        string `json:"role"`
	Content     any    `json:"content"`
	CreatedAt   string `json:"created_at"`
}

type DeveloperPendingTool struct {
	SessionID           string     `json:"session_id"`
	Kind                string     `json:"kind"`
	State               string     `json:"state"`
	ToolCalls           []ToolCall `json:"tool_calls"`
	TraceID             string     `json:"trace_id,omitempty"`
	ParentObservationID string     `json:"parent_observation_id,omitempty"`
	Step                int        `json:"step"`
	CreatedAt           string     `json:"created_at"`
}

type DeveloperSessionSnapshot struct {
	ID              string `json:"id"`
	SessionID       string `json:"session_id"`
	WorkspaceID     string `json:"workspace_id"`
	OwnerUserID     string `json:"owner_user_id"`
	Step            int    `json:"step"`
	Phase           string `json:"phase"`
	HeadSHA         string `json:"head_sha,omitempty"`
	StorageObjectID string `json:"storage_object_id,omitempty"`
	CreatedAt       string `json:"created_at"`
}

func (v DeveloperSpace) Validate() error {
	if v.DiskLimitBytes < 0 {
		return errors.New("disk_limit_bytes must not be negative")
	}
	if image := strings.TrimSpace(v.Image); image != "" && !ValidContainerImage(image) {
		return errors.New("image must be a Docker image reference such as debian:13.7-slim")
	}
	if cpu := strings.TrimSpace(v.CPULimit); cpu != "" {
		if value, err := strconv.ParseFloat(cpu, 64); err != nil || value <= 0 || value > 256 {
			return errors.New("cpu_limit must be a positive number of cores, e.g. 2 or 0.5")
		}
	}
	if memory := strings.TrimSpace(v.MemoryLimit); memory != "" && !developerMemoryPattern.MatchString(memory) {
		return errors.New("memory_limit must be a size such as 512m or 4g")
	}
	for name, profile := range map[string]DeveloperAgentProfile{"plan": v.Config.Plan, "build": v.Config.Build, "review": v.Config.Review} {
		if profile.MaxIterations < 0 {
			return errors.New(name + " max_iterations must not be negative")
		}
		if profile.ToolTimeoutSeconds < 0 || profile.ToolTimeoutSeconds > 3600 {
			return errors.New(name + " tool_timeout_seconds must be between 0 and 3600")
		}
		for _, rule := range profile.Rules {
			if rule.Action == "" || rule.Resource == "" {
				return errors.New(name + " tool rules require action and resource")
			}
			if rule.Effect != "allow" && rule.Effect != "ask" && rule.Effect != "deny" {
				return errors.New(name + " tool rule effect must be allow, ask, or deny")
			}
			if _, err := filepath.Match(rule.Resource, rule.Resource); err != nil {
				return errors.New(name + " tool rule resource pattern is invalid")
			}
		}
	}
	return nil
}

func ValidDeveloperMode(mode string) bool {
	return mode == DeveloperModePlan || mode == DeveloperModeBuild || mode == DeveloperModeReview
}

func (v DeveloperSession) Validate() error {
	if v.SpaceID == "" {
		return errors.New("space_id is required")
	}
	if _, err := CleanDeveloperPath(v.ProjectPath); err != nil {
		return fmt.Errorf("project_path: %w", err)
	}
	if strings.TrimSpace(v.Provider) == "" {
		return errors.New("provider is required")
	}
	if !ValidDeveloperMode(v.Mode) {
		return errors.New("mode must be plan, build, or review")
	}
	return nil
}

type DeveloperSpaceStorer interface {
	// EnsureDeveloperSpace returns the caller's single space in the selected
	// workspace, creating it with the default profiles on first use.
	EnsureDeveloperSpace(ctx context.Context) (*DeveloperSpace, error)
	GetDeveloperSpace(ctx context.Context, id string) (*DeveloperSpace, error)
	UpdateDeveloperSpace(ctx context.Context, space DeveloperSpace) (*DeveloperSpace, error)
	SetDeveloperSpaceRuntime(ctx context.Context, id, status, runtimeError string) (*DeveloperSpace, error)
	DeleteDeveloperSpace(ctx context.Context, id string) error

	ListDeveloperSessions(ctx context.Context, spaceID string) ([]DeveloperSession, error)
	RenameDeveloperSession(ctx context.Context, id, title string) (*DeveloperSession, error)
	UpdateDeveloperSessionSettings(ctx context.Context, id string, settings DeveloperSessionSettings) (*DeveloperSession, error)
	GetDeveloperSession(ctx context.Context, id string) (*DeveloperSession, error)
	CreateDeveloperSession(ctx context.Context, session DeveloperSession) (*DeveloperSession, error)
	SetDeveloperSessionRuntime(ctx context.Context, id, status, runtimeError string) (*DeveloperSession, error)
	BeginDeveloperSessionRun(ctx context.Context, id string) (*DeveloperSession, error)
	DeleteDeveloperSession(ctx context.Context, id string) error
	ListDeveloperSessionMessages(ctx context.Context, sessionID string) ([]DeveloperSessionMessage, error)
	AppendDeveloperSessionMessage(ctx context.Context, message DeveloperSessionMessage) (*DeveloperSessionMessage, error)
	ListDeveloperSessionSnapshots(ctx context.Context, sessionID string) ([]DeveloperSessionSnapshot, error)
	SaveDeveloperSessionSnapshot(ctx context.Context, snapshot DeveloperSessionSnapshot) (*DeveloperSessionSnapshot, error)
	GetDeveloperPendingTool(ctx context.Context, sessionID string) (*DeveloperPendingTool, error)
	SaveDeveloperPendingTool(ctx context.Context, pending DeveloperPendingTool) (*DeveloperPendingTool, error)
	DeleteDeveloperPendingTool(ctx context.Context, sessionID string) error
	ClaimDeveloperPendingTool(ctx context.Context, sessionID string) (*DeveloperPendingTool, error)
	ResolveDeveloperPendingTool(ctx context.Context, sessionID string, content []ContentBlock) (*DeveloperSessionMessage, error)
}

// DeveloperWorkspaceRoot is the persistent volume mount inside a space.
const DeveloperWorkspaceRoot = "/workspace"

// CleanDeveloperPath canonicalizes a path relative to the space root. The
// empty string is the root itself. Absolute paths, backslashes and parent
// segments are refused rather than resolved, so a typo cannot address a
// different location than the one displayed.
func CleanDeveloperPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, "/")
	if raw == "" || raw == "." {
		return "", nil
	}
	if strings.HasPrefix(raw, "/") || strings.ContainsRune(raw, '\\') || strings.ContainsRune(raw, 0) {
		return "", errors.New("path must be relative to the space")
	}
	for _, part := range strings.Split(raw, "/") {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("path contains an invalid segment")
		}
	}
	if len(raw) > 4096 {
		return "", errors.New("path is too long")
	}
	return raw, nil
}

// DeveloperAbsolutePath joins a cleaned relative path onto the space root.
func DeveloperAbsolutePath(rel string) string {
	if rel == "" {
		return DeveloperWorkspaceRoot
	}
	return path.Join(DeveloperWorkspaceRoot, rel)
}

// ValidDeveloperRemote accepts https, ssh, git and scp-style remotes without
// embedded credentials. Host reachability is checked separately at clone time.
func ValidDeveloperRemote(remote string) error {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return errors.New("remote URL is required")
	}
	if strings.HasPrefix(remote, "-") {
		return errors.New("remote URL is invalid")
	}
	if strings.HasPrefix(remote, "git@") && strings.Contains(remote, ":") {
		return nil
	}
	parsed, err := url.Parse(remote)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "ssh" && parsed.Scheme != "git") {
		return errors.New("remote URL must be an https, ssh, git, or git@host remote")
	}
	if parsed.User != nil {
		return errors.New("remote URL must not contain credentials")
	}
	return nil
}

// ValidDeveloperBranch applies Git's safety-relevant ref-name constraints
// before a value reaches checkout or push argv. Git remains the authority for
// less common repository-specific restrictions.
func ValidDeveloperBranch(branch string) bool {
	if branch == "" || branch == "@" || strings.HasPrefix(branch, "-") || strings.HasPrefix(branch, ".") ||
		strings.HasSuffix(branch, ".") || strings.HasSuffix(branch, "/") || strings.Contains(branch, "..") ||
		strings.Contains(branch, "@{") || strings.Contains(branch, "//") || strings.Contains(branch, "/.") ||
		strings.ContainsAny(branch, "\x00\r\n ~^:?*[\\") {
		return false
	}
	for _, part := range strings.Split(branch, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}
