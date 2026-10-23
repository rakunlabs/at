package agentloop

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/loopgov"
)

func bigHistory(n int) []service.Message {
	msgs := []service.Message{{Role: "system", Content: "sys"}}
	for i := 0; i < n; i++ {
		msgs = append(msgs,
			service.Message{Role: "user", Content: strings.Repeat("q", 600)},
			service.Message{Role: "assistant", Content: strings.Repeat("r", 600)},
		)
	}
	return append(msgs, service.Message{Role: "user", Content: "latest"})
}

// isSummaryRequest recognises the compactor's call (no tools, compaction
// instructions in the system prompt).
func isSummaryRequest(messages []service.Message) bool {
	s, _ := messages[0].Content.(string)
	return strings.Contains(s, "working summary")
}

func TestCallProviderCompactsWithSameModelAndChargesUsage(t *testing.T) {
	gov := loopgov.New(loopgov.Config{WindowTokens: 3000, CompactionSummaryTokens: 200}, nil)
	summaries, calls := 0, 0
	provider := stubProvider{chat: func(_ context.Context, model string, messages []service.Message, _ []service.Tool, _ *service.ChatOptions) (*service.LLMResponse, error) {
		if model != "m" {
			t.Fatalf("model = %q", model)
		}
		if isSummaryRequest(messages) {
			summaries++
			return &service.LLMResponse{Content: "the summary", Usage: service.Usage{PromptTokens: 100, CompletionTokens: 10}}, nil
		}
		calls++
		found := false
		for _, m := range messages {
			if s, ok := m.Content.(string); ok && strings.Contains(s, "the summary") {
				found = true
			}
		}
		if !found {
			t.Fatal("main call did not receive the summary")
		}
		return &service.LLMResponse{Content: "ok", Finished: true, Usage: service.Usage{PromptTokens: 5, CompletionTokens: 1}}, nil
	}}
	resp, _, _, err := CallProvider(context.Background(), gov, provider, "m", "agent", "run", bigHistory(20), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if summaries != 1 || calls != 1 {
		t.Fatalf("summaries=%d calls=%d", summaries, calls)
	}
	if resp.Usage.PromptTokens != 105 || resp.Usage.CompletionTokens != 11 {
		t.Fatalf("compaction usage not charged: %+v", resp.Usage)
	}
}

func TestCallProviderRecoversFromContextOverflow(t *testing.T) {
	gov := loopgov.New(loopgov.Config{WindowTokens: 1_000_000, CompactionSummaryTokens: 200}, nil)
	attempts := 0
	provider := stubProvider{chat: func(_ context.Context, _ string, messages []service.Message, _ []service.Tool, _ *service.ChatOptions) (*service.LLMResponse, error) {
		if isSummaryRequest(messages) {
			return &service.LLMResponse{Content: "the summary"}, nil
		}
		attempts++
		if attempts == 1 {
			return nil, errors.New("anthropic: 400 prompt is too long: 210000 tokens > 200000 maximum")
		}
		return &service.LLMResponse{Content: "ok", Finished: true}, nil
	}}
	full := bigHistory(20)
	resp, sent, _, err := CallProvider(context.Background(), gov, provider, "m", "agent", "run", full, nil, "")
	if err != nil || resp.Content != "ok" || attempts != 2 {
		t.Fatalf("resp=%v attempts=%d err=%v", resp, attempts, err)
	}
	if len(sent) >= len(full) {
		t.Fatalf("retry was not compacted: %d of %d messages", len(sent), len(full))
	}
}

func TestCallProviderOverflowRetriesOnce(t *testing.T) {
	gov := loopgov.New(loopgov.Config{CompactionSummaryTokens: 200}, nil)
	attempts := 0
	provider := stubProvider{chat: func(_ context.Context, _ string, messages []service.Message, _ []service.Tool, _ *service.ChatOptions) (*service.LLMResponse, error) {
		if isSummaryRequest(messages) {
			return &service.LLMResponse{Content: "s"}, nil
		}
		attempts++
		return nil, errors.New("context_length_exceeded")
	}}
	if _, _, _, err := CallProvider(context.Background(), gov, provider, "m", "a", "r", bigHistory(20), nil, ""); err == nil || attempts != 2 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}
