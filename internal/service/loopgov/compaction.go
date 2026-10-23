package loopgov

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// Compactor summarises messages that leave the context window. previous is
// the summary produced by the last compaction of the same run ("" the first
// time); the result replaces it, so summaries are updated incrementally and
// each compaction only reads the newly evicted messages.
type Compactor interface {
	Compact(ctx context.Context, system, previous string, evicted []service.Message, maxTokens int) (string, error)
}

// CompactionResult reports what LimitCompacting did for one call.
type CompactionResult struct {
	// Compacted is true when a summarisation call ran.
	Compacted bool
	// Summarized is true when the returned messages carry a summary,
	// including one reused from an earlier call.
	Summarized bool
	// Evicted is the number of messages folded into the summary by this call.
	Evicted int
}

// compactionState is the cached summary of a run's message prefix. hashes
// fingerprints each message the summary stands for, so a caller whose
// reloaded history starts later (a sliding history limit) still matches,
// while one whose history changed underneath it does not.
type compactionState struct {
	hashes  []uint64
	summary string
	used    time.Time
}

const (
	compactionCacheMax = 2048
	compactionCacheTTL = 6 * time.Hour
	// Several runs may share one key; each keeps its own summary.
	compactionStatesPerKey = 8
	compactionTag          = "[CONVERSATION_SUMMARY]"
)

// LimitCompacting windows messages like LimitWithTools, but folds evicted
// history into a summary written by compactor instead of dropping it.
//
// budget overrides WindowTokens when positive; overflow recovery uses it to
// retry a request the provider rejected as too long. When compaction is not
// possible (no compactor, nothing to fold, or the summarisation call failed)
// it degrades to the dropping behaviour of LimitWithTools.
func (g *Governor) LimitCompacting(ctx context.Context, agentID, taskID string, messages []service.Message, tools []service.Tool, compactor Compactor, budget int) ([]service.Message, CompactionResult, error) {
	var result CompactionResult
	if g.cfg.Disabled || len(messages) == 0 {
		return messages, result, nil
	}
	if budget <= 0 || budget > g.cfg.WindowTokens {
		budget = g.cfg.WindowTokens
	}
	toolEst := estimateTools(tools)
	if compactor == nil {
		out, err := g.limitBudget(ctx, agentID, taskID, messages, toolEst, budget)
		return out, result, err
	}

	var system []service.Message
	body := messages
	if messages[0].Role == "system" {
		system = messages[:1]
		body = messages[1:]
	}
	systemPrompt := ""
	if len(system) > 0 {
		systemPrompt = stringContent(system[0])
	}

	key := agentID + "\x00" + taskID
	hashes := messageHashes(body)
	covered, summary := g.compactionLookup(key, hashes)
	view := compactedView(system, summary, body[covered:])
	if summary != "" {
		result.Summarized = true
	}
	if estimateMessages(view)+toolEst <= budget {
		return RepairToolPairs(view), result, nil
	}

	// Keep about half of the room left after fixed costs as a verbatim tail,
	// so the next iterations fit without another summarisation call.
	fixed := estimateMessages(system) + toolEst + g.cfg.CompactionSummaryTokens
	keep := (budget - fixed) / 2
	if keep < 0 {
		keep = 0
	}
	keepStart := len(body)
	used := 0
	for i := len(body) - 1; i >= covered; i-- {
		est := estimateMessage(body[i])
		if i < len(body)-1 && used+est > keep {
			break
		}
		used += est
		keepStart = i
	}
	keepStart = alignToolBoundary(body, keepStart, covered)
	if keepStart <= covered {
		out, err := g.limitBudget(ctx, agentID, taskID, view, toolEst, budget)
		return out, result, err
	}

	evicted := body[covered:keepStart]
	sumCtx, cancel := context.WithTimeout(ctx, g.cfg.CompactionTimeout)
	next, err := compactor.Compact(sumCtx, systemPrompt, summary, evicted, g.cfg.CompactionSummaryTokens)
	cancel()
	next = strings.TrimSpace(next)
	if err != nil || next == "" {
		if ctx.Err() != nil {
			return nil, result, ctx.Err()
		}
		reason := "empty summary"
		if err != nil {
			reason = err.Error()
		}
		slog.Warn("loopgov.compaction_failed",
			"agent_id", agentID, "task_id", taskID,
			"evicted", len(evicted), "error", reason)
		out, limitErr := g.limitBudget(ctx, agentID, taskID, view, toolEst, budget)
		return out, result, limitErr
	}

	g.compactionStore(key, &compactionState{hashes: hashes[:keepStart:keepStart], summary: next})
	result.Compacted, result.Summarized, result.Evicted = true, true, len(evicted)
	slog.Info("loopgov.compacted",
		"agent_id", agentID, "task_id", taskID,
		"evicted", len(evicted), "kept", len(body)-keepStart,
		"summary_chars", len(next), "incremental", summary != "")

	view = compactedView(system, next, body[keepStart:])
	if estimateMessages(view)+toolEst > budget {
		// A single oversized recent message: keep the summary and fall back
		// to evicting tool output / oldest text from the tail.
		out, err := g.limitBudget(ctx, agentID, taskID, view, toolEst, budget)
		return out, result, err
	}
	return RepairToolPairs(view), result, nil
}

