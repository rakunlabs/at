package server

import (
	"context"
	"log/slog"
	"slices"
	"sort"
	"strings"

	"github.com/rakunlabs/ada/middleware/auth/identity"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/gateway/wire"
	"github.com/rakunlabs/at/internal/service"
)

type userUsageKey struct{}

type userUsageAttribution struct {
	userID string
	source string
}

// Browser accounting trusts the authenticated principal, never request.user or
// metadata. Token gateway traffic remains token-attributed.
func withUserUsage(ctx context.Context, source string) context.Context {
	userID := ""
	if a, ok := service.AccessPrincipalFromContext(ctx); ok {
		userID = a.UserID
	} else if id := identity.FromContext(ctx); id != nil {
		userID = id.Subject
	}
	return context.WithValue(ctx, userUsageKey{}, userUsageAttribution{userID: userID, source: source})
}

func (s *Server) estimateGatewayUsageCostCents(ctx context.Context, providerKey, actualModel, fullModel string, usage service.Usage) float64 {
	cost, _ := s.estimateGatewayUsageCost(ctx, providerKey, actualModel, fullModel, usage)
	return cost
}

func (s *Server) estimateGatewayUsageCost(ctx context.Context, providerKey, actualModel, fullModel string, usage service.Usage) (float64, bool) {
	if s.agentBudgetStore == nil {
		return 0, false
	}
	pricingList, err := s.modelPricingFor(ctx, pricingWorkspaceID(ctx), providerKey)
	if err != nil {
		return 0, false
	}
	return estimateUsageCost(pricingList, providerKey, actualModel, fullModel, usage)
}

type pricingWorkspaceKey struct{}

// withPricingWorkspace names the workspace whose providers price a call when
// the context carries no principal, e.g. a gateway request authenticated by
// an API token.
func withPricingWorkspace(ctx context.Context, workspaceID string) context.Context {
	if workspaceID == "" {
		return ctx
	}
	return context.WithValue(ctx, pricingWorkspaceKey{}, workspaceID)
}

func pricingWorkspaceID(ctx context.Context) string {
	if id, ok := ctx.Value(pricingWorkspaceKey{}).(string); ok && id != "" {
		return id
	}
	if p, _, ok := service.ExecutionFromContext(ctx); ok && p.WorkspaceID != "" {
		return p.WorkspaceID
	}
	if a, ok := service.AccessPrincipalFromContext(ctx); ok && a.WorkspaceID != "" {
		return a.WorkspaceID
	}
	if token := gatewayTokenFromContext(ctx); token != nil {
		return token.WorkspaceID
	}
	return service.DefaultWorkspaceID
}

// modelPricingFor returns the installation price table followed by the prices
// the named providers declare in their own config. Installation rows come
// first, so an administrator's price for a provider and model always wins;
// a provider's own price beats only an installation-wide ("") row.
func (s *Server) modelPricingFor(ctx context.Context, workspaceID string, providerKeys ...string) ([]service.ModelPricing, error) {
	pricingList, err := s.agentBudgetStore.ListModelPricing(ctx)
	if err != nil {
		return nil, err
	}
	prices, ok := s.store.(service.ProviderPriceStorer)
	if !ok || len(providerKeys) == 0 {
		return pricingList, nil
	}
	declared, err := prices.ProviderModelPrices(ctx, workspaceID, providerKeys)
	if err != nil {
		// Accounting must not fail a call; fall back to installation prices.
		slog.Warn("load provider model prices failed", "error", err)
		return pricingList, nil
	}
	return appendProviderPrices(pricingList, declared), nil
}

func appendProviderPrices(pricingList []service.ModelPricing, declared map[string]map[string]config.ModelPrice) []service.ModelPricing {
	keys := make([]string, 0, len(declared))
	for key := range declared {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := slices.Clip(pricingList)
	for _, key := range keys {
		models := make([]string, 0, len(declared[key]))
		for model := range declared[key] {
			models = append(models, model)
		}
		sort.Strings(models)
		for _, model := range models {
			price := declared[key][model]
			out = append(out, service.ModelPricing{
				ProviderKey:          key,
				Model:                model,
				PromptPricePer1M:     price.Input,
				CompletionPricePer1M: price.Output,
				CacheReadPricePer1M:  price.CacheRead,
				CacheWritePricePer1M: price.CacheWrite,
				Source:               service.ModelPricingSourceProvider,
			})
		}
	}
	return out
}

func estimateUsageCostCents(pricingList []service.ModelPricing, providerKey, actualModel, fullModel string, usage service.Usage) float64 {
	cost, _ := estimateUsageCost(pricingList, providerKey, actualModel, fullModel, usage)
	return cost
}

func estimateUsageCost(pricingList []service.ModelPricing, providerKey, actualModel, fullModel string, usage service.Usage) (float64, bool) {
	pricing, ok := findModelPricing(pricingList, providerKey, actualModel, fullModel)
	if !ok {
		return 0, false
	}

	dollars := (float64(usage.PromptTokens) * pricing.PromptPricePer1M / 1_000_000) +
		(float64(usage.CompletionTokens) * pricing.CompletionPricePer1M / 1_000_000) +
		(float64(usage.CacheReadTokens) * pricing.CacheReadPricePer1M / 1_000_000) +
		(float64(usage.CacheWriteTokens) * pricing.CacheWritePricePer1M / 1_000_000)
	if dollars <= 0 {
		return 0, true
	}
	return dollars * 100, true
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

// chatsStreamUsage is the usage chunk of a stream, plus the call's priced cost
// when the request is the browser Chats workbench. Gateway clients keep the
// plain OpenAI shape.
func (s *Server) chatsStreamUsage(ctx context.Context, providerKey, actualModel, fullModel string, usage service.Usage) *wire.ChatCompletionUsage {
	out := wire.ChatCompletionUsagePtrFromService(usage)
	if actor, ok := ctx.Value(userUsageKey{}).(userUsageAttribution); !ok || actor.source != "chats" {
		return out
	}
	if cost, ok := s.estimateGatewayUsageCost(context.WithoutCancel(ctx), providerKey, actualModel, fullModel, usage); ok {
		out.AtCostCents = &cost
	}

	return out
}
