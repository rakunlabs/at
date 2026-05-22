package server

import (
	"context"
	"testing"
	"time"

	"github.com/rakunlabs/query"
	"github.com/worldline-go/types"

	"github.com/rakunlabs/at/internal/service"
)

func TestEstimateUsageCostCentsIncludesCachePricing(t *testing.T) {
	got := estimateUsageCostCents([]service.ModelPricing{{
		ProviderKey:          "anthropic",
		Model:                "claude-sonnet-4-5",
		PromptPricePer1M:     1,
		CompletionPricePer1M: 2,
		CacheReadPricePer1M:  0.5,
		CacheWritePricePer1M: 3,
	}}, "anthropic", "claude-sonnet-4-5", "anthropic/claude-sonnet-4-5", service.Usage{
		PromptTokens:     1000,
		CompletionTokens: 2000,
		CacheReadTokens:  3000,
		CacheWriteTokens: 4000,
	})

	want := 1.85
	if got < want-0.000001 || got > want+0.000001 {
		t.Fatalf("cost cents = %f, want %f", got, want)
	}
}

func TestEstimateUsageCostCentsMissingPricingZero(t *testing.T) {
	got := estimateUsageCostCents(nil, "anthropic", "claude", "anthropic/claude", service.Usage{
		PromptTokens:     1000,
		CompletionTokens: 1000,
		CacheReadTokens:  1000,
		CacheWriteTokens: 1000,
	})
	if got != 0 {
		t.Fatalf("cost cents = %f, want 0", got)
	}
}

func TestCheckTokenLimitsSpendExceeded(t *testing.T) {
	s := &Server{costEventStore: &budgetCostEventStore{spend: 501}}
	token := &service.APIToken{
		ID:              "tok_1",
		SpendLimitCents: types.NewNull(500.0),
		CreatedAt:       types.NewTime(time.Now().UTC().Add(-time.Hour)),
	}

	msg, err := s.checkTokenLimits(context.Background(), &authResult{token: token})
	if err != nil {
		t.Fatalf("checkTokenLimits: %v", err)
	}
	if msg != "token spend limit exceeded" {
		t.Fatalf("message = %q, want spend exceeded", msg)
	}
}

func TestCheckTokenLimitsResetIntervalReopensSpendBudget(t *testing.T) {
	usageStore := &budgetTokenUsageStore{}
	s := &Server{
		tokenUsageStore: usageStore,
		costEventStore:  &budgetCostEventStore{spend: 501},
	}
	token := &service.APIToken{
		ID:                 "tok_1",
		SpendLimitCents:    types.NewNull(500.0),
		LimitResetInterval: types.NewNull("1h"),
		CreatedAt:          types.NewTime(time.Now().UTC().Add(-2 * time.Hour)),
	}

	msg, err := s.checkTokenLimits(context.Background(), &authResult{token: token})
	if err != nil {
		t.Fatalf("checkTokenLimits: %v", err)
	}
	if msg != "" {
		t.Fatalf("message = %q, want allowed after reset", msg)
	}
	if usageStore.resetTokenID != token.ID {
		t.Fatalf("reset token id = %q, want %q", usageStore.resetTokenID, token.ID)
	}
}

type budgetTokenUsageStore struct {
	total        int64
	resetTokenID string
}

func (b *budgetTokenUsageStore) RecordUsage(context.Context, string, string, service.Usage) error {
	return nil
}

func (b *budgetTokenUsageStore) GetTokenUsage(context.Context, string) ([]service.TokenUsage, error) {
	return nil, nil
}

func (b *budgetTokenUsageStore) GetTokenTotalUsage(context.Context, string) (int64, error) {
	return b.total, nil
}

func (b *budgetTokenUsageStore) ResetTokenUsage(_ context.Context, tokenID string) error {
	b.resetTokenID = tokenID
	return nil
}

type budgetCostEventStore struct {
	spend float64
}

func (b *budgetCostEventStore) RecordCostEvent(context.Context, service.CostEvent) error { return nil }

func (b *budgetCostEventStore) ListCostEvents(context.Context, *query.Query) (*service.ListResult[service.CostEvent], error) {
	return nil, nil
}

func (b *budgetCostEventStore) GetCostByAgent(context.Context, string) (float64, error) {
	return b.spend, nil
}

func (b *budgetCostEventStore) GetCostByAgentSince(context.Context, string, string) (float64, error) {
	return b.spend, nil
}

func (b *budgetCostEventStore) GetCostByProject(context.Context, string) (float64, error) {
	return 0, nil
}

func (b *budgetCostEventStore) GetCostByGoal(context.Context, string) (float64, error) {
	return 0, nil
}

func (b *budgetCostEventStore) GetCostByBillingCode(context.Context, string) (float64, error) {
	return 0, nil
}

func (b *budgetCostEventStore) GetCostByTasks(context.Context, []string) (service.CostByTasksResult, error) {
	return service.CostByTasksResult{}, nil
}

func (b *budgetCostEventStore) GetUsageSummary(context.Context, service.UsageFilter) (service.UsageSummary, error) {
	return service.UsageSummary{}, nil
}

func (b *budgetCostEventStore) GetUsageGrouped(context.Context, service.UsageFilter, string, int) ([]service.UsageSummary, error) {
	return nil, nil
}

func (b *budgetCostEventStore) GetUsageTimeSeries(context.Context, service.UsageFilter, string) ([]service.UsageTimeSeriesPoint, error) {
	return nil, nil
}
