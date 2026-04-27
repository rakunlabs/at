package common

import (
	"encoding/json"
	"fmt"

	"github.com/rakunlabs/at/internal/service"
)

// charsPerToken is a coarse estimate. Modern Claude/GPT/Gemini tokenizers
// average ~3.5–4.5 characters per token for English prose. We round to 4
// because:
//   - The estimate is only used to weight a token-bucket. Slight under- or
//     over-estimation just means the limiter is a bit looser or tighter,
//     not that calls fail.
//   - Tools/JSON are denser than prose and skew the per-token ratio lower
//     (more tokens per char), so erring on the side of "more tokens"
//     prevents under-counting on tool-heavy calls.
const charsPerToken = 4

// EstimateInputTokens returns an approximate input-token count for the
// given system prompt, message history, and tool definitions. It is
// intentionally cheap (no tokenizer dependency) and is meant to feed
// the per-provider rate limiter's ITPM bucket — exact accuracy is not
// required.
//
// Pass an empty string / nil slices for components you don't want
// counted.
func EstimateInputTokens(system string, messages []service.Message, tools []service.Tool) int {
	n := charLen(system)

	for _, m := range messages {
		switch c := m.Content.(type) {
		case string:
			n += len(c)
		case []byte:
			n += len(c)
		case []service.ContentBlock:
			for _, b := range c {
				n += len(b.Text)
				n += len(b.Content)
				if len(b.Input) > 0 {
					if buf, err := json.Marshal(b.Input); err == nil {
						n += len(buf)
					}
				}
				if b.Source != nil {
					// Media data is heavy; count its length but cap to
					// avoid one giant base64 image dominating the bucket.
					if l := len(b.Source.Data); l > 0 {
						if l > 4096 {
							l = 4096
						}
						n += l
					}
				}
			}
		default:
			// Fall back to a string render for unknown payloads (e.g. raw
			// gateway pass-through messages stored as map[string]any).
			n += len(fmt.Sprint(c))
		}
		// Add small per-message structural overhead.
		n += 8
	}

	for _, t := range tools {
		n += len(t.Name) + len(t.Description)
		if t.InputSchema != nil {
			if buf, err := json.Marshal(t.InputSchema); err == nil {
				n += len(buf)
			}
		}
	}

	return n / charsPerToken
}

func charLen(s string) int { return len(s) }
