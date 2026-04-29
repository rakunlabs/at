package loopgov

import (
	"context"
	"fmt"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// LLMSummarizer is a Summarizer that uses a service.LLMProvider to
// produce the rolling summary. The same provider/model used by the
// agentic loop is fine — there's no requirement that summaries use a
// cheaper model. (We may add that knob later; see design.md decision 5.)
type LLMSummarizer struct {
	Provider service.LLMProvider
	Model    string
}

// Summarize implements Summarizer.
func (s *LLMSummarizer) Summarize(ctx context.Context, system string, dropped []service.Message, maxTokens int) (string, error) {
	if s.Provider == nil {
		return "", fmt.Errorf("summarizer: nil provider")
	}
	if len(dropped) == 0 {
		return "", nil
	}

	// Build a compact text representation of the dropped slice. Each
	// per-message segment is itself capped to keep the summarisation
	// prompt well within the model's context window even when the
	// dropped span is enormous.
	const perMsgChars = 2000
	var b strings.Builder
	for i, m := range dropped {
		text := stringContent(m)
		if len(text) > perMsgChars {
			text = text[:perMsgChars] + " …"
		}
		fmt.Fprintf(&b, "[%d %s] %s\n", i, m.Role, text)
	}

	instr := "You are summarising a multi-turn agent conversation that grew too long. " +
		"Produce a concise summary (≤ " + fmt.Sprintf("%d", maxTokens) + " tokens) that preserves: " +
		"(1) the user's goal, (2) decisions and conclusions reached, " +
		"(3) tool calls made and their key results, (4) any pending sub-tasks. " +
		"Omit pleasantries and verbatim tool output. Output plain text only."

	messages := []service.Message{
		{Role: "system", Content: instr},
	}
	if system != "" {
		messages = append(messages, service.Message{
			Role:    "user",
			Content: "Original system prompt (for context):\n" + system,
		})
	}
	messages = append(messages, service.Message{
		Role:    "user",
		Content: "Conversation to summarise:\n" + b.String(),
	})

	cap := maxTokens
	opts := &service.ChatOptions{MaxTokens: &cap}
	resp, err := s.Provider.Chat(ctx, s.Model, messages, nil, opts)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Content), nil
}
