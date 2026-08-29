package minimax

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func TestNativeMediaNonSuccessReturnsUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"base_resp":{"status_code":1004,"status_msg":"invalid api key"}}`))
	}))
	defer server.Close()

	provider, err := New("test-key", "test-model", server.URL+"/v1", "", false, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "image",
			call: func() error {
				_, err := provider.GenerateImage(context.Background(), service.ImageGenerateRequest{Prompt: "test"})
				return err
			},
		},
		{
			name: "audio",
			call: func() error {
				_, err := provider.GenerateAudio(context.Background(), service.AudioGenerateRequest{Input: "test"})
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var upstreamErr *service.UpstreamError
			if err := tt.call(); !errors.As(err, &upstreamErr) {
				t.Fatalf("error = %T %v, want *service.UpstreamError", err, err)
			}
			if upstreamErr.Provider != "minimax" || upstreamErr.StatusCode != http.StatusUnauthorized || upstreamErr.Code != "1004" || upstreamErr.Message != "invalid api key" {
				t.Fatalf("UpstreamError = %#v", upstreamErr)
			}
		})
	}
}

func TestNativeMediaRateLimitRemainsTyped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"base_resp":{"status_code":1002,"status_msg":"too many requests"}}`))
	}))
	defer server.Close()

	provider, err := New("test-key", "test-model", server.URL+"/v1", "", false, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = provider.GenerateImage(context.Background(), service.ImageGenerateRequest{Prompt: "test"})

	var rateLimitErr *service.RateLimitError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("error = %T %v, want *service.RateLimitError", err, err)
	}
	if rateLimitErr.Provider != "minimax" || rateLimitErr.RetryAfter != 3*time.Second {
		t.Fatalf("RateLimitError = %#v", rateLimitErr)
	}
}
