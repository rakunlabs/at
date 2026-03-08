package memory

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

func (m *Memory) ListTasks(_ context.Context, q *query.Query) (*service.ListResult[service.Task], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		result = append(result, t)
	}

	slices.SortFunc(result, func(a, b service.Task) int {
		if a.Title < b.Title {
			return -1
		}
		if a.Title > b.Title {
			return 1
		}
		return 0
	})

	return paginate(result, q), nil
}

func (m *Memory) GetTask(_ context.Context, id string) (*service.Task, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, ok := m.tasks[id]
	if !ok {
		return nil, nil
	}

	return &t, nil
}

func (m *Memory) CreateTask(_ context.Context, task service.Task) (*service.Task, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.Task{
		ID:              id,
		OrganizationID:  task.OrganizationID,
		ProjectID:       task.ProjectID,
		GoalID:          task.GoalID,
		ParentID:        task.ParentID,
		AssignedAgentID: task.AssignedAgentID,
		Identifier:      task.Identifier,
		Title:           task.Title,
		Description:     task.Description,
		Status:          task.Status,
		PriorityLevel:   task.PriorityLevel,
		Priority:        task.Priority,
		Result:          task.Result,
		BillingCode:     task.BillingCode,
		RequestDepth:    task.RequestDepth,
		CreatedAt:       now,
		UpdatedAt:       now,
		CreatedBy:       task.CreatedBy,
		UpdatedBy:       task.UpdatedBy,
	}

	m.mu.Lock()
	m.tasks[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateTask(_ context.Context, id string, task service.Task) (*service.Task, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.tasks[id]
	if !ok {
		return nil, nil
	}

	existing.OrganizationID = task.OrganizationID
	existing.ProjectID = task.ProjectID
	existing.GoalID = task.GoalID
	existing.ParentID = task.ParentID
	existing.AssignedAgentID = task.AssignedAgentID
	existing.Identifier = task.Identifier
	existing.Title = task.Title
	existing.Description = task.Description
	existing.Status = task.Status
	existing.PriorityLevel = task.PriorityLevel
	existing.Priority = task.Priority
	existing.Result = task.Result
	existing.BillingCode = task.BillingCode
	existing.RequestDepth = task.RequestDepth
	existing.StartedAt = task.StartedAt
	existing.CompletedAt = task.CompletedAt
	existing.CancelledAt = task.CancelledAt
	existing.HiddenAt = task.HiddenAt
	existing.UpdatedAt = now
	existing.UpdatedBy = task.UpdatedBy
	m.tasks[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteTask(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.tasks, id)
	m.mu.Unlock()

	return nil
}

func (m *Memory) ListTasksByAgent(_ context.Context, agentID string) ([]service.Task, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.Task
	for _, t := range m.tasks {
		if t.AssignedAgentID == agentID {
			result = append(result, t)
		}
	}

	slices.SortFunc(result, func(a, b service.Task) int {
		if a.Title < b.Title {
			return -1
		}
		if a.Title > b.Title {
			return 1
		}
		return 0
	})

	return result, nil
}

func (m *Memory) ListTasksByGoal(_ context.Context, goalID string) ([]service.Task, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.Task
	for _, t := range m.tasks {
		if t.GoalID == goalID {
			result = append(result, t)
		}
	}

	slices.SortFunc(result, func(a, b service.Task) int {
		if a.Title < b.Title {
			return -1
		}
		if a.Title > b.Title {
			return 1
		}
		return 0
	})

	return result, nil
}

func (m *Memory) CheckoutTask(_ context.Context, taskID, agentID string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.tasks[taskID]
	if !ok {
		return nil
	}

	if existing.CheckedOutBy != "" && existing.CheckedOutBy != agentID {
		return fmt.Errorf("task %q is already checked out by agent %q", taskID, existing.CheckedOutBy)
	}

	existing.CheckedOutBy = agentID
	existing.CheckedOutAt = now
	m.tasks[taskID] = existing

	return nil
}

func (m *Memory) ReleaseTask(_ context.Context, taskID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.tasks[taskID]
	if !ok {
		return nil
	}

	existing.CheckedOutBy = ""
	existing.CheckedOutAt = ""
	m.tasks[taskID] = existing

	return nil
}
