package server

import (
	"context"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

func (s *Server) estimateGatewayUsageCostCents(ctx context.Context, providerKey, actualModel, fullModel string, usage service.Usage) float64 {
	if s.agentBudgetStore == nil {
		return 0
	}
	pricingList, err := s.agentBudgetStore.ListModelPricing(ctx)
	if err != nil {
		return 0
	}
	return estimateUsageCostCents(pricingList, providerKey, actualModel, fullModel, usage)
}

func estimateUsageCostCents(pricingList []service.ModelPricing, providerKey, actualModel, fullModel string, usage service.Usage) float64 {
	pricing, ok := findModelPricing(pricingList, providerKey, actualModel, fullModel)
	if !ok {
		return 0
	}

	dollars := (float64(usage.PromptTokens) * pricing.PromptPricePer1M / 1_000_000) +
		(float64(usage.CompletionTokens) * pricing.CompletionPricePer1M / 1_000_000) +
		(float64(usage.CacheReadTokens) * pricing.CacheReadPricePer1M / 1_000_000) +
		(float64(usage.CacheWriteTokens) * pricing.CacheWritePricePer1M / 1_000_000)
	if dollars <= 0 {
		return 0
	}
	return dollars * 100
}

func findModelPricing(pricingList []service.ModelPricing, providerKey, actualModel, fullModel string) (service.ModelPricing, bool) {
	for _, p := range pricingList {
		if p.ProviderKey == providerKey && pricingModelMatches(p.Model, actualModel, fullModel) {
			return p, true
		}
	}
	for _, p := range pricingList {
		if p.ProviderKey == "" && pricingModelMatches(p.Model, actualModel, fullModel) {
			return p, true
		}
	}
	if providerKey == "" {
		for _, p := range pricingList {
			if pricingModelMatches(p.Model, actualModel, fullModel) {
				return p, true
			}
		}
	}
	return service.ModelPricing{}, false
}

func pricingModelMatches(pricingModel, actualModel, fullModel string) bool {
	if pricingModel == actualModel || pricingModel == fullModel {
		return true
	}
	if fullModel != "" && strings.HasSuffix(fullModel, "/"+pricingModel) {
		return true
	}
	return false
}
