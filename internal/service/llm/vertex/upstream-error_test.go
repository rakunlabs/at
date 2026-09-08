package vertex

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/ok"
	"golang.org/x/oauth2"

	"github.com/rakunlabs/at/internal/service"
)

func TestChatNonSuccessReturnsUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid model","code":400}}`))
	}))
	defer srv.Close()

	client, err := ok.New(
		ok.WithEnableBaseURLCheck(false),
		ok.WithDisableRetry(true),
		ok.WithEnableEnvValues(false),
	)
	if err != nil {
		t.Fatalf("ok.New: %v", err)
	}
	p := &Provider{
		Model:       "test-model",
		EndpointURL: srv.URL,
		tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"}),
		client:      client,
	}
	_, err = p.Chat(context.Background(), "test-model", []service.Message{{Role: "user", Content: "hi"}}, nil, nil)

	var upstreamErr *service.UpstreamError
	if !errors.As(err, &upstreamErr) {
		t.Fatalf("error = %T %v, want *service.UpstreamError", err, err)
	}
	if upstreamErr.StatusCode != http.StatusBadRequest || upstreamErr.Code != "400" || upstreamErr.Message != "invalid model" {
		t.Fatalf("UpstreamError = %#v", upstreamErr)
	}
}
