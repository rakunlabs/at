package memory

import (
	"context"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

func (m *Memory) ListProjects(_ context.Context, q *query.Query) (*service.ListResult[service.Project], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.Project, 0, len(m.projects))
	for _, p := range m.projects {
		result = append(result, p)
	}

	slices.SortFunc(result, func(a, b service.Project) int {
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

func (m *Memory) GetProject(_ context.Context, id string) (*service.Project, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.projects[id]
	if !ok {
		return nil, nil
	}

	return &p, nil
}

func (m *Memory) CreateProject(_ context.Context, project service.Project) (*service.Project, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.Project{
		ID:             id,
		OrganizationID: project.OrganizationID,
		GoalID:         project.GoalID,
		LeadAgentID:    project.LeadAgentID,
		Name:           project.Name,
		Description:    project.Description,
		Status:         project.Status,
		Color:          project.Color,
		TargetDate:     project.TargetDate,
		CreatedAt:      now,
		UpdatedAt:      now,
		CreatedBy:      project.CreatedBy,
		UpdatedBy:      project.UpdatedBy,
	}

	m.mu.Lock()
	m.projects[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateProject(_ context.Context, id string, project service.Project) (*service.Project, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.projects[id]
	if !ok {
		return nil, nil
	}

	existing.OrganizationID = project.OrganizationID
	existing.GoalID = project.GoalID
	existing.LeadAgentID = project.LeadAgentID
	existing.Name = project.Name
	existing.Description = project.Description
	existing.Status = project.Status
	existing.Color = project.Color
	existing.TargetDate = project.TargetDate
	existing.ArchivedAt = project.ArchivedAt
	existing.UpdatedAt = now
	existing.UpdatedBy = project.UpdatedBy
	m.projects[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteProject(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.projects, id)
	m.mu.Unlock()

	return nil
}

func (m *Memory) ListProjectsByGoal(_ context.Context, goalID string) ([]service.Project, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.Project
	for _, p := range m.projects {
		if p.GoalID == goalID {
			result = append(result, p)
		}
	}

	slices.SortFunc(result, func(a, b service.Project) int {
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

func (m *Memory) ListProjectsByOrganization(_ context.Context, orgID string) ([]service.Project, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.Project
	for _, p := range m.projects {
		if p.OrganizationID == orgID {
			result = append(result, p)
		}
	}

	slices.SortFunc(result, func(a, b service.Project) int {
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
