package memory

import (
	"context"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

func (m *Memory) ListGoals(_ context.Context, q *query.Query) (*service.ListResult[service.Goal], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.Goal, 0, len(m.goals))
	for _, g := range m.goals {
		result = append(result, g)
	}

	slices.SortFunc(result, func(a, b service.Goal) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})

	return paginate(result, q), nil
}

func (m *Memory) GetGoal(_ context.Context, id string) (*service.Goal, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	g, ok := m.goals[id]
	if !ok {
		return nil, nil
	}

	return &g, nil
}

func (m *Memory) CreateGoal(_ context.Context, goal service.Goal) (*service.Goal, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.Goal{
		ID:             id,
		OrganizationID: goal.OrganizationID,
		ParentGoalID:   goal.ParentGoalID,
		Name:           goal.Name,
		Description:    goal.Description,
		Status:         goal.Status,
		Priority:       goal.Priority,
		CreatedAt:      now,
		UpdatedAt:      now,
		CreatedBy:      goal.CreatedBy,
		UpdatedBy:      goal.UpdatedBy,
	}

	m.mu.Lock()
	m.goals[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateGoal(_ context.Context, id string, goal service.Goal) (*service.Goal, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.goals[id]
	if !ok {
		return nil, nil
	}

	existing.OrganizationID = goal.OrganizationID
	existing.ParentGoalID = goal.ParentGoalID
	existing.Name = goal.Name
	existing.Description = goal.Description
	existing.Status = goal.Status
	existing.Priority = goal.Priority
	existing.UpdatedAt = now
	existing.UpdatedBy = goal.UpdatedBy
	m.goals[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteGoal(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.goals, id)
	m.mu.Unlock()

	return nil
}

func (m *Memory) ListGoalsByParent(_ context.Context, parentID string) ([]service.Goal, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.Goal
	for _, g := range m.goals {
		if g.ParentGoalID == parentID {
			result = append(result, g)
		}
	}

	slices.SortFunc(result, func(a, b service.Goal) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})

	return result, nil
}

func (m *Memory) GetGoalAncestry(_ context.Context, id string) ([]service.Goal, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var chain []service.Goal
	seen := make(map[string]bool)
	current := id

	for current != "" {
		if seen[current] {
			break // prevent infinite loops on circular references
		}
		seen[current] = true

		g, ok := m.goals[current]
		if !ok {
			break
		}

		chain = append(chain, g)
		current = g.ParentGoalID
	}

	return chain, nil
}
