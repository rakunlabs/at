package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestModelPricingTools(t *testing.T) {
	ctx := context.Background()

	t.Run("set keeps omitted prices and marks override", func(t *testing.T) {
		store := &pricingTestBudgetStore{pricing: []service.ModelPricing{{
			ID: "p1", ProviderKey: "anthropic", Model: "claude-opus-5-5",
			PromptPricePer1M: 5, CompletionPricePer1M: 25, CacheReadPricePer1M: 0.5, CacheWritePricePer1M: 6.25,
			Source: atPricingSource,
		}}}
		s := &Server{agentBudgetStore: store}
		if _, err := s.execModelPricingSet(ctx, map[string]any{"provider_key": "anthropic", "model": "claude-opus-5-5", "input": 4.0, "output": 20.0}); err != nil {
			t.Fatal(err)
		}
		if len(store.set) != 1 {
			t.Fatalf("set calls = %d", len(store.set))
		}
		got := store.set[0]
		if got.PromptPricePer1M != 4 || got.CompletionPricePer1M != 20 || got.CacheReadPricePer1M != 0.5 || got.CacheWritePricePer1M != 6.25 {
			t.Fatalf("prices = %+v", got)
		}
		if !got.ManualOverride || got.Source != "" {
			t.Fatalf("manual set must be an override that keeps the synced source: %+v", got)
		}
	})

	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"missing provider", map[string]any{"model": "m", "input": 1.0}, "provider_key is required"},
		{"no prices", map[string]any{"provider_key": "p", "model": "m"}, "at least one"},
		{"negative", map[string]any{"provider_key": "p", "model": "m", "input": -1.0}, "non-negative"},
		{"not a number", map[string]any{"provider_key": "p", "model": "m", "input": "4"}, "must be a number"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{agentBudgetStore: &pricingTestBudgetStore{}}
			if _, err := s.execModelPricingSet(ctx, tt.args); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}

	t.Run("list filters", func(t *testing.T) {
		s := &Server{agentBudgetStore: &pricingTestBudgetStore{pricing: []service.ModelPricing{
			{ProviderKey: "a", Model: "claude-opus-5-5"},
			{ProviderKey: "a", Model: "claude-sonnet-5-5"},
			{ProviderKey: "b", Model: "claude-opus-5-5"},
		}}}
		out, err := s.execModelPricingList(ctx, map[string]any{"provider_key": "a", "model": "OPUS"})
		if err != nil {
			t.Fatal(err)
		}
		var res struct {
			Total int `json:"total"`
		}
		if err := json.Unmarshal([]byte(out), &res); err != nil || res.Total != 1 {
			t.Fatalf("list = %s, %v", out, err)
		}
	})

	t.Run("delete and reset resolve by key", func(t *testing.T) {
		s := &Server{agentBudgetStore: &pricingTestBudgetStore{pricing: []service.ModelPricing{{ID: "p1", ProviderKey: "a", Model: "m"}}}}
		if _, err := s.execModelPricingDelete(ctx, map[string]any{"provider_key": "a", "model": "m"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.execModelPricingDelete(ctx, map[string]any{"provider_key": "a", "model": "missing"}); err == nil {
			t.Fatal("expected not-found error")
		}
		if _, err := s.execModelPricingReset(ctx, map[string]any{"id": "p1"}); err == nil || !strings.Contains(err.Error(), "no synced source") {
			t.Fatalf("reset without source: %v", err)
		}
	})
}
