package loopgov

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

type fakeCompactor struct {
	calls    int
	previous []string
	evicted  []int
	err      error
}

func (c *fakeCompactor) Compact(_ context.Context, _ string, previous string, evicted []service.Message, _ int) (string, error) {
	c.calls++
	c.previous = append(c.previous, previous)
	c.evicted = append(c.evicted, len(evicted))
	if c.err != nil {
		return "", c.err
	}
	return "summary-" + strings.Repeat("x", c.calls), nil
}

func longConversation(n int) []service.Message {
	msgs := []service.Message{{Role: "system", Content: "sys"}}
	for i := 0; i < n; i++ {
		msgs = append(msgs,
			service.Message{Role: "user", Content: strings.Repeat("u", 300) + string(rune('a'+i%26))},
			service.Message{Role: "assistant", Content: strings.Repeat("a", 300) + string(rune('a'+i%26))},
		)
	}
	return msgs
}

func hasSummary(msgs []service.Message) bool {
	for _, m := range msgs {
		if strings.Contains(stringContent(m), compactionTag) {
			return true
		}
	}
	return false
}

func TestLimitCompactingUnderBudgetPassesThrough(t *testing.T) {
	g := New(Config{WindowTokens: 10_000}, nil)
	c := &fakeCompactor{}
	msgs := longConversation(2)
	got, res, err := g.LimitCompacting(context.Background(), "a", "t", msgs, nil, c, 0)
	if err != nil || res.Compacted || c.calls != 0 || len(got) != len(msgs) {
		t.Fatalf("expected pass-through: got %d msgs, res %+v, calls %d, err %v", len(got), res, c.calls, err)
	}
}

