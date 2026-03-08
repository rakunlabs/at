package memory

import (
	"context"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
)

func (m *Memory) ListLabels(_ context.Context, orgID string) ([]service.Label, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.Label
	for _, l := range m.labels {
		if l.OrganizationID == orgID {
			result = append(result, l)
		}
	}

	slices.SortFunc(result, func(a, b service.Label) int {
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

func (m *Memory) GetLabel(_ context.Context, id string) (*service.Label, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	l, ok := m.labels[id]
	if !ok {
		return nil, nil
	}

	return &l, nil
}

func (m *Memory) CreateLabel(_ context.Context, label service.Label) (*service.Label, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.Label{
		ID:             id,
		OrganizationID: label.OrganizationID,
		Name:           label.Name,
		Color:          label.Color,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	m.mu.Lock()
	m.labels[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateLabel(_ context.Context, id string, label service.Label) (*service.Label, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.labels[id]
	if !ok {
		return nil, nil
	}

	existing.Name = label.Name
	existing.Color = label.Color
	existing.UpdatedAt = now
	m.labels[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteLabel(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.labels, id)

	// Remove this label from all task-label associations.
	for taskID, labelSet := range m.taskLabels {
		delete(labelSet, id)
		if len(labelSet) == 0 {
			delete(m.taskLabels, taskID)
		}
	}

	return nil
}

// ─── Task-Label Associations ───

func (m *Memory) AddLabelToTask(_ context.Context, taskID, labelID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.taskLabels[taskID] == nil {
		m.taskLabels[taskID] = make(map[string]bool)
	}
	m.taskLabels[taskID][labelID] = true

	return nil
}

func (m *Memory) RemoveLabelFromTask(_ context.Context, taskID, labelID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if labelSet, ok := m.taskLabels[taskID]; ok {
		delete(labelSet, labelID)
		if len(labelSet) == 0 {
			delete(m.taskLabels, taskID)
		}
	}

	return nil
}

func (m *Memory) ListLabelsForTask(_ context.Context, taskID string) ([]service.Label, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.Label
	labelSet := m.taskLabels[taskID]
	for labelID := range labelSet {
		if l, ok := m.labels[labelID]; ok {
			result = append(result, l)
		}
	}

	slices.SortFunc(result, func(a, b service.Label) int {
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

func (m *Memory) ListTasksForLabel(_ context.Context, labelID string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []string
	for taskID, labelSet := range m.taskLabels {
		if labelSet[labelID] {
			result = append(result, taskID)
		}
	}

	slices.Sort(result)

	return result, nil
}