// limitBudget is the dropping window with an explicit budget. It never calls
// the configured summarizer; compaction has already had its chance.
func (g *Governor) limitBudget(ctx context.Context, agentID, taskID string, messages []service.Message, toolEst, budget int) ([]service.Message, error) {
	return g.limit(ctx, agentID, taskID, messages, toolEst, budget, nil)
}

func compactedView(system []service.Message, summary string, tail []service.Message) []service.Message {
	out := make([]service.Message, 0, len(system)+1+len(tail))
	out = append(out, system...)
	if summary != "" {
		out = append(out, service.Message{
			Role: "user",
			Content: compactionTag + " Earlier conversation was compacted to fit the context window. " +
				"This summary replaces it; the messages after it are verbatim.\n\n" + summary,
		})
	}
	return append(out, tail...)
}

// alignToolBoundary moves a cut so the kept tail does not start with tool
// results whose calls were evicted: the call message is kept with them.
func alignToolBoundary(body []service.Message, keepStart, floor int) int {
	for keepStart > floor && keepStart < len(body) && hasToolResult(body[keepStart]) {
		keepStart--
	}
	return keepStart
}

func hasToolResult(m service.Message) bool {
	if m.Role != "user" {
		return false
	}
	blocks, ok := m.Content.([]service.ContentBlock)
	if !ok {
		return false
	}
	for _, b := range blocks {
		if b.Type == "tool_result" {
			return true
		}
	}
	return false
}

func messageHashes(messages []service.Message) []uint64 {
	out := make([]uint64, len(messages))
	for i, m := range messages {
		h := fnv.New64a()
		_ = json.NewEncoder(h).Encode(m)
		out[i] = h.Sum64()
	}
	return out
}

// compactionLookup finds a cached summary whose covered messages match the
// start of body, allowing body to begin part-way through them. It returns
// how many leading messages of body the summary covers.
func (g *Governor) compactionLookup(key string, body []uint64) (int, string) {
	g.compactMu.Lock()
	defer g.compactMu.Unlock()
	best, summary := 0, ""
	var hit *compactionState
	for _, state := range g.compactions[key] {
		if covered, ok := coveredPrefix(state.hashes, body); ok && covered > best {
			best, summary, hit = covered, state.summary, state
		}
	}
	if hit != nil {
		hit.used = time.Now()
	}
	return best, summary
}

