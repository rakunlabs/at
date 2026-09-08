package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/query"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/antropic"
	"github.com/rakunlabs/at/internal/service/llm/bedrock"
	"github.com/rakunlabs/at/internal/service/llm/cohere"
	"github.com/rakunlabs/at/internal/service/llm/minimax"
)

func TestPersonalChatNativeAdapters(t *testing.T) {
	for _, tc := range []struct{ provider, reason, want string }{
		{"anthropic", "end_turn", "stop"}, {"anthropic", "max_tokens", "length"}, {"anthropic", "stop_sequence", "stop"},
		{"minimax", "end_turn", "stop"}, {"minimax", "max_tokens", "length"},
		{"bedrock", "end_turn", "stop"}, {"bedrock", "max_tokens", "length"}, {"bedrock", "stop_sequence", "stop"}, {"bedrock", "guardrail_intervened", "content_filter"}, {"bedrock", "content_filtered", "content_filter"},
		{"cohere", "COMPLETE", "stop"}, {"cohere", "MAX_TOKENS", "length"}, {"cohere", "STOP_SEQUENCE", "stop"},
		{"anthropic", "unknown_native", ""}, {"bedrock", "unknown_native", ""}, {"cohere", "unknown_native", ""},
		{"bedrock", "", ""}, {"cohere", "", ""},
	} {
		t.Run(tc.provider+"/"+tc.reason, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" {
					t.Errorf("method: %s", r.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				switch tc.provider {
				case "anthropic", "minimax":
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":7,\"cache_read_input_tokens\":2,\"cache_creation_input_tokens\":3}}}\n\n")
					_, _ = io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"native text\"}}\n\n")
					_, _ = fmt.Fprintf(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":%q},\"usage\":{\"output_tokens\":5}}\n\n", tc.reason)
					// The adapter publishes usage only on this event AFTER the finish.
					_, _ = io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
				case "bedrock":
					_, _ = fmt.Fprintf(w, `{"output":{"message":{"content":[{"text":"native text"}]}},"stopReason":%q,"usage":{"inputTokens":7,"outputTokens":5,"totalTokens":12}}`, tc.reason)
				case "cohere":
					_, _ = fmt.Fprintf(w, `{"message":{"role":"assistant","content":[{"type":"text","text":"native text"}]},"finish_reason":%q,"usage":{"tokens":{"input_tokens":7,"output_tokens":5}}}`, tc.reason)
				}
			}))
			defer upstream.Close()
			var provider service.LLMProvider
			var err error
			switch tc.provider {
			case "anthropic":
				provider, err = antropic.New("private", "text-model", upstream.URL, "", false)
			case "minimax":
				provider, err = minimax.New("private", "text-model", upstream.URL, "", false, nil)
			case "bedrock":
				provider, err = bedrock.New("access:secret", "text-model", upstream.URL, "", false)
			case "cohere":
				provider, err = cohere.New("private", "text-model", upstream.URL, "", false)
			}
			if err != nil {
				t.Fatal(err)
			}
			s, owners, tokens := personalHTTPFixture(t, provider)
			pricing := service.ModelPricing{ProviderKey: "test", Model: "text-model", PromptPricePer1M: 1, CompletionPricePer1M: 2, CacheReadPricePer1M: 0.5, CacheWritePricePer1M: 1.5}
			if err := s.agentBudgetStore.SetModelPricing(t.Context(), pricing); err != nil {
				t.Fatal(err)
			}
			c := personalCreate(t, s, tokens[0])
			w := personalRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"content":"hello","request_id":"one"}`)
			messages, err := s.store.(service.PersonalChatStorer).ListPersonalMessages(t.Context(), owners[0], c.ID, "", 50)
			if err != nil || len(messages) != 2 {
				t.Fatal(messages, err)
			}
			m := messages[0]
			if m.Content != "native text" {
				t.Fatalf("finish discarded text: %+v", m)
			}
			if tc.want == "" {
				if m.Status != "failed" || strings.Contains(w.Body.String(), "event: done") {
					t.Fatal(m, w.Body)
				}
				return
			}
			if m.Status != "completed" || m.FinishReason != tc.want || m.Usage.PromptTokens != 7 || m.Usage.CompletionTokens != 5 {
				t.Fatal(m, w.Body)
			}
			wantCost := (7.0 + 5*2) / 10000
			if tc.provider == "anthropic" || tc.provider == "minimax" {
				if m.Usage.CacheReadTokens != 2 || m.Usage.CacheWriteTokens != 3 || m.Usage.TotalTokens != 17 {
					t.Fatal(m)
				}
				wantCost += (2*0.5 + 3*1.5) / 10000
			}
			costs, err := s.costEventStore.ListCostEvents(t.Context(), &query.Query{})
			if err != nil || len(costs.Data) != 1 || math.Abs(costs.Data[0].CostCents-wantCost) > 1e-10 {
				t.Fatal(costs, err, wantCost)
			}
			encoded, _ := json.Marshal(m)
			if !strings.Contains(w.Body.String(), "event: done\ndata: "+string(encoded)) {
				t.Fatal("terminal snapshot differs", w.Body)
			}
		})
	}
}

func TestPersonalChatStrictFinishMap(t *testing.T) {
	for _, tc := range []struct{ reason, want string }{
		{"STOP", "stop"}, {"endofturn", "stop"}, {"MAX_OUTPUT_TOKENS", "length"}, {"SAFETY", "content_filter"}, {"blocklist", "content_filter"}, {"prohibited_content", "content_filter"}, {"spii", "content_filter"}, {"recitation", "content_filter"},
		{"", ""}, {"surprise", ""}, {"tool_use", ""}, {"TOOL_CALL", ""}, {"function_call", ""}, {"ERROR", ""},
	} {
		if got := personalFinishReason(tc.reason); got != tc.want {
			t.Errorf("%q => %q, want %q", tc.reason, got, tc.want)
		}
	}
}

// Cancellation is triggered synchronously while the handler consumes a delta,
// making EOF, provider errors and cancellation ready together without sleeps.
type personalCancelWriter struct {
	*httptest.ResponseRecorder
	stop func()
}

func (w *personalCancelWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	if strings.Contains(string(p), "event: delta") {
		w.stop()
	}
	return n, err
}

func TestPersonalChatCancellationWinsStreamOutcome(t *testing.T) {
	for _, outcome := range []string{"eof", "error", "finish", "deadline"} {
		t.Run(outcome, func(t *testing.T) {
			provider := &personalTestProvider{stream: func(context.Context, string, []service.Message) (<-chan service.StreamChunk, error) {
				ch := make(chan service.StreamChunk, 2)
				chunk := service.StreamChunk{Content: "partial"}
				if outcome == "finish" || outcome == "deadline" {
					chunk.FinishReason = "end_turn"
				}
				ch <- chunk
				if outcome == "error" {
					ch <- service.StreamChunk{Error: errors.New("cancelled upstream secret")}
				}
				close(ch)
				return ch, nil
			}}
			s, owners, tokens := personalHTTPFixture(t, provider)
			c := personalCreate(t, s, tokens[0])
			ctx, cancel := context.WithCancel(t.Context())
			if outcome == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), time.Second)
			}
			defer cancel()
			r := httptest.NewRequest("POST", "/at/api/v1/conversations/"+c.ID+"/messages", strings.NewReader(`{"content":"hello","request_id":"one"}`)).WithContext(ctx)
			r.Header.Set("Authorization", "Bearer "+tokens[0])
			r.Header.Set("Content-Type", "application/json")
			w := &personalCancelWriter{ResponseRecorder: httptest.NewRecorder(), stop: cancel}
			if outcome == "deadline" {
				w.stop = func() { <-ctx.Done() }
			}
			s.server.ServeHTTP(w, r)
			messages, err := s.store.(service.PersonalChatStorer).ListPersonalMessages(t.Context(), owners[0], c.ID, "", 50)
			if err != nil {
				t.Fatal(err)
			}
			m := messages[0]
			wantStatus, wantError := "cancelled", "generation_cancelled"
			if outcome == "deadline" {
				wantStatus, wantError = "failed", "generation_timeout"
			}
			if m.Status != wantStatus || m.Error != wantError || m.Content != "partial" {
				t.Fatalf("%s: %+v", outcome, m)
			}
			if strings.Contains(w.Body.String(), "event: done") {
				t.Fatal(w.Body)
			}
		})
	}
}
