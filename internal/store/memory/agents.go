package memory

import (
	"context"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
)

func (m *Memory) ListAgents(_ context.Context) ([]service.Agent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.Agent, 0, len(m.agents))
	for _, a := range m.agents {
		result = append(result, a)
	}

	slices.SortFunc(result, func(a, b service.Agent) int {
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

func (m *Memory) GetAgent(_ context.Context, id string) (*service.Agent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	a, ok := m.agents[id]
	if !ok {
		return nil, nil
	}

	return &a, nil
}

func (m *Memory) CreateAgent(_ context.Context, agent service.Agent) (*service.Agent, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.Agent{
		ID:            id,
		Name:          agent.Name,
		Description:   agent.Description,
		Provider:      agent.Provider,
		Model:         agent.Model,
		SystemPrompt:  agent.SystemPrompt,
		Skills:        agent.Skills,
		MCPs:          agent.MCPs,
		MaxIterations: agent.MaxIterations,
		ToolTimeout:   agent.ToolTimeout,
		CreatedAt:     now,
		UpdatedAt:     now,
		CreatedBy:     agent.CreatedBy,
		UpdatedBy:     agent.UpdatedBy,
	}

	m.mu.Lock()
	m.agents[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateAgent(_ context.Context, id string, agent service.Agent) (*service.Agent, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.agents[id]
	if !ok {
		return nil, nil
	}

	existing.Name = agent.Name
	existing.Description = agent.Description
	existing.Provider = agent.Provider
	existing.Model = agent.Model
	existing.SystemPrompt = agent.SystemPrompt
	existing.Skills = agent.Skills
	existing.MCPs = agent.MCPs
	existing.MaxIterations = agent.MaxIterations
	existing.ToolTimeout = agent.ToolTimeout
	existing.UpdatedAt = now
	existing.UpdatedBy = agent.UpdatedBy

	m.agents[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteAgent(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.agents, id)
	m.mu.Unlock()

	return nil
}