// coveredPrefix reports how much of body a summary covering stored stands
// for: body must start at some message of stored and agree with it to the end.
func coveredPrefix(stored, body []uint64) (int, bool) {
	if len(stored) == 0 || len(body) == 0 {
		return 0, false
	}
	for offset := 0; offset < len(stored); offset++ {
		if stored[offset] != body[0] {
			continue
		}
		covered := len(stored) - offset
		if covered > len(body) {
			continue
		}
		match := true
		for i := 0; i < covered; i++ {
			if stored[offset+i] != body[i] {
				match = false
				break
			}
		}
		if match {
			return covered, true
		}
	}
	return 0, false
}

// compactionStore records a summary. A key holds a few summaries because
// concurrent runs may share agent and task IDs (workflow nodes have none).
func (g *Governor) compactionStore(key string, state *compactionState) {
	now := time.Now()
	state.used = now
	g.compactMu.Lock()
	defer g.compactMu.Unlock()
	if g.compactions == nil {
		g.compactions = map[string][]*compactionState{}
	}
	total := 0
	for k, states := range g.compactions {
		kept := states[:0]
		for _, st := range states {
			if now.Sub(st.used) <= compactionCacheTTL {
				kept = append(kept, st)
			}
		}
		if len(kept) == 0 {
			delete(g.compactions, k)
			continue
		}
		g.compactions[k] = kept
		total += len(kept)
	}
	states := g.compactions[key]
	// A summary that extends an existing one replaces it.
	kept := states[:0]
	for _, st := range states {
		if _, ok := coveredPrefix(st.hashes, state.hashes); !ok {
			kept = append(kept, st)
		} else {
			total--
		}
	}
	if len(kept) >= compactionStatesPerKey {
		kept = kept[1:]
		total--
	}
	if total >= compactionCacheMax {
		// Drop the least recently used other key; a cache miss only costs
		// one extra summarisation call.
		oldestKey, oldest := "", now
		for k, sts := range g.compactions {
			if k == key {
				continue
			}
			for _, st := range sts {
				if st.used.Before(oldest) {
					oldestKey, oldest = k, st.used
				}
			}
		}
		delete(g.compactions, oldestKey)
	}
	g.compactions[key] = append(kept, state)
}

// LLMCompactor writes compaction summaries with a chat provider, normally the
// one the loop is already using, so no separate model needs configuring.
// Usage accumulates the tokens spent so callers can account for them.
type LLMCompactor struct {
	Provider service.LLMProvider
	Model    string
	// MaxInputTokens bounds the transcript sent for summarisation; the
	// newest evicted messages are kept. 0 = DefaultWindowTokens.
	MaxInputTokens int
	Usage          service.Usage
}

const compactorMessageChars = 8000

// Compact implements Compactor.
func (c *LLMCompactor) Compact(ctx context.Context, system, previous string, evicted []service.Message, maxTokens int) (string, error) {
	if c.Provider == nil {
		return "", fmt.Errorf("compactor: nil provider")
	}
	maxInput := c.MaxInputTokens
	if maxInput <= 0 {
		maxInput = DefaultWindowTokens
	}
	// Leave room for the instructions, system prompt and previous summary.
	transcript := compactionTranscript(evicted, maxInput*3*3/4)

	instr := "You maintain the working summary of a long agent conversation whose oldest messages are being removed from the context window. " +
		"Write a summary that lets the agent continue the work without the removed messages. " +
		"Use these sections, omitting empty ones: Objective; Requirements and constraints; Decisions; Completed work (with key results); " +
		"Active work; Blockers; Next steps; Important identifiers (exact file paths, IDs, URLs, names, numbers). " +
		"Copy identifiers exactly. Keep tool results only as the facts they established. " +
		fmt.Sprintf("Stay under %d tokens. Output plain text only.", maxTokens)
	if previous != "" {
		instr += " An existing summary is provided: update it with the new messages, keeping what is still relevant, rather than starting over."
	}

	messages := []service.Message{{Role: "system", Content: instr}}
	var user strings.Builder
	if system != "" {
		user.WriteString("Agent system prompt (context only, do not summarise it):\n")
		user.WriteString(truncateMiddle(system, compactorMessageChars))
		user.WriteString("\n\n")
	}
	if previous != "" {
		user.WriteString("Existing summary:\n")
		user.WriteString(previous)
		user.WriteString("\n\n")
	}
	user.WriteString("Messages being removed:\n")
	user.WriteString(transcript)
	messages = append(messages, service.Message{Role: "user", Content: user.String()})

	limit := maxTokens * 2
	resp, err := c.Provider.Chat(ctx, c.Model, messages, nil, &service.ChatOptions{MaxTokens: &limit})
	if err != nil {
		return "", err
	}
	c.Usage.PromptTokens += resp.Usage.PromptTokens
	c.Usage.CompletionTokens += resp.Usage.CompletionTokens
	c.Usage.CacheReadTokens += resp.Usage.CacheReadTokens
	c.Usage.CacheWriteTokens += resp.Usage.CacheWriteTokens
	c.Usage.ReasoningTokens += resp.Usage.ReasoningTokens
	c.Usage.TotalTokens += resp.Usage.TotalTokens
	return strings.TrimSpace(resp.Content), nil
}

