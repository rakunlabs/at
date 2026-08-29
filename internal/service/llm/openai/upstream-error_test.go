package openai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestChatNonSuccessReturnsUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key provided","type":"invalid_request_error","param":null,"code":"invalid_api_key"}}`))
	}))
	defer srv.Close()

	p, err := New("test-key", "test-model", srv.URL, "", false, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), "test-model", []service.Message{{Role: "user", Content: "hi"}}, nil, nil)

	var upstreamErr *service.UpstreamError
	if !errors.As(err, &upstreamErr) {
		t.Fatalf("error = %T %v, want *service.UpstreamError", err, err)
	}
	if upstreamErr.StatusCode != http.StatusUnauthorized || upstreamErr.Code != "invalid_api_key" || upstreamErr.Param != "" || upstreamErr.Message != "Incorrect API key provided" {
		t.Fatalf("UpstreamError = %#v", upstreamErr)
	}
}

func TestChatStreamNonSuccessPreservesOpenAIErrorFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"The model does not exist","type":"invalid_request_error","param":"model","code":"model_not_found"}}`))
	}))
	defer srv.Close()

	p, err := New("test-key", "missing-model", srv.URL, "", false, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _, err = p.ChatStream(context.Background(), "missing-model", []service.Message{{Role: "user", Content: "hi"}}, nil, nil)

	var upstreamErr *service.UpstreamError
	if !errors.As(err, &upstreamErr) {
		t.Fatalf("error = %T %v, want *service.UpstreamError", err, err)
	}
	if upstreamErr.StatusCode != http.StatusNotFound || upstreamErr.Code != "model_not_found" || upstreamErr.Param != "model" || upstreamErr.Message != "The model does not exist" {
		t.Fatalf("UpstreamError = %#v", upstreamErr)
	}
}
