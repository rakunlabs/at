package loopgov

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// fakeSummarizer captures inputs and produces a fixed summary string.
type fakeSummarizer struct {
	called   int
	dropped  int
	output   string
	err      error
	delay    time.Duration
	systemIn string
}

func (s *fakeSummarizer) Summarize(ctx context.Context, system string, dropped []service.Message, maxTokens int) (string, error) {
	s.called++
	s.dropped = len(dropped)
	s.systemIn = system
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if s.err != nil {
		return "", s.err
	}
	return s.output, nil
}

func TestClampIterations(t *testing.T) {
	g := New(Config{MaxIterCeiling: 30}, nil)
	tests := []struct {
		name            string
		agent, task     int
		want            int
		expectClamped   bool
		disabled        bool
		ceilingOverride int
	}{
		{name: "agent only", agent: 12, task: 0, want: 12},
		{name: "task wins over agent", agent: 5, task: 20, want: 20},
		{name: "fallback when both zero", agent: 0, task: 0, want: 10},
		{name: "above ceiling clamped", agent: 100, task: 0, want: 30, expectClamped: true},
		{name: "task above ceiling clamped", agent: 5, task: 80, want: 30, expectClamped: true},
		{name: "below ceiling kept", agent: 25, task: 0, want: 25},
		{name: "disabled returns raw", agent: 100, task: 0, want: 100, disabled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gov := g
			if tt.disabled {
				gov = New(Config{Disabled: true}, nil)
			}
			got := gov.ClampIterations(tt.agent, tt.task)
			if got != tt.want {
				t.Fatalf("got %d want %d", got, tt.want)
			}
		})
	}
}

func TestLimitNoWindowingWhenUnderBudget(t *testing.T) {
	sum := &fakeSummarizer{output: "irrelevant"}
	g := New(Config{WindowTokens: 10_000}, sum)
	msgs := []service.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "world"},
	}
	got, err := g.Limit(context.Background(), "a1", "t1", msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(msgs) {
		t.Fatalf("expected pass-through, got %d messages", len(got))
	}
	if sum.called != 0 {
		t.Fatalf("summarizer should not be called when under budget; called=%d", sum.called)
	}
}

func TestLimitDisabledModeIsPassThrough(t *testing.T) {
	sum := &fakeSummarizer{output: "x"}
	g := New(Config{Disabled: true, WindowTokens: 10}, sum)
	bigText := strings.Repeat("a", 50_000)
	msgs := []service.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: bigText},
		{Role: "assistant", Content: bigText},
	}
	got, err := g.Limit(context.Background(), "a", "t", msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(msgs) {
		t.Fatalf("expected pass-through under disabled, got %d", len(got))
	}
	if sum.called != 0 {
		t.Fatal("summarizer should not run when disabled")
	}
	if g.MaxOutputTokens() != 0 {
		t.Fatal("disabled MaxOutputTokens should be 0")
	}
	if g.ChatHistoryLimit() != 0 {
		t.Fatal("disabled ChatHistoryLimit should be 0")
	}
}

func TestLimitProducesSummaryWhenOverBudget(t *testing.T) {
	sum := &fakeSummarizer{output: "rolling summary text"}
	g := New(Config{
		WindowTokens:  500,
		SummaryTokens: 100,
	}, sum)

	// Build a long conversation: ~10K chars per message → 2.5K tokens each.
	bigText := strings.Repeat("xyz ", 2_500) // 10K chars
	msgs := []service.Message{
		{Role: "system", Content: "sys"},
	}
	for i := 0; i < 10; i++ {
		msgs = append(msgs,
			service.Message{Role: "user", Content: bigText},
			service.Message{Role: "assistant", Content: bigText},
		)
	}
	got, err := g.Limit(context.Background(), "agentA", "taskB", msgs)
	if err != nil {
		t.Fatal(err)
	}
	if sum.called != 1 {
		t.Fatalf("expected one summarisation call, got %d", sum.called)
	}
	if len(got) >= len(msgs) {
		t.Fatalf("expected windowing to drop messages, got %d (input %d)", len(got), len(msgs))
	}
	// Layout: [system, summary_user, ...tail]
	if got[0].Role != "system" {
		t.Fatalf("system prompt must be preserved at index 0")
	}
	if !strings.Contains(stringContent(got[1]), "rolling summary text") {
		t.Fatalf("summary message missing or malformed: %v", got[1])
	}
	if !strings.Contains(stringContent(got[1]), "[CONVERSATION_SUMMARY]") {
		t.Fatalf("summary message must carry the [CONVERSATION_SUMMARY] tag")
	}
}

func TestLimitSummarizationFailureFallsBackToDrop(t *testing.T) {
	sum := &fakeSummarizer{err: errors.New("model down")}
	g := New(Config{WindowTokens: 500, SummaryTokens: 100}, sum)
	bigText := strings.Repeat("xyz ", 2_500)
	msgs := []service.Message{
		{Role: "system", Content: "sys"},
	}
	for i := 0; i < 10; i++ {
		msgs = append(msgs,
			service.Message{Role: "user", Content: bigText},
			service.Message{Role: "assistant", Content: bigText},
		)
	}
	got, err := g.Limit(context.Background(), "a", "t", msgs)
	if err != nil {
		t.Fatal(err)
	}
	if sum.called != 1 {
		t.Fatal("summarizer should be invoked once even on failure")
	}
	// No summary message should be inserted; just system + tail.
	if got[0].Role != "system" {
		t.Fatal("system must remain")
	}
	for _, m := range got[1:] {
		if strings.Contains(stringContent(m), "[CONVERSATION_SUMMARY]") {
			t.Fatal("summary must NOT be present on summarizer error")
		}
	}
	if len(got) >= len(msgs) {
		t.Fatal("dropping must still occur on summarizer failure")
	}
}

func TestLimitSummarizerTimeoutUsesDrop(t *testing.T) {
	sum := &fakeSummarizer{output: "x", delay: 50 * time.Millisecond}
	g := New(Config{
		WindowTokens:   500,
		SummaryTokens:  100,
		SummaryTimeout: 5 * time.Millisecond,
	}, sum)
	bigText := strings.Repeat("xyz ", 2_500)
	msgs := []service.Message{
		{Role: "system", Content: "sys"},
	}
	for i := 0; i < 10; i++ {
		msgs = append(msgs,
			service.Message{Role: "user", Content: bigText},
			service.Message{Role: "assistant", Content: bigText},
		)
	}
	got, err := g.Limit(context.Background(), "a", "t", msgs)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got[1:] {
		if strings.Contains(stringContent(m), "[CONVERSATION_SUMMARY]") {
			t.Fatal("summary must NOT be present on timeout")
		}
	}
}

func TestEstimateTokens(t *testing.T) {
	cases := map[string]int{
		"":                       0,
		"a":                      1,
		"abcd":                   1,
		"abcde":                  1, // 5/4 = 1
		strings.Repeat("a", 400): 100,
	}
	for in, want := range cases {
		if got := estimateTokens(in); got != want {
			t.Errorf("estimateTokens(%q): got %d want %d", in[:min(20, len(in))], got, want)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
