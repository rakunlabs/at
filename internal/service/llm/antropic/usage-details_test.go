package antropic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestThinkingUsageDetails(t *testing.T) {
	const usage = `{"input_tokens":10,"output_tokens":100,"cache_creation_input_tokens":5,"cache_read_input_tokens":20,"output_tokens_details":{"thinking_tokens":80}}`
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"cache_creation_input_tokens\":5,\"cache_read_input_tokens\":20}}}\n\n")
					fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
					fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"answer\"}}\n\n")
					fmt.Fprintf(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":%s}\n\n", usage)
					fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
				} else {
					fmt.Fprintf(w, `{"content":[{"type":"text","text":"answer"}],"stop_reason":"end_turn","usage":%s}`, usage)
				}
			}))
			defer server.Close()
			p, err := New("test-key", "claude-opus-4-7", server.URL, "", false)
			if err != nil {
				t.Fatal(err)
			}
			var got service.Usage
			messages := []service.Message{{Role: "user", Content: "hello"}}
			if stream {
				chunks, _, err := p.ChatStream(context.Background(), "", messages, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				for chunk := range chunks {
					if chunk.Error != nil {
						t.Fatal(chunk.Error)
					}
					if chunk.Usage != nil {
						got = *chunk.Usage
					}
				}
			} else {
				resp, err := p.Chat(context.Background(), "", messages, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				got = resp.Usage
			}
			if got.ReasoningTokens != 80 || got.CompletionTokens != 100 || got.TotalTokens != 135 {
				t.Fatalf("usage = %+v (thinking must not be counted twice)", got)
			}
		})
	}
}