func TestLimitCompactingSummarizesAndReuses(t *testing.T) {
	g := New(Config{WindowTokens: 2000, CompactionSummaryTokens: 100}, nil)
	c := &fakeCompactor{}
	msgs := longConversation(20) // ~4000 tokens
	got, res, err := g.LimitCompacting(context.Background(), "a", "t", msgs, nil, c, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Compacted || c.calls != 1 || !hasSummary(got) {
		t.Fatalf("expected one compaction with a summary: res %+v calls %d", res, c.calls)
	}
	if got[0].Role != "system" || got[len(got)-1].Content != msgs[len(msgs)-1].Content {
		t.Fatal("system prompt and latest message must be kept")
	}
	if estimateMessages(got) > 2000 {
		t.Fatalf("compacted view exceeds budget: %d", estimateMessages(got))
	}

	// One more exchange fits in the room left: the summary is reused.
	msgs = append(msgs, service.Message{Role: "user", Content: "next"})
	got, res, err = g.LimitCompacting(context.Background(), "a", "t", msgs, nil, c, 0)
	if err != nil || res.Compacted || !res.Summarized || c.calls != 1 || !hasSummary(got) {
		t.Fatalf("expected cached summary reuse: res %+v calls %d err %v", res, c.calls, err)
	}

	// Growing past the budget again updates the existing summary.
	msgs = append(msgs, longConversation(20)[1:]...)
	_, res, err = g.LimitCompacting(context.Background(), "a", "t", msgs, nil, c, 0)
	if err != nil || !res.Compacted || c.calls != 2 {
		t.Fatalf("expected incremental compaction: res %+v calls %d err %v", res, c.calls, err)
	}
	if c.previous[1] != "summary-x" {
		t.Fatalf("second compaction must receive the previous summary, got %q", c.previous[1])
	}
}

func TestLimitCompactingSlidingHistoryReusesSummary(t *testing.T) {
	g := New(Config{WindowTokens: 2000, CompactionSummaryTokens: 100}, nil)
	c := &fakeCompactor{}
	msgs := longConversation(20)
	if _, _, err := g.LimitCompacting(context.Background(), "a", "t", msgs, nil, c, 0); err != nil {
		t.Fatal(err)
	}
	// A reload that starts two messages later still matches the summary.
	reloaded := append([]service.Message{msgs[0]}, msgs[3:]...)
	got, res, err := g.LimitCompacting(context.Background(), "a", "t", reloaded, nil, c, 0)
	if err != nil || c.calls != 1 || !res.Summarized || !hasSummary(got) {
		t.Fatalf("expected reuse after a sliding reload: res %+v calls %d err %v", res, c.calls, err)
	}
}

func TestLimitCompactingChangedHistoryDoesNotReuse(t *testing.T) {
	g := New(Config{WindowTokens: 2000, CompactionSummaryTokens: 100}, nil)
	c := &fakeCompactor{}
	if _, _, err := g.LimitCompacting(context.Background(), "a", "t", longConversation(20), nil, c, 0); err != nil {
		t.Fatal(err)
	}
	other := longConversation(20)
	other[1].Content = "a different conversation"
	if _, _, err := g.LimitCompacting(context.Background(), "a", "t", other, nil, c, 0); err != nil {
		t.Fatal(err)
	}
	if c.calls != 2 || c.previous[1] != "" {
		t.Fatalf("a different history must start a fresh summary: calls %d previous %q", c.calls, c.previous)
	}
}

func TestLimitCompactingFailureFallsBackToDrop(t *testing.T) {
	g := New(Config{WindowTokens: 2000}, nil)
	c := &fakeCompactor{err: errors.New("down")}
	msgs := longConversation(20)
	got, res, err := g.LimitCompacting(context.Background(), "a", "t", msgs, nil, c, 0)
	if err != nil || res.Compacted || hasSummary(got) || len(got) >= len(msgs) {
		t.Fatalf("expected drop fallback: %d msgs, res %+v, err %v", len(got), res, err)
	}
}

func TestLimitCompactingKeepsToolPairs(t *testing.T) {
	g := New(Config{WindowTokens: 1500, CompactionSummaryTokens: 100}, nil)
	c := &fakeCompactor{}
	msgs := []service.Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "go"}}
	for i := 0; i < 15; i++ {
		id := string(rune('a' + i))
		msgs = append(msgs,
			service.Message{Role: "assistant", Content: []service.ContentBlock{{Type: "tool_use", ID: id, Name: "t", Input: map[string]any{}}}},
			service.Message{Role: "user", Content: []service.ContentBlock{{Type: "tool_result", ToolUseID: id, Content: strings.Repeat("r", 600)}}},
		)
	}
	got, res, err := g.LimitCompacting(context.Background(), "a", "t", msgs, nil, c, 0)
	if err != nil || !res.Compacted {
		t.Fatalf("expected compaction: res %+v err %v", res, err)
	}
	if len(RepairToolPairs(got)) != len(got) {
		t.Fatal("compacted view must not contain orphan tool blocks")
	}
	for i, m := range got {
		if hasToolResult(m) && (i == 0 || got[i-1].Role != "assistant") {
			t.Fatalf("tool result at %d lost its call", i)
		}
	}
}

func TestLimitCompactingOverflowBudget(t *testing.T) {
	g := New(Config{WindowTokens: 100_000, CompactionSummaryTokens: 100}, nil)
	c := &fakeCompactor{}
	msgs := longConversation(20)
	budget := g.OverflowBudget(msgs, nil)
	got, res, err := g.LimitCompacting(context.Background(), "a", "t", msgs, nil, c, budget)
	if err != nil || !res.Compacted || estimateMessages(got) > budget {
		t.Fatalf("expected compaction to the overflow budget %d: got %d tokens, res %+v", budget, estimateMessages(got), res)
	}
}

func TestIsContextOverflowError(t *testing.T) {
	for _, msg := range []string{
		"This model's maximum context length is 128000 tokens",
		"anthropic: 400 prompt is too long: 210000 tokens > 200000 maximum",
		`{"error":{"code":"context_length_exceeded"}}`,
		"The input token count (1200000) exceeds the maximum number of tokens allowed",
	} {
		if !IsContextOverflowError(errors.New(msg)) {
			t.Errorf("expected overflow: %q", msg)
		}
	}
	for _, msg := range []string{"rate limit exceeded", "invalid api key", "request entity too large"} {
		if IsContextOverflowError(errors.New(msg)) {
			t.Errorf("unexpected overflow: %q", msg)
		}
	}
}
