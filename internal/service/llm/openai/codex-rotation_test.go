package openai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCodexCoordinatedRefreshSharesRotatedCredential(t *testing.T) {
	var exchanges atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exchanges.Add(1)
		_, _ = w.Write([]byte(`{"access_token":"access-new","refresh_token":"refresh-new","expires_in":3600}`))
	}))
	defer upstream.Close()
	stored := CodexTokens{AccessToken: "access-old", RefreshToken: "refresh-old", AccountID: "account", ExpiresAt: time.Now().Add(-time.Hour)}
	var lock sync.Mutex
	coordinator := func(ctx context.Context, rejected string, exchange func(context.Context, CodexTokens) (*CodexTokens, error)) (*CodexTokens, error) {
		lock.Lock()
		defer lock.Unlock()
		if !CodexTokenFresh(stored.AccessToken, stored.ExpiresAt) || stored.AccessToken == rejected {
			next, err := exchange(ctx, stored)
			if err != nil {
				return nil, err
			}
			stored = *next
		}
		copy := stored
		return &copy, nil
	}
	var sources []*CodexTokenSource
	for range 8 {
		source := NewCodexTokenSource("access-old", "refresh-old", "account", time.Now().Add(-time.Hour), upstream.Client(), codexTestEndpoints(upstream.URL))
		source.SetCoordinator(coordinator)
		sources = append(sources, source)
	}
	var wg sync.WaitGroup
	for _, source := range sources {
		wg.Go(func() {
			token, err := source.Token(t.Context())
			if err != nil || token != "access-new" {
				t.Errorf("token = %q, error = %v", token, err)
			}
		})
	}
	wg.Wait()
	if exchanges.Load() != 1 {
		t.Fatalf("single-use token exchanged %d times", exchanges.Load())
	}
}

func TestCodexCoordinatedPersistenceFailureRetriesSaveOnly(t *testing.T) {
	source := NewCodexTokenSource("old", "old-refresh", "account", time.Now().Add(-time.Hour), nil)
	acquires, saves := 0, 0
	source.SetCoordinator(func(context.Context, string, func(context.Context, CodexTokens) (*CodexTokens, error)) (*CodexTokens, error) {
		acquires++
		return &CodexTokens{AccessToken: "new", RefreshToken: "new-refresh", AccountID: "account", ExpiresAt: time.Now().Add(time.Hour), PreviousRefreshToken: "old-refresh"}, errors.New("commit failed")
	})
	source.SetRefreshCallback(func(_ context.Context, previous, access, refresh, account string, expiry time.Time) error {
		saves++
		if previous != "old-refresh" || access != "new" || refresh != "new-refresh" {
			t.Fatal("rotated credentials were lost")
		}
		return nil
	})
	if _, err := source.Token(t.Context()); err == nil {
		t.Fatal("persistence error hidden")
	}
	if token, err := source.Token(t.Context()); err != nil || token != "new" {
		t.Fatalf("retry = %q %v", token, err)
	}
	if acquires != 1 || saves != 1 {
		t.Fatalf("acquires=%d saves=%d", acquires, saves)
	}
}

func TestCodexDelayedUnauthorizedDoesNotInvalidateNewToken(t *testing.T) {
	source := NewCodexTokenSource("new", "refresh", "account", time.Now().Add(time.Hour), nil)
	source.InvalidateToken("old")
	if token, err := source.Token(t.Context()); err != nil || token != "new" {
		t.Fatalf("new token invalidated: %v", err)
	}
}

func TestCodexModelsEmptyCatalogIsAnError(t *testing.T) {
	for _, body := range []string{`{"models":[]}`, `{}`, `{"models":[{"id":"unexpected"}]}`} {
		t.Run(body, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("client_version") != CodexClientVersion {
					t.Error("catalog used AT version rather than Codex version")
				}
				if r.URL.Path != "/models" || r.URL.Query().Get("custom") != "keep" {
					t.Errorf("custom catalog URL changed: %s", r.URL)
				}
				_, _ = w.Write([]byte(body))
			}))
			defer upstream.Close()
			p := NewCodexProvider("", "account", NewCodexTokenSource("access", "", "account", time.Time{}, nil), WithCodexBaseURL(upstream.URL+"/responses/?client_version=0.0.0&custom=keep"))
			if _, err := p.Models(t.Context()); err == nil || !strings.Contains(err.Error(), "no available model") {
				t.Fatalf("empty catalog = %v", err)
			}
		})
	}
}

func TestCodexResponsesURLNormalizesOpenAIPreset(t *testing.T) {
	for _, raw := range []string{"", "https://api.openai.com", "https://api.openai.com/v1", "https://api.openai.com/v1/chat/completions", "https://api.openai.com/v1/responses/"} {
		if got := CodexResponsesURL(raw); got != CodexDefaultResponsesURL {
			t.Fatalf("preset %q resolved to %q", raw, got)
		}
	}
	if got := CodexResponsesURL("https://relay.example/codex/responses/?custom=abc/"); got != "https://relay.example/codex/responses/?custom=abc/" {
		t.Fatalf("custom relay changed: %q", got)
	}
}
