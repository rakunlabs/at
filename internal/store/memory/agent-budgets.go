package memory

import (
	"context"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

func (m *Memory) GetAgentBudget(_ context.Context, agentID string) (*service.AgentBudget, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	b, ok := m.agentBudgets[agentID]
	if !ok {
		return nil, nil
	}

	return &b, nil
}

func (m *Memory) SetAgentBudget(_ context.Context, budget service.AgentBudget) error {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.agentBudgets[budget.AgentID]; ok {
		existing.MonthlyLimit = budget.MonthlyLimit
		existing.CurrentSpend = budget.CurrentSpend
		existing.PeriodStart = budget.PeriodStart
		existing.PeriodEnd = budget.PeriodEnd
		existing.UpdatedAt = now
		m.agentBudgets[budget.AgentID] = existing
	} else {
		budget.ID = ulid.Make().String()
		budget.CreatedAt = now
		budget.UpdatedAt = now
		m.agentBudgets[budget.AgentID] = budget
	}

	return nil
}

func (m *Memory) RecordAgentUsage(_ context.Context, usage service.AgentUsageRecord) error {
	now := time.Now().UTC().Format(time.RFC3339)

	usage.ID = ulid.Make().String()
	usage.CreatedAt = now

	m.mu.Lock()
	m.agentUsage = append(m.agentUsage, usage)
	m.mu.Unlock()

	return nil
}

func (m *Memory) GetAgentUsage(_ context.Context, agentID string, q *query.Query) (*service.ListResult[service.AgentUsageRecord], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.AgentUsageRecord
	for _, u := range m.agentUsage {
		if u.AgentID == agentID {
			result = append(result, u)
		}
	}

	// Sort by created_at descending (newest first).
	slices.SortFunc(result, func(a, b service.AgentUsageRecord) int {
		if a.CreatedAt > b.CreatedAt {
			return -1
		}
		if a.CreatedAt < b.CreatedAt {
			return 1
		}
		return 0
	})

	return paginate(result, q), nil
}

func (m *Memory) GetAgentTotalSpend(_ context.Context, agentID string) (float64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var total float64
	for _, u := range m.agentUsage {
		if u.AgentID == agentID {
			total += u.EstimatedCost
		}
	}

	return total, nil
}

func (m *Memory) ListModelPricing(_ context.Context) ([]service.ModelPricing, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.ModelPricing, 0, len(m.modelPricing))
	for _, p := range m.modelPricing {
		result = append(result, p)
	}

	slices.SortFunc(result, func(a, b service.ModelPricing) int {
		keyA := a.ProviderKey + "::" + a.Model
		keyB := b.ProviderKey + "::" + b.Model
		if keyA < keyB {
			return -1
		}
		if keyA > keyB {
			return 1
		}
		return 0
	})

	return result, nil
}

func (m *Memory) SetModelPricing(_ context.Context, pricing service.ModelPricing) error {
	now := time.Now().UTC().Format(time.RFC3339)
	mapKey := pricing.ProviderKey + "::" + pricing.Model

	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.modelPricing[mapKey]; ok {
		existing.PromptPricePer1M = pricing.PromptPricePer1M
		existing.CompletionPricePer1M = pricing.CompletionPricePer1M
		existing.UpdatedAt = now
		m.modelPricing[mapKey] = existing
	} else {
		pricing.ID = ulid.Make().String()
		pricing.CreatedAt = now
		pricing.UpdatedAt = now
		m.modelPricing[mapKey] = pricing
	}

	return nil
}
