package server

import (
	"context"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

type providerPriceStore struct {
	service.ProviderStorer
	prices    map[string]map[string]config.ModelPrice
	workspace string
}

func (s *providerPriceStore) ProviderModelPrices(_ context.Context, workspaceID string, keys []string) (map[string]map[string]config.ModelPrice, error) {
	s.workspace = workspaceID
	out := map[string]map[string]config.ModelPrice{}
	for _, key := range keys {
		if prices, ok := s.prices[key]; ok {
			out[key] = prices
		}
	}
	return out, nil
}

func TestProviderDeclaredPricing(t *testing.T) {
	personal := service.PersonalProviderReference("01PERSONAL")
	store := &providerPriceStore{prices: map[string]map[string]config.ModelPrice{
		"openai": {"gpt-x": {Input: 9, Output: 9}, "free": {}},
		personal: {"gpt-x": {Input: 2, Output: 4}},
	}}
	budgets := &pricedBudgetStore{pricing: []service.ModelPricing{
		{ProviderKey: "openai", Model: "gpt-x", PromptPricePer1M: 1, CompletionPricePer1M: 1},
		{ProviderKey: "", Model: "gpt-x", PromptPricePer1M: 100, CompletionPricePer1M: 100},
	}}
	s := &Server{store: store, agentBudgetStore: budgets}
	usage := service.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000}
	ctx := withPricingWorkspace(context.Background(), "team")

	tests := []struct {
		name      string
		key       string
		model     string
		want      float64
		available bool
	}{
		{name: "installation price wins over provider price", key: "openai", model: "gpt-x", want: 200, available: true},
		{name: "provider price beats installation-wide row", key: personal, model: "gpt-x", want: 600, available: true},
		{name: "declared free model is priced at zero", key: "openai", model: "free", want: 0, available: true},
		{name: "unpriced stays unavailable", key: personal, model: "other", want: 0, available: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := s.estimateGatewayUsageCost(ctx, tt.key, tt.model, tt.key+"/"+tt.model, usage)
			if ok != tt.available || got != tt.want {
				t.Fatalf("cost = (%v, %v), want (%v, %v)", got, ok, tt.want, tt.available)
			}
		})
	}
	if store.workspace != "team" {
		t.Fatalf("pricing workspace = %q, want team", store.workspace)
	}
}

// A personal provider whose key collides with an installation provider must be
// priced from its own declaration, never from the other provider's row.
func TestBudgetedProviderPricesPersonalByReference(t *testing.T) {
	personal := service.PersonalProviderReference("01PERSONAL")
	store := &providerPriceStore{prices: map[string]map[string]config.ModelPrice{personal: {"gpt-x": {Input: 2, Output: 4}}}}
	budgets := &pricedBudgetStore{pricing: []service.ModelPricing{{ProviderKey: "openai", Model: "gpt-x", PromptPricePer1M: 50, CompletionPricePer1M: 50}}}
	s := &Server{store: store, agentBudgetStore: budgets}
	route := &service.ProviderRoute{Record: service.ProviderRecord{ID: "01PERSONAL", Key: "openai", OwnerUserID: "user-1"}, ActualModel: "gpt-x"}
	provider := s.providerForRoute(route, &silentProxyProvider{}, "").(*budgetedProvider)
	cost := provider.estimate(context.Background(), "gpt-x", service.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000})
	if cost == nil || *cost != 600 {
		t.Fatalf("estimate = %v, want 600", cost)
	}
}

func TestValidateModelPricing(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.LLMConfig
		want string
	}{
		{name: "chat model", cfg: config.LLMConfig{Models: []string{"m"}, ModelPricing: map[string]config.ModelPrice{"m": {Input: 1, Output: 2}}}},
		{name: "embedding model", cfg: config.LLMConfig{Model: "m", EmbeddingModels: []string{"e"}, ModelPricing: map[string]config.ModelPrice{"e": {Input: 0.1}}}},
		{name: "free", cfg: config.LLMConfig{Model: "m", ModelPricing: map[string]config.ModelPrice{"m": {}}}},
		{name: "unknown model", cfg: config.LLMConfig{Model: "m", ModelPricing: map[string]config.ModelPrice{"x": {}}}, want: "does not match a model"},
		{name: "negative", cfg: config.LLMConfig{Model: "m", ModelPricing: map[string]config.ModelPrice{"m": {Output: -1}}}, want: "m.output must be a non-negative"},
		{name: "absurd", cfg: config.LLMConfig{Model: "m", ModelPricing: map[string]config.ModelPrice{"m": {CacheWrite: 1e9}}}, want: "cache_write must be at most"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateModelPricing(tt.cfg)
			if tt.want == "" && got != "" || tt.want != "" && !strings.Contains(got, tt.want) {
				t.Fatalf("validateModelPricing = %q, want %q", got, tt.want)
			}
		})
	}
}
