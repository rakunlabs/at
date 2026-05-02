// Package loopgov implements the loop governor: a single point of policy
// for the three agentic loops in AT (org delegation, chat session,
// workflow agent_call node). It enforces:
//
//   - A sliding window on the message history sent to provider.Chat,
//     with rolling-summary fallback when the budget is exceeded.
//   - A hard ceiling on iteration count regardless of agent or task config.
//   - A single byte cap on tool-result payloads appended to the message
//     history; the full payload is preserved on disk under the workspace
//     root so the LLM sees a marker pointing at the file.
//
// All limits are enforced uniformly across the three loops by routing
// every loop through Governor methods. Set `Disabled: true` for an
// emergency rollback.
//
// History note: an earlier revision of this package shipped per-tool
// byte caps (`ToolCapOverrides`) and per-class caps
// (`ToolCapClassDefaults`). Those were removed because they
// over-truncated structured tool outputs (e.g. video-generation tools
// returning JSON with task IDs / variants / URLs) and broke the video
// pipeline. We now use a single global cap; combined with the workspace
// dump, the LLM either gets the full payload inline or a precise file
// reference it can read on demand.
//
// Output-token caps were also removed: providers and agent configs
// already specify per-model limits, and forcing a single platform-wide
// `max_tokens` truncated structured outputs (e.g. the Script Writer's
// multi-scene JSON for a YouTube Short).
package loopgov

import "time"

// Config carries the platform-wide knobs that govern agentic loops.
// Zero on a numeric field means "use the built-in default" —
// Config.fillDefaults() applies them at New() time so the rest of the
// package can rely on positive values.
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

	// ToolResultMaxBytes is the byte cap for tool results appended to
	// message history. Above this, the head is kept inline and the full
	// payload is written to the workspace dump file so the LLM can read
	// it via file_read or bash_execute. 0 = use default.
	ToolResultMaxBytes int

	// ChatHistoryLimit caps the number of rows ListChatMessages returns
	// in the chat-session loop. 0 = use default.
	ChatHistoryLimit int

	// Disabled is the global rollback switch. When true every Governor
	// method is a pass-through.
	Disabled bool

	// WorkspaceRoot is the directory under which truncated tool-result
	// full payloads are written (subpath: .at-tool-output/<run-id>/).
	// When empty, tool dumps are skipped (the marker still appears).
	// Defaults to /tmp/at-tasks (matching defaultTaskWorkspaceBase in
	// the server package) so dumps land alongside per-task workspaces
	// and agents can file_read them without extra plumbing.
	WorkspaceRoot string
}

// Built-in defaults.
//
// `ToolResultMaxBytes` is generous on purpose: video-generation tools
// (FAL Veo, Sora, Runway), image generators, TTS providers, and the
// `delegate_to_*` channel routinely return 10–60 KB of structured JSON
// (task IDs, asset URLs, scene metadata, voiceover URLs). Truncating
// that mid-JSON breaks downstream parsing. Combined with the workspace
// dump, we keep ample inline context AND preserve everything on disk.
const (
	DefaultWindowTokens       = 32 * 1024
	DefaultSummaryTokens      = 2000
	DefaultSummaryTimeout     = 10 * time.Second
	DefaultMaxIterCeiling     = 60
	DefaultToolResultMaxBytes = 64 * 1024
	DefaultChatHistoryLimit   = 200
	DefaultWorkspaceRoot      = "/tmp/at-tasks"
)

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
	if c.ToolResultMaxBytes <= 0 {
		c.ToolResultMaxBytes = DefaultToolResultMaxBytes
	}
	if c.ChatHistoryLimit <= 0 {
		c.ChatHistoryLimit = DefaultChatHistoryLimit
	}
	if c.WorkspaceRoot == "" {
		c.WorkspaceRoot = DefaultWorkspaceRoot
	}
}
