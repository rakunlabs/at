package common

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// relay drives a wrapped response the way a ReverseProxy does: read the body to
// completion, then close it.
func relay(t *testing.T, resp *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	return string(body)
}

func observedResponse(t *testing.T, contentType, body string, status int) (service.ProxyObservation, string) {
	t.Helper()

	var got service.ProxyObservation
	calls := 0
	ctx := service.ContextWithProxyObserver(context.Background(), func(obs service.ProxyObservation) {
		got = obs
		calls++
	})

	modify := ProxyResponseObserver(ctx)
	if modify == nil {
		t.Fatal("observer not attached")
	}

	resp := &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	if err := modify(resp); err != nil {
		t.Fatalf("modify: %v", err)
	}

	relayed := relay(t, resp)
	if calls != 1 {
		t.Fatalf("observer fired %d times, want exactly 1", calls)
	}

	return got, relayed
}

func TestProxyObserverParsesAnthropicUsage(t *testing.T) {
	body := `{"id":"msg_1","type":"message","usage":{"input_tokens":120,"output_tokens":45,"cache_read_input_tokens":80}}`
	obs, relayed := observedResponse(t, "application/json", body, http.StatusOK)

	if relayed != body {
		t.Fatalf("body was altered in transit")
	}
	if obs.UsageSource != service.UsageSourceParsed {
		t.Fatalf("usage source %q want parsed", obs.UsageSource)
	}
	if obs.Usage.PromptTokens != 120 || obs.Usage.CompletionTokens != 45 || obs.Usage.CacheReadTokens != 80 {
		t.Fatalf("usage %+v", obs.Usage)
	}
	if obs.StatusCode != http.StatusOK {
		t.Fatalf("status %d", obs.StatusCode)
	}
}

func TestProxyObserverParsesOpenAIUsage(t *testing.T) {
	body := `{"id":"chatcmpl-1","usage":{"prompt_tokens":10,"completion_tokens":7,"total_tokens":17,"completion_tokens_details":{"reasoning_tokens":3}}}`
	obs, _ := observedResponse(t, "application/json; charset=utf-8", body, http.StatusOK)

	if obs.UsageSource != service.UsageSourceParsed {
		t.Fatalf("usage source %q want parsed", obs.UsageSource)
	}
	if obs.Usage.PromptTokens != 10 || obs.Usage.CompletionTokens != 7 || obs.Usage.ReasoningTokens != 3 {
		t.Fatalf("usage %+v", obs.Usage)
	}
}

// Anthropic reports input on message_start and output on message_delta, so a
// later event must not erase the earlier count.
func TestProxyObserverMergesStreamedUsageAcrossEvents(t *testing.T) {
	body := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":200,"output_tokens":1}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","usage":{"output_tokens":64}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	obs, relayed := observedResponse(t, "text/event-stream", body, http.StatusOK)

	if relayed != body {
		t.Fatal("stream body was altered in transit")
	}
	if obs.UsageSource != service.UsageSourceStreamParsed {
		t.Fatalf("usage source %q want stream_parsed", obs.UsageSource)
	}
	if obs.Usage.PromptTokens != 200 {
		t.Fatalf("input tokens lost across events: %+v", obs.Usage)
	}
	if obs.Usage.CompletionTokens != 64 {
		t.Fatalf("output tokens %d want 64", obs.Usage.CompletionTokens)
	}
}

func TestProxyObserverOpenAIStreamUsage(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"hi"}}]}`,
		``,
		`data: {"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":5}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	obs, _ := observedResponse(t, "text/event-stream", body, http.StatusOK)

	if obs.UsageSource != service.UsageSourceStreamParsed {
		t.Fatalf("usage source %q want stream_parsed", obs.UsageSource)
	}
	if obs.Usage.PromptTokens != 11 || obs.Usage.CompletionTokens != 5 {
		t.Fatalf("usage %+v", obs.Usage)
	}
}

// A request AT cannot meter must still be recorded, with zero counts and an
// explicit marker — never estimated, never dropped.
func TestProxyObserverReportsUnavailable(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		body        string
	}{
		{"binary body", "application/octet-stream", "\x00\x01\x02binary"},
		{"json without usage", "application/json", `{"id":"file_1","object":"file"}`},
		{"malformed json", "application/json", `{not json`},
		{"stream without usage", "text/event-stream", "data: {\"choices\":[]}\n\n"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			obs, relayed := observedResponse(t, tt.contentType, tt.body, http.StatusOK)
			if relayed != tt.body {
				t.Fatal("body was altered in transit")
			}
			if obs.UsageSource != service.UsageSourceUnavailable {
				t.Fatalf("usage source %q want unavailable", obs.UsageSource)
			}
			if obs.Usage != (service.Usage{}) {
				t.Fatalf("counts must stay zero when usage is unreadable: %+v", obs.Usage)
			}
		})
	}
}

// Over the inspection bound the body relays untouched and is not buffered whole.
func TestProxyObserverDoesNotBufferOversizedBodies(t *testing.T) {
	filler := strings.Repeat("a", ProxyUsageMaxBytes+1024)
	body := `{"padding":"` + filler + `","usage":{"input_tokens":5,"output_tokens":5}}`

	obs, relayed := observedResponse(t, "application/json", body, http.StatusOK)

	if len(relayed) != len(body) {
		t.Fatalf("relayed %d bytes want %d", len(relayed), len(body))
	}
	if obs.UsageSource != service.UsageSourceUnavailable {
		t.Fatalf("usage source %q want unavailable for an oversized body", obs.UsageSource)
	}
}

// A client that disconnects mid-response closes the body without reading EOF;
// the request must still be recorded exactly once.
func TestProxyObserverFiresOnEarlyClose(t *testing.T) {
	var got service.ProxyObservation
	calls := 0
	ctx := service.ContextWithProxyObserver(context.Background(), func(obs service.ProxyObservation) {
		got = obs
		calls++
	})

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: {\"usage\":{\"input_tokens\":3}}\n\n")),
	}
	if err := ProxyResponseObserver(ctx)(resp); err != nil {
		t.Fatalf("modify: %v", err)
	}

	buf := make([]byte, 4)
	if _, err := resp.Body.Read(buf); err != nil {
		t.Fatalf("partial read: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if calls != 1 {
		t.Fatalf("observer fired %d times on early close, want 1", calls)
	}
	if got.StatusCode != http.StatusOK {
		t.Fatalf("status %d", got.StatusCode)
	}
}

func TestProxyResponseObserverAbsentWithoutContext(t *testing.T) {
	if ProxyResponseObserver(context.Background()) != nil {
		t.Fatal("observer must be nil when the gateway attached none")
	}
}

func TestProxyInputWeightEstimatesFromBodyLength(t *testing.T) {
	r, err := http.NewRequest(http.MethodPost, "http://example.test/v1/messages", strings.NewReader(strings.Repeat("x", 40000)))
	if err != nil {
		t.Fatal(err)
	}
	if got := ProxyInputWeight(r); got != 10000 {
		t.Fatalf("weight %d want 10000", got)
	}

	bodyless, err := http.NewRequest(http.MethodGet, "http://example.test/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := ProxyInputWeight(bodyless); got != 0 {
		t.Fatalf("bodyless weight %d want 0", got)
	}
}
