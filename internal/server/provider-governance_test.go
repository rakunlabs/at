package server

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

type governanceTestStore struct {
	service.ProviderStorer
	service.ProviderBudgetStorer
	reserveErr error
	resources  []service.BudgetResource
	userID     string
	estimate   *float64
	settled    []float64
}

func (s *governanceTestStore) ReserveProviderBudget(_ context.Context, resources []service.BudgetResource, userID string, estimate *float64) (*service.ProviderBudgetReservation, error) {
	s.resources, s.userID, s.estimate = resources, userID, estimate
	if s.reserveErr != nil {
		return nil, s.reserveErr
	}
	return &service.ProviderBudgetReservation{ID: "r1"}, nil
}

func (s *governanceTestStore) SettleProviderBudget(_ context.Context, _ string, actual float64) error {
	s.settled = append(s.settled, actual)
	return nil
}

type governancePricingStore struct {
	service.AgentBudgetStorer
}

func (governancePricingStore) ListModelPricing(context.Context) ([]service.ModelPricing, error) {
	return []service.ModelPricing{{ProviderKey: "openai", Model: "gpt-real", PromptPricePer1M: 1_000_000, CompletionPricePer1M: 2_000_000}}, nil
}

type governanceInnerProvider struct {
	model string
	usage service.Usage
	err   error
}

func (p *governanceInnerProvider) Chat(_ context.Context, model string, _ []service.Message, _ []service.Tool, _ *service.ChatOptions) (*service.LLMResponse, error) {
	p.model = model
	if p.err != nil {
		return nil, p.err
	}
	return &service.LLMResponse{Content: "ok", Usage: p.usage, Finished: true}, nil
}

func TestBudgetedProviderReservesAndSettles(t *testing.T) {
	store := &governanceTestStore{}
	s := &Server{store: store, agentBudgetStore: governancePricingStore{}}
	inner := &governanceInnerProvider{usage: service.Usage{PromptTokens: 2, CompletionTokens: 3}}
	route := &service.ProviderRoute{Record: service.ProviderRecord{ID: "prov-1", Key: "openai"}, ActualModel: "gpt-real", VirtualProviderID: "virt-1"}
	provider := s.providerForRoute(route, inner, "token-owner")
	max := 10
	if _, err := provider.Chat(t.Context(), "alias", []service.Message{{Role: "user", Content: "hi"}}, nil, &service.ChatOptions{MaxTokens: &max}); err != nil {
		t.Fatal(err)
	}
	if inner.model != "gpt-real" {
		t.Fatalf("virtual alias was not mapped to the real model: %q", inner.model)
	}
	if len(store.resources) != 2 || store.resources[0].ID != "prov-1" || store.resources[1].Kind != service.BudgetResourceVirtualProvider {
		t.Fatalf("resources: %+v", store.resources)
	}
	if store.userID != "token-owner" {
		t.Fatalf("personal token owner not charged: %q", store.userID)
	}
	if store.estimate == nil || *store.estimate <= 0 {
		t.Fatalf("priced call reserved no estimate: %v", store.estimate)
	}
	// 2 prompt * 1¢ + 3 completion * 2¢ = 8 cents at these test prices.
	if len(store.settled) != 1 || store.settled[0] != 800 {
		t.Fatalf("settled = %v", store.settled)
	}

	// A signed-in principal wins over the fallback user.
	ctx := service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: "browser-user"})
	if _, err := provider.Chat(ctx, "alias", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if store.userID != "browser-user" {
		t.Fatalf("browser user not charged: %q", store.userID)
	}

	// Shared tokens (no owner, no principal) never charge a user.
	shared := s.providerForRoute(route, inner, "")
	if _, err := shared.Chat(t.Context(), "alias", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if store.userID != "" {
		t.Fatalf("shared token charged user %q", store.userID)
	}
}

func TestBudgetedProviderErrorsFallbackAndClassify(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		status   int
		code     string
		fallback bool
	}{
		{"total", service.ErrProviderBudgetExceeded, http.StatusTooManyRequests, "provider_budget_exceeded", true},
		{"user", service.ErrProviderUserLimit, http.StatusTooManyRequests, "provider_user_budget_exceeded", true},
		{"blocked", service.ErrProviderUserBlocked, http.StatusForbidden, "provider_user_blocked", true},
		{"pricing", service.ErrProviderPricingRequired, http.StatusConflict, "provider_pricing_required", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &governanceTestStore{reserveErr: tt.err}
			s := &Server{store: store}
			inner := &governanceInnerProvider{}
			provider := s.providerForRoute(&service.ProviderRoute{Record: service.ProviderRecord{ID: "p", Key: "openai"}}, inner, "")
			_, err := provider.Chat(t.Context(), "m", nil, nil, nil)
			if !errors.Is(err, tt.err) {
				t.Fatalf("err = %v", err)
			}
			if inner.model != "" {
				t.Fatal("upstream was called after a refused reservation")
			}
			if shouldFallback(err) != tt.fallback {
				t.Fatalf("shouldFallback = %v", !tt.fallback)
			}
			status, body := classifyGatewayError(err)
			code := body["error"].(map[string]any)["code"]
			if status != tt.status || code != tt.code {
				t.Fatalf("classify = %d %v", status, code)
			}
		})
	}
}
