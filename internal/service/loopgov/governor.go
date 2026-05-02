package loopgov

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/rakunlabs/at/internal/service"
)

// Summarizer produces a bounded rolling summary of the messages folded
// into the window. It is provided externally so loopgov stays free of
// provider dependencies — the summarisation call is just another LLM
// chat completion using whatever provider/model the caller chose.
//
// In practice we ship with summarizer = nil and let `Limit` drop the
// oldest messages when the window overflows. Summarisation is an extra
// LLM round-trip that costs more than it saves for the agentic-loop
// access patterns we see in production (single-task videos, chat turns).
// It is wired up as an opt-in for callers that want it.
type Summarizer interface {
	Summarize(ctx context.Context, system string, dropped []service.Message, maxTokens int) (string, error)
}

// Governor is the central enforcement point for agentic-loop policy.
// One Governor is constructed at server start and shared by every loop.
// Methods are safe for concurrent use; the only mutable state is an
// atomic dump-sequence counter and a per-runID seq map.
type Governor struct {
	cfg        Config
	summarizer Summarizer

	// dumpSeq tracks the next monotonic sequence number to use for
	// tool-output dump filenames within a given run id. Writes are
	// rare relative to reads so a single mutex is fine.
	dumpSeqMu sync.Mutex
	dumpSeq   map[string]*atomic.Int64
}

// New constructs a Governor. summarizer may be nil; when nil and the
// window budget is exceeded the governor falls back to dropping oldest
// messages without producing a summary (the common case in production).
func New(cfg Config, summarizer Summarizer) *Governor {
	cfg.fillDefaults()
	g := &Governor{
		cfg:        cfg,
		summarizer: summarizer,
		dumpSeq:    map[string]*atomic.Int64{},
	}
	if cfg.Disabled {
		slog.Warn("loopgov.disabled — governor pass-through mode active; no limits enforced")
	}
	return g
}

// Disabled reports whether the governor is in pass-through mode.
func (g *Governor) Disabled() bool { return g.cfg.Disabled }

// Config returns the resolved configuration (with defaults applied).
// Useful for tests and for callers that need the chat history limit.
func (g *Governor) Config() Config { return g.cfg }

// ChatOptions used to return a per-call MaxTokens cap. We removed the
// platform-wide output-token cap because it broke structured outputs
// (e.g. multi-scene Script Writer JSON for video shorts). Per-model
// limits already live in the provider/agent config; the governor stays
// out of the way. Returning nil is the documented "no cap" sentinel
// for every provider adapter.
func (g *Governor) ChatOptions() *service.ChatOptions { return nil }

// ChatHistoryLimit returns the row cap for ListChatMessages. Returns 0
// when disabled, meaning "no limit" — callers should pass 0 through to
// the storer to preserve the legacy unbounded behaviour.
func (g *Governor) ChatHistoryLimit() int {
	if g.cfg.Disabled {
		return 0
	}
	return g.cfg.ChatHistoryLimit
}

// ClampIterations applies the platform iteration ceiling. Inputs are
// the agent's MaxIterations (per-agent default) and the task's
// MaxIterations (per-task override). Either or both may be ≤ 0 meaning
// "use the next-tier default". The result is always > 0.
//
// Resolution order:
//  1. taskMax > 0 wins
//  2. agentMax > 0 falls back
//  3. legacy fallback of 10
//
// Then: clamp the result to MaxIterCeiling. When clamped, a structured
// log event is emitted so operators can observe runaway configs.
func (g *Governor) ClampIterations(agentMax, taskMax int) int {
	requested := taskMax
	if requested <= 0 {
		requested = agentMax
	}
	if requested <= 0 {
		requested = 10
	}
	if g.cfg.Disabled {
		return requested
	}
	if requested > g.cfg.MaxIterCeiling {
		slog.Warn("loopgov.iter_clamped",
			"requested", requested,
			"effective", g.cfg.MaxIterCeiling)
		return g.cfg.MaxIterCeiling
	}
	return requested
}

