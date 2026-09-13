package vertex

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/ok"
	"golang.org/x/oauth2"

	"github.com/rakunlabs/at/internal/service"
)

func TestVertexStreamContracts(t *testing.T) {
	for _, tt := range []struct {
		name, args, finish string
		wantError          bool
		wantCalls          int
	}{
		{"object", `{"x":1}`, "tool_calls", false, 1},
		{"null", "null", "tool_calls", true, 0},
		{"length", `{"x":`, "length", false, 0},
		{"abrupt EOF", `{"x":1}`, "", true, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data:{\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"f\",\"arguments\":%q}}]},\"finish_reason\":null}]}\n\n", tt.args)
				if tt.finish != "" {
					fmt.Fprintf(w, "data:{\"choices\":[{\"delta\":{},\"finish_reason\":%q}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":10,\"total_tokens\":110,\"prompt_tokens_details\":{\"cached_tokens\":80}}}\n\ndata:[DONE]\n\n", tt.finish)
				}
			}))
			defer srv.Close()
			client, err := ok.New(ok.WithEnableBaseURLCheck(false), ok.WithDisableRetry(true), ok.WithEnableEnvValues(false))
			if err != nil {
				t.Fatal(err)
			}
			p := &Provider{Model: "model", EndpointURL: srv.URL, tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"}), client: client}
			ch, _, err := p.ChatStream(context.Background(), "", nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			var streamErr error
			var usage *service.Usage
			var calls int
			for c := range ch {
				if c.Error != nil {
					streamErr = c.Error
				}
				if c.Usage != nil {
					usage = c.Usage
				}
				calls += len(c.ToolCalls)
			}
			if (streamErr != nil) != tt.wantError || calls != tt.wantCalls {
				t.Fatalf("calls=%d err=%v", calls, streamErr)
			}
			if tt.finish != "" && (usage == nil || usage.PromptTokens != 20 || usage.CacheReadTokens != 80) {
				t.Fatalf("usage lost: %+v", usage)
			}
		})
	}
}
