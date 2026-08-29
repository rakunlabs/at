package gemini

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
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"invalid request","status":"INVALID_ARGUMENT"}}`))
	}))
	defer srv.Close()

	p, err := New("test-key", "test-model", srv.URL, "", false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Chat(context.Background(), "test-model", []service.Message{{Role: "user", Content: "hi"}}, nil, nil)

	var upstreamErr *service.UpstreamError
	if !errors.As(err, &upstreamErr) {
		t.Fatalf("error = %T %v, want *service.UpstreamError", err, err)
	}
	if upstreamErr.StatusCode != http.StatusBadRequest || upstreamErr.Code != "INVALID_ARGUMENT" || upstreamErr.Message != "invalid request" {
		t.Fatalf("UpstreamError = %#v", upstreamErr)
	}
}