// Limit produces the windowed message slice that should be passed to
// provider.Chat. Inputs:
//
//	ctx       — caller context; honoured for summarisation timeout
//	agentID   — for log attribution
//	taskID    — for log attribution
//	messages  — the full conversation including system prompt at index 0
//
// Behaviour:
//   - Disabled mode: returns messages unchanged.
//   - When the estimated input-token budget is satisfied: returns
//     messages unchanged.
//   - Otherwise: reserves the system prompt at index 0, finds the
//     largest suffix of trailing messages that fits in the remaining
//     budget, and replaces the dropped middle with one rolling-summary
//     user message (when a Summarizer is configured) or simply drops
//     them (when not).
//
// Returns an error only on context cancellation; summarisation failures
// are logged and degrade gracefully to dropping.
func (g *Governor) Limit(ctx context.Context, agentID, taskID string, messages []service.Message) ([]service.Message, error) {
	if g.cfg.Disabled || len(messages) == 0 {
		return messages, nil
	}

	totalEst := estimateMessages(messages)
	if totalEst <= g.cfg.WindowTokens {
		// Even when no windowing is needed, the caller's slice may
		// already contain orphan tool_use / tool_result blocks (e.g.
		// from an interrupted previous run, manual edit, or a bug
		// upstream). Repairing here protects every provider with one
		// pass instead of relying on per-provider wire-level repair.
		return RepairToolPairs(messages), nil
	}

	// Always preserve the system prompt at index 0 if present. We treat
	// any role==system message at the head specially: it is reserved.
	systemIdx := -1
	systemEst := 0
	if messages[0].Role == "system" {
		systemIdx = 0
		systemEst = estimateMessage(messages[0])
	}

	// Reserve room for the eventual rolling-summary user message. We
	// don't know the exact size until summarisation runs; budget the
	// upper bound (SummaryTokens worth of chars).
	reserved := systemEst + g.cfg.SummaryTokens

	// Walk from the tail forward, accumulating the suffix that fits in
	// the remaining budget. We always keep at least the most recent
	// message even if it overshoots — otherwise the loop has nothing
	// to send.
	keepStart := len(messages)
	used := 0
	for i := len(messages) - 1; i > systemIdx; i-- {
		est := estimateMessage(messages[i])
		// Always include the last message even if it would overshoot.
		if i == len(messages)-1 {
			keepStart = i
			used = est
			continue
		}
		if reserved+used+est > g.cfg.WindowTokens {
			break
		}
		used += est
		keepStart = i
	}

	// If keepStart is at the boundary right after system there is
	// nothing to fold — just return the original (this means a single
	// recent message overshoots even alone; the LLM will reject it on
	// its own and there's nothing useful for us to do here).
	dropStart := systemIdx + 1
	if keepStart <= dropStart {
		return messages, nil
	}

	dropped := messages[dropStart:keepStart]
	tail := messages[keepStart:]

	// Build the summary message. Try the configured summarizer first;
	// on any failure or timeout, fall back to "drop without summary".
	var summary string
	if g.summarizer != nil {
		sumCtx, cancel := context.WithTimeout(ctx, g.cfg.SummaryTimeout)
		var systemPrompt string
		if systemIdx == 0 {
			systemPrompt = stringContent(messages[systemIdx])
		}
		s, err := g.summarizer.Summarize(sumCtx, systemPrompt, dropped, g.cfg.SummaryTokens)
		cancel()
		if err != nil {
			slog.Warn("loopgov.summarize_failed",
				"agent_id", agentID,
				"task_id", taskID,
				"dropped", len(dropped),
				"error", err.Error())
		} else {
			summary = s
		}
	}

	out := make([]service.Message, 0, 1+1+len(tail))
	if systemIdx == 0 {
		out = append(out, messages[systemIdx])
	}
	if summary != "" {
		out = append(out, service.Message{
			Role:    "user",
			Content: "[CONVERSATION_SUMMARY] " + summary,
		})
		slog.Info("loopgov.summarized",
			"agent_id", agentID,
			"task_id", taskID,
			"dropped", len(dropped),
			"kept", len(tail),
			"summary_chars", len(summary))
	} else {
		slog.Info("loopgov.dropped",
			"agent_id", agentID,
			"task_id", taskID,
			"dropped", len(dropped),
			"kept", len(tail))
	}
	out = append(out, tail...)

	// CRITICAL: the contiguous middle-drop above is unaware of the
	// tool_use ↔ tool_result pairing constraint. The cut can land
	// between an assistant tool_use message and its matching
	// tool_result, leaving an orphan on either side. Every LLM
	// provider rejects that ("tool id (call_xxxx) not found" on
	// OpenAI; "tool_result block ... does not refer to a preceding
	// tool_use" on Anthropic). Repair the kept slice here so the
	// invariant holds regardless of which provider receives it.
	return RepairToolPairs(out), nil
}

// stringContent returns the textual content of a message for
// summarisation prompts. Handles both string and []ContentBlock.
func stringContent(m service.Message) string {
	switch v := m.Content.(type) {
	case string:
		return v
	case []service.ContentBlock:
		var b []byte
		for _, blk := range v {
			if blk.Text != "" {
				b = append(b, blk.Text...)
				b = append(b, '\n')
			} else if blk.Content != "" {
				b = append(b, blk.Content...)
				b = append(b, '\n')
			}
		}
		return string(b)
	}
	return ""
}

// estimateMessages returns the rough input-token estimate for a slice.
func estimateMessages(messages []service.Message) int {
	total := 0
	for _, m := range messages {
		total += estimateMessage(m)
	}
	return total
}

// estimateMessage returns the rough input-token estimate for one
// message, summing across all content blocks. We use len(s)/4 as a
// stand-in for a real tokeniser — provider-exact counts are not
// available pre-flight and ~4 chars/token is a robust over-estimate
// for English/code.
func estimateMessage(m service.Message) int {
	switch v := m.Content.(type) {
	case string:
		return estimateTokens(v)
	case []service.ContentBlock:
		total := 0
		for _, blk := range v {
			total += estimateTokens(blk.Text)
			total += estimateTokens(blk.Content)
			// Tool args / inputs add bytes too — JSON-marshal-ish.
			for k, val := range blk.Input {
				total += estimateTokens(k)
				if s, ok := val.(string); ok {
					total += estimateTokens(s)
				}
			}
		}
		return total
	}
	return 0
}

// estimateTokens applies the 4-char-per-token heuristic. A floor of 1
// is applied when s is non-empty to avoid undercounting tiny strings.
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	t := len(s) / 4
	if t < 1 {
		return 1
	}
	return t
}
