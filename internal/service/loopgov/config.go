// Package loopgov implements the loop governor: a single point of policy
// for the three agentic loops in AT (org delegation, chat session,
// workflow agent_call node). It enforces:
//
//   - A sliding window on the message history sent to provider.Chat,
//     with rolling-summary fallback when the budget is exceeded.
//   - A hard ceiling on iteration count regardless of agent or task config.
//   - A bound on per-call output tokens (max_tokens).
//   - A per-tool byte cap on tool-result payloads appended to the
//     message history; the full payload is preserved on disk under
//     the workspace root so the LLM sees a marker pointing at the file.
//
// All limits are enforced uniformly across the three loops by routing
// every loop through Governor methods. A single env switch
// (LOOP_GOVERNOR_DISABLED) bypasses every limit for emergency rollback.
//
// See openspec/changes/agent-loop-context-controls/ for the design.
package loopgov

import "time"

// Config carries the platform-wide knobs that govern agentic loops.
// Values resolved from server config / env. Zero on a numeric field
// means "use the built-in default" — Config.fillDefaults() applies them
// at New() time so the rest of the package can rely on positive values.
type Config struct {
	// WindowTokens is the soft input-token budget the windowed message
	// slice is required to fit within. Older messages are dropped or
	// summarised when the budget would be exceeded. 0 = use default.
	WindowTokens int

	// SummaryTokens caps the size of the rolling-summary message that
	// replaces dropped history. 0 = use default.
	SummaryTokens int

	// SummaryTimeout bounds the LLM summarisation call. On timeout the
	// governor drops oldest messages without summary and logs.
	// 0 = use default.
	SummaryTimeout time.Duration

	// MaxIterCeiling is the platform's hard ceiling on agentic loop
	// iterations. 0 = use default. Per-agent / per-task max_iterations
	// are clamped to this value.
	MaxIterCeiling int

	// MaxOutputTokens is the value passed as ChatOptions.MaxTokens for
	// every provider call. 0 = use default.
	MaxOutputTokens int

	// ToolResultMaxBytes is the default byte cap for tool results
	// appended to message history. Per-tool overrides take precedence.
	// 0 = use default.
	ToolResultMaxBytes int

	// ToolCapOverrides maps tool name -> byte cap. Used for tools whose
	// outputs need a different ceiling (e.g. structured `task_get`).
	ToolCapOverrides map[string]int

	// ToolCapClassDefaults maps a class name -> default byte cap. The
	// governor's classifier picks the class for unknown tool names; see
	// classify(). Empty map → built-in defaults are used.
	ToolCapClassDefaults map[string]int

	// ChatHistoryLimit caps the number of rows ListChatMessages returns
	// in the chat-session loop. 0 = use default.
	ChatHistoryLimit int

	// Disabled is the global rollback switch. When true every Governor
	// method is a pass-through.
	Disabled bool

	// WorkspaceRoot is the directory under which truncated tool-result
	// full payloads are written (subpath: .at-tool-output/<run-id>/).
	// When empty, tool dumps are skipped (the marker still appears).
	WorkspaceRoot string
}

// Built-in defaults. These are tuned against the production data captured
// in 2026-04 (10M+ tokens/day with 47:1 input:output ratio) — the goal is
// to cap a typical agentic-loop task at well under 1M tokens without
// breaking the average task that legitimately needs 10–20 turns.
const (
	DefaultWindowTokens       = 32 * 1024
	DefaultSummaryTokens      = 2000
	DefaultSummaryTimeout     = 10 * time.Second
	DefaultMaxIterCeiling     = 30
	DefaultMaxOutputTokens    = 4096
	DefaultToolResultMaxBytes = 8 * 1024
	DefaultChatHistoryLimit   = 200
)

// Default per-class byte caps. Class is chosen by classify().
//
// "executable" — bash_execute, exec, http_request bodies, file_read, etc.
// "structured" — task_get, task_list and other JSON-shaped responses
// "freeform"   — fallback (skill JS, MCP, delegation results)
const (
	DefaultClassExecutableBytes = 8 * 1024
	DefaultClassStructuredBytes = 32 * 1024
	DefaultClassFreeformBytes   = 8 * 1024
)

// Default per-tool byte caps. These take precedence over class defaults
// for the listed tool names. Tuned against the production data captured
// in 2026-04 where Director-style head agents poll children with
// task_get / task_list 100+ times per task; full 32KB descriptions were
// re-fed into the LLM context on every poll, dominating input cost.
//
// 4KB is enough for status + identifier + first 3KB of result/title;
// the agent can call file_read (or bash_execute cat) on the workspace
// brief file when it needs the full payload.
var defaultPerToolCaps = map[string]int{
	"task_get":  4 * 1024,
	"task_list": 4 * 1024,
}

// fillDefaults rewrites a zero value to the built-in default. It is
// applied once in New() so callers don't have to special-case zero.
func (c *Config) fillDefaults() {
	if c.WindowTokens <= 0 {
		c.WindowTokens = DefaultWindowTokens
	}
	if c.SummaryTokens <= 0 {
		c.SummaryTokens = DefaultSummaryTokens
	}
	if c.SummaryTimeout <= 0 {
		c.SummaryTimeout = DefaultSummaryTimeout
	}
	if c.MaxIterCeiling <= 0 {
		c.MaxIterCeiling = DefaultMaxIterCeiling
	}
	if c.MaxOutputTokens <= 0 {
		c.MaxOutputTokens = DefaultMaxOutputTokens
	}
	if c.ToolResultMaxBytes <= 0 {
		c.ToolResultMaxBytes = DefaultToolResultMaxBytes
	}
	if c.ChatHistoryLimit <= 0 {
		c.ChatHistoryLimit = DefaultChatHistoryLimit
	}
	if c.ToolCapOverrides == nil {
		c.ToolCapOverrides = map[string]int{}
	}
	// Seed built-in per-tool caps for tools that empirically dominate
	// input cost (head-agent polling). Operator-supplied overrides win.
	for k, v := range defaultPerToolCaps {
		if _, set := c.ToolCapOverrides[k]; !set {
			c.ToolCapOverrides[k] = v
		}
	}
	if c.ToolCapClassDefaults == nil {
		c.ToolCapClassDefaults = map[string]int{
			"executable": DefaultClassExecutableBytes,
			"structured": DefaultClassStructuredBytes,
			"freeform":   DefaultClassFreeformBytes,
		}
	}
}

// classify returns a tool-class label for the given tool name. Used to
// resolve per-class default byte caps when no override is provided.
//
// The classifier is intentionally narrow — it errs on the side of
// "executable" (smallest cap) so an unknown tool defaults to the
// strictest setting. Operators can promote a name via ToolCapOverrides.
func classify(toolName string) string {
	switch toolName {
	// Bash/exec/file reads/HTTP — large outputs are common and rarely
	// useful in their entirety; aggressive truncation is correct.
	case "bash_execute", "exec", "file_read", "file_grep",
		"file_glob", "http_request", "url_fetch":
		return "executable"
	// JSON-shaped responses agents do tend to consume in full.
	case "task_get", "task_list", "agent_get", "agent_list",
		"workflow_get", "workflow_list", "org_get", "org_list",
		"goal_get", "project_get", "list_chat_messages":
		return "structured"
	}
	return "freeform"
}