// compactionTranscript renders evicted messages as text, including tool calls
// and their results, bounded per message and in total (newest kept).
func compactionTranscript(evicted []service.Message, maxChars int) string {
	parts := make([]string, 0, len(evicted))
	for _, m := range evicted {
		var b strings.Builder
		switch v := m.Content.(type) {
		case string:
			b.WriteString(v)
		case []service.ContentBlock:
			for _, blk := range v {
				switch blk.Type {
				case "tool_use":
					args, _ := json.Marshal(blk.Input)
					fmt.Fprintf(&b, "[tool call %s %s]\n", blk.Name, args)
				case "tool_result":
					fmt.Fprintf(&b, "[tool result] %s\n", blk.ContentText())
				case "thinking", "redacted_thinking":
				case "image":
					b.WriteString("[image]\n")
				case "document":
					b.WriteString("[document]\n")
				default:
					if blk.Text != "" {
						b.WriteString(blk.Text)
						b.WriteString("\n")
					}
				}
			}
		}
		text := strings.TrimSpace(b.String())
		if text == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("[%s] %s", m.Role, truncateMiddle(text, compactorMessageChars)))
	}
	total := 0
	start := len(parts)
	for start > 0 && total+len(parts[start-1]) <= maxChars {
		start--
		total += len(parts[start])
	}
	out := strings.Join(parts[start:], "\n\n")
	if start > 0 {
		out = fmt.Sprintf("[%d older messages omitted]\n\n", start) + out
	}
	return out
}

func truncateMiddle(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	head := limit * 2 / 3
	tail := limit - head
	return strings.ToValidUTF8(s[:head], "") + fmt.Sprintf("\n…[%d characters omitted]…\n", len(s)-limit) + strings.ToValidUTF8(s[len(s)-tail:], "")
}

// IsContextOverflowError reports whether a provider rejected a request
// because its input exceeds the model's context window. Wording differs per
// provider; byte-size limits on the HTTP body are not matched.
func IsContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"context_length_exceeded",
		"maximum context length",
		"context length exceeded",
		"exceeds the context window",
		"exceeds the model's context",
		"context window exceeded",
		"prompt is too long",
		"input is too long",
		"too many input tokens",
		"input token count",
		"maximum number of tokens",
		"reduce the length of the messages",
		"model_context_window_exceeded",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// CompactionInputTokens is the input budget for one compaction call: the
// same window the loop itself is held to.
func (g *Governor) CompactionInputTokens() int { return g.cfg.WindowTokens }

// OverflowBudget is the budget used to retry a request the provider rejected
// as too long: half of what was sent, never more than the window.
func (g *Governor) OverflowBudget(sent []service.Message, tools []service.Tool) int {
	budget := (estimateMessages(sent) + estimateTools(tools)) / 2
	if budget > g.cfg.WindowTokens/2 {
		budget = g.cfg.WindowTokens / 2
	}
	if budget < 1 {
		budget = 1
	}
	return budget
}
