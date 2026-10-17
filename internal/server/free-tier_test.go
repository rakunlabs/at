package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

func TestProviderQuotaGatewayFallbackBeforeUpstreamCall(t *testing.T) {
	primary := &countingStreamProvider{name: "primary"}
	backup := &countingStreamProvider{name: "backup"}
	s, store, ctx := anthropicEndpointServer(t, map[string]ProviderInfo{
		"alpha": {provider: primary, providerType: "openai", defaultModel: "m", models: []string{"m"}},
		"beta":  {provider: backup, providerType: "openai", defaultModel: "m", models: []string{"m"}},
	})
	row, err := store.CreateProvider(service.WithLegacyWorkspaceAccess(ctx), service.ProviderRecord{Key: "alpha", Config: config.LLMConfig{Type: "openai", Model: "m", Quota: &config.ProviderQuotaConfig{QuotaLimits: config.QuotaLimits{RequestsPerDay: 1}}}})
	if err != nil {
		t.Fatal(err)
	}
	s.providerMu.Lock()
	info := s.providers["alpha"].WithProviderID(row.ID)
	s.providers["alpha"] = info
	s.providerMu.Unlock()
	token := gatewayRoutingToken(t, ctx, store, "at_daily_quota_test_key", service.APIToken{})
	body := `{"model":"alpha/m","messages":[{"role":"user","content":"hi"}],"at_fallbacks":["beta/m"]}`
	w := postChatStream(t, s, token, body)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "primary") {
		t.Fatalf("first %d: %s", w.Code, w.Body)
	}
	w = postChatStream(t, s, token, body)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "backup") {
		t.Fatalf("second %d: %s", w.Code, w.Body)
	}
	if primary.calls.Load() != 1 {
		t.Fatalf("exhausted provider called %d times", primary.calls.Load())
	}
	// Native and Responses clients use the same quota wrapper.
	for _, req := range []struct{ path, body string }{
		{"/at/gateway/v1/messages", `{"model":"alpha/m","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`},
		{"/at/gateway/v1/responses", `{"model":"alpha/m","input":"hi"}`},
	} {
		r := httptest.NewRequest("POST", req.path, strings.NewReader(req.body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		s.server.ServeHTTP(w, r)
		if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
			t.Fatalf("%s quota response %d: %s", req.path, w.Code, w.Body)
		}
	}
	if primary.calls.Load() != 1 {
		t.Fatal("other endpoint bypassed quota")
	}
}

type runtimeQuotaStore struct {
	governanceTestStore
	reserveQuotaErr error
	estimate        int64
	actual          int64
	attempted       bool
	settlements     int
}

func (s *runtimeQuotaStore) ReserveProviderQuota(_ context.Context, _, _ string, estimate int64) (*service.ProviderQuotaReservation, error) {
	s.estimate = estimate
	if s.reserveQuotaErr != nil {
		return nil, s.reserveQuotaErr
	}
	return &service.ProviderQuotaReservation{ID: "q", GuardOverrides: true, TokenLimited: true}, nil
}
func (s *runtimeQuotaStore) SettleProviderQuota(_ context.Context, _ string, actual int64, attempted bool) error {
	s.actual, s.attempted = actual, attempted
	s.settlements++
	return nil
}
func (*runtimeQuotaStore) ProviderQuotaUsage(context.Context, string) ([]service.ProviderQuotaUsage, error) {
	return nil, nil
}

func TestProviderQuotaRuntimeAdmissionAndSettlement(t *testing.T) {
	store := &runtimeQuotaStore{}
	s := &Server{store: store}
	inner := &governanceInnerProvider{usage: service.Usage{PromptTokens: 5, CompletionTokens: 8}}
	p := s.providerForRoute(&service.ProviderRoute{Record: service.ProviderRecord{ID: "real-id", Key: "key"}}, inner, "")
	if _, err := p.Chat(t.Context(), "m", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if store.estimate < 32000 || store.actual != 13 || !store.attempted {
		t.Fatalf("estimate=%d settled=%d attempted=%v", store.estimate, store.actual, store.attempted)
	}
	store.governanceTestStore.reserveErr = service.ErrProviderBudgetExceeded
	if _, err := p.Chat(t.Context(), "m", nil, nil, nil); !errors.Is(err, service.ErrProviderBudgetExceeded) {
		t.Fatal(err)
	}
	if store.attempted {
		t.Fatal("monetary refusal did not release quota admission")
	}
	store.reserveQuotaErr = &service.ProviderQuotaError{ResetAt: time.Now().Add(time.Hour)}
	inner.model = ""
	if _, err := p.Chat(t.Context(), "m", nil, nil, nil); !errors.Is(err, service.ErrProviderQuotaExceeded) {
		t.Fatal(err)
	}
	if inner.model != "" {
		t.Fatal("quota refusal reached upstream")
	}
}

func TestProviderQuotaEffectiveOutputAndOverrides(t *testing.T) {
	store := &runtimeQuotaStore{}
	s := &Server{store: store}
	inner := &governanceInnerProvider{}
	p := s.providerForRoute(&service.ProviderRoute{Record: service.ProviderRecord{ID: "id", Key: "key"}}, inner, "")
	small, large := 1, 100000
	if _, err := p.Chat(t.Context(), "m", nil, nil, &service.ChatOptions{MaxTokens: &small, MaxCompletionTokens: &large}); err != nil {
		t.Fatal(err)
	}
	if store.estimate != int64(large) {
		t.Fatalf("reserved %d, want larger effective allowance %d", store.estimate, large)
	}
	for _, body := range []map[string]any{{"max_tokens": 100000}, {"max_completion_tokens": 100000}, {"model": "other"}, {"generationConfig": map[string]any{"maxOutputTokens": 100000}}} {
		inner.model = ""
		_, err := p.Chat(t.Context(), "m", nil, nil, &service.ChatOptions{ExtraBody: body})
		if !errors.Is(err, service.ErrUnsupportedOperation) || inner.model != "" || store.attempted {
			t.Fatalf("unsafe extra_body admitted or not refunded: %v", err)
		}
	}
}
