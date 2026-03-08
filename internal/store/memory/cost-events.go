package memory

import (
	"context"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

func (m *Memory) RecordCostEvent(_ context.Context, event service.CostEvent) error {
	now := time.Now().UTC().Format(time.RFC3339)

	event.ID = ulid.Make().String()
	event.CreatedAt = now

	m.mu.Lock()
	m.costEvents = append(m.costEvents, event)
	m.mu.Unlock()

	return nil
}

func (m *Memory) ListCostEvents(_ context.Context, q *query.Query) (*service.ListResult[service.CostEvent], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.CostEvent, len(m.costEvents))
	copy(result, m.costEvents)

	// Sort by created_at descending (newest first).
	slices.SortFunc(result, func(a, b service.CostEvent) int {
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

func (m *Memory) GetCostByAgent(_ context.Context, agentID string) (float64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var total float64
	for _, e := range m.costEvents {
		if e.AgentID == agentID {
			total += e.CostCents
		}
	}

	return total, nil
}

func (m *Memory) GetCostByProject(_ context.Context, projectID string) (float64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var total float64
	for _, e := range m.costEvents {
		if e.ProjectID == projectID {
			total += e.CostCents
		}
	}

	return total, nil
}

func (m *Memory) GetCostByGoal(_ context.Context, goalID string) (float64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var total float64
	for _, e := range m.costEvents {
		if e.GoalID == goalID {
			total += e.CostCents
		}
	}

	return total, nil
}

func (m *Memory) GetCostByBillingCode(_ context.Context, billingCode string) (float64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var total float64
	for _, e := range m.costEvents {
		if e.BillingCode == billingCode {
			total += e.CostCents
		}
	}

	return total, nil
}
