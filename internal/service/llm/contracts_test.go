package llm_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/antropic"
	"github.com/rakunlabs/at/internal/service/llm/bedrock"
	"github.com/rakunlabs/at/internal/service/llm/cohere"
	"github.com/rakunlabs/at/internal/service/llm/gemini"
	"github.com/rakunlabs/at/internal/service/llm/minimax"
	"github.com/rakunlabs/at/internal/service/llm/openai"
	"github.com/rakunlabs/at/internal/service/llm/vertex"
)

type failingToken struct{}

func (failingToken) Token(context.Context) (string, error) { return "", errors.New("refresh failed") }

type googleToken struct{ fail bool }

func (g googleToken) Token() (string, error) {
	if g.fail {
		return "", errors.New("refresh failed")
	}
	return "google-token", nil
}

func TestAllProvidersRejectUnrepresentableChoicesBeforeInference(t *testing.T) {
	providers := map[string]service.LLMProvider{
		"openai": &openai.Provider{}, "codex": &openai.CodexProvider{},
		"anthropic": &antropic.Provider{}, "minimax": &minimax.Provider{Provider: &antropic.Provider{}},
		"gemini": &gemini.Provider{}, "vertex-gemini": &gemini.Provider{},
		"vertex": &vertex.Provider{}, "bedrock": &bedrock.Provider{}, "cohere": &cohere.Provider{},
	}
	for name, p := range providers {
		t.Run(name, func(t *testing.T) {
			n := 2
			opts := &service.ChatOptions{N: &n}
			_, err := p.Chat(context.Background(), "model", nil, nil, opts)
			var upstream *service.UpstreamError
			if !errors.As(err, &upstream) || upstream.StatusCode != 400 || upstream.Param != "n" {
				t.Fatalf("unexpected validation: %v", err)
			}
			if stream, ok := p.(service.LLMStreamProvider); ok {
				_, _, err = stream.ChatStream(context.Background(), "model", nil, nil, opts)
				if !errors.As(err, &upstream) || upstream.Param != "n" {
					t.Fatalf("stream validation: %v", err)
				}
			}
		})
	}
}

func TestProxyRefreshFailureNeverForwards(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer srv.Close()
	o, err := openai.New("", "model", srv.URL, "", false, nil, openai.WithTokenSource(failingToken{}))
	if err != nil {
		t.Fatal(err)
	}
	a, err := antropic.New("", "model", srv.URL, "", false, antropic.WithTokenSource(failingToken{}))
	if err != nil {
		t.Fatal(err)
	}
	g, err := gemini.New("", "model", srv.URL, "", false, gemini.WithGoogleTokenSource(googleToken{fail: true}))
	if err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]service.LLMStreamProvider{"openai": o, "anthropic": a, "vertex-gemini": g} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/proxy", strings.NewReader(`{"model":"test"}`))
			req.Header.Set("Authorization", "Bearer at-private-token")
			if err := p.Proxy(httptest.NewRecorder(), req, "/v1/messages"); err == nil {
				t.Fatal("refresh failure ignored")
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("forwarded %d unauthenticated requests", calls.Load())
	}
}

func TestGeminiProxyUsesADCWithoutCallerCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer google-token" || r.Header.Get("X-Goog-Api-Key") != "" || r.Header.Get("Cookie") != "" {
			t.Error("incorrect proxy credentials")
		}
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()
	p, err := gemini.New("stale-key", "model", srv.URL, "", false, gemini.WithGoogleTokenSource(googleToken{}))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/proxy", nil)
	req.Header.Set("Authorization", "Bearer at-token")
	req.Header.Set("Cookie", "at-session=private")
	w := httptest.NewRecorder()
	if err := p.Proxy(w, req, "/v1/models"); err != nil {
		t.Fatal(err)
	}
	if w.Body.String() != "ok" {
		t.Fatalf("proxy response: %s", w.Body.String())
	}
}

func TestAzureProxyUsesConfiguredKeyAndAPIVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Values("Api-Key"); len(got) != 1 || got[0] != "azure-key" {
			t.Errorf("API key override/duplication: %v", got)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("caller credentials forwarded")
		}
		if r.URL.Query().Get("api-version") != "2024-10-21" || r.URL.Query().Get("limit") != "1" {
			t.Errorf("query parameters lost: %s", r.URL.RawQuery)
		}
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()
	p, err := openai.New("", "m", srv.URL+"/chat/completions?api-version=2024-10-21", "", false, map[string]string{"api-key": "azure-key"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/proxy?limit=1", nil)
	req.Header.Set("Api-Key", "caller-key")
	req.Header.Set("Authorization", "Bearer at-token")
	req.Header.Set("Cookie", "at-session=private")
	w := httptest.NewRecorder()
	if err := p.Proxy(w, req, "/models"); err != nil {
		t.Fatal(err)
	}
	if w.Body.String() != "ok" {
		t.Fatalf("proxy response: %s", w.Body.String())
	}
}

func TestAnthropicExtraHeadersDoNotOverrideOAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer oauth-token" || r.Header.Get("X-Api-Key") != "" {
			t.Error("configured stale credentials override OAuth")
		}
		if r.Header.Get("X-Custom") != "value" || r.Header.Get("At-Prompt-Caching") != "" {
			t.Error("extra headers/internal controls handled incorrectly")
		}
		fmt.Fprint(w, `{"type":"message","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()
	p, err := antropic.New("", "m", srv.URL, "", false, antropic.WithTokenSource(antropic.NewStaticTokenSource("oauth-token")), antropic.WithExtraHeaders(map[string]string{
		"authorization": "stale", "x-api-key": "stale", "x-custom": "value", "at-prompt-caching": "off",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Chat(context.Background(), "", []service.Message{{Role: "user", Content: "hi"}}, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestMiniMaxExtraHeadersReachChatAndMedia(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("X-Custom") != "value" || r.Header.Get("At-Prompt-Caching") != "" {
			t.Error("extra headers dropped or internal control forwarded")
		}
		switch r.URL.Path {
		case "/anthropic/v1/messages":
			fmt.Fprint(w, `{"type":"message","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
		case "/v1/image_generation":
			fmt.Fprint(w, `{"base_resp":{"status_code":0},"data":{"image_urls":["https://example.com/image.png"]}}`)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()
	p, err := minimax.New("key", "model", srv.URL+"/anthropic", "", false, map[string]string{"x-custom": "value", "at-prompt-caching": "off"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Chat(context.Background(), "", []service.Message{{Role: "user", Content: "hi"}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := p.GenerateImage(context.Background(), service.ImageGenerateRequest{Prompt: "test"}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d", calls.Load())
	}
}
