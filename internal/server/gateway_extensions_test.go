package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func TestShouldFallback(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"context.Canceled", context.Canceled, true},
		{"context.DeadlineExceeded", context.DeadlineExceeded, true},
		{"rate limit", &service.RateLimitError{StatusCode: 429, Provider: "p"}, true},
		// classifyGatewayError defaults unclassified errors to 502, which
		// is in the retryable 5xx range — fallback IS appropriate.
		{"plain error treated as 5xx", errors.New("upstream blew up"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldFallback(tt.err); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIdempotencyCache_GetPut(t *testing.T) {
	c := newIdempotencyCache()
	c.put("k", idempotencyEntry{
		statusCode: 200,
		body:       []byte("hi"),
		expiresAt:  time.Now().Add(5 * time.Minute),
	})

	got, ok := c.get("k")
	if !ok {
		t.Fatal("expected entry")
	}
	if got.statusCode != 200 || string(got.body) != "hi" {
		t.Errorf("entry mismatch: %+v", got)
	}

	if _, ok := c.get("missing"); ok {
		t.Error("missing key should not return ok")
	}
}

func TestIdempotencyCache_Expiry(t *testing.T) {
	c := newIdempotencyCache()
	c.put("k", idempotencyEntry{
		statusCode: 200,
		body:       []byte("stale"),
		expiresAt:  time.Now().Add(-1 * time.Second),
	})
	if _, ok := c.get("k"); ok {
		t.Error("expired entry should not be returned")
	}
}

func TestBuildMockChatResponse(t *testing.T) {
	resp := buildMockChatResponse("openai/gpt-4o", "hello world")
	if resp.Model != "openai/gpt-4o" {
		t.Errorf("model: %q", resp.Model)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices: %d", len(resp.Choices))
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason: %q", resp.Choices[0].FinishReason)
	}
	if resp.Choices[0].Message.Content == nil || *resp.Choices[0].Message.Content != "hello world" {
		t.Errorf("content mismatch")
	}
}

func TestCaptureResponseWriter(t *testing.T) {
	c := newCaptureWriter()
	c.Header().Set("X-Foo", "bar")
	c.WriteHeader(http.StatusTeapot)
	_, _ = c.Write([]byte("body"))

	real := httptest.NewRecorder()
	c.flushTo(real)
	if real.Code != http.StatusTeapot {
		t.Errorf("status: %d", real.Code)
	}
	if real.Body.String() != "body" {
		t.Errorf("body: %q", real.Body.String())
	}
	if real.Header().Get("X-Foo") != "bar" {
		t.Errorf("header lost")
	}
}

func TestWithRequestTimeout(t *testing.T) {
	ctx, cancel := withRequestTimeout(context.Background(), 0)
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Error("0 timeout should not set deadline")
	}

	ctx, cancel = withRequestTimeout(context.Background(), 100)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("100ms timeout should set deadline")
	}
	if d := time.Until(deadline); d > 200*time.Millisecond || d < 0 {
		t.Errorf("deadline drift: %v", d)
	}
}
