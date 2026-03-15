package memory

import (
	"context"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
)

// ─── Organization Agent Membership CRUD ───

func (m *Memory) ListOrganizationAgents(_ context.Context, orgID string) ([]service.OrganizationAgent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.OrganizationAgent
	for _, oa := range m.organizationAgents {
		if oa.OrganizationID == orgID {
			result = append(result, oa)
		}
	}

	slices.SortFunc(result, func(a, b service.OrganizationAgent) int {
		if a.CreatedAt < b.CreatedAt {
			return -1
		}
		if a.CreatedAt > b.CreatedAt {
			return 1
		}
		return 0
	})

	return result, nil
}

func (m *Memory) ListAgentOrganizations(_ context.Context, agentID string) ([]service.OrganizationAgent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.OrganizationAgent
	for _, oa := range m.organizationAgents {
		if oa.AgentID == agentID {
			result = append(result, oa)
		}
	}

	slices.SortFunc(result, func(a, b service.OrganizationAgent) int {
		if a.CreatedAt < b.CreatedAt {
			return -1
		}
		if a.CreatedAt > b.CreatedAt {
			return 1
		}
		return 0
	})

	return result, nil
}

func (m *Memory) GetOrganizationAgent(_ context.Context, id string) (*service.OrganizationAgent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	oa, ok := m.organizationAgents[id]
	if !ok {
		return nil, nil
	}

	return &oa, nil
}

func (m *Memory) GetOrganizationAgentByPair(_ context.Context, orgID, agentID string) (*service.OrganizationAgent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := orgID + "::" + agentID
	id, ok := m.orgAgentIndex[key]
	if !ok {
		return nil, nil
	}

	oa, ok := m.organizationAgents[id]
	if !ok {
		return nil, nil
	}

	return &oa, nil
}

func (m *Memory) CreateOrganizationAgent(_ context.Context, oa service.OrganizationAgent) (*service.OrganizationAgent, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	status := oa.Status
	if status == "" {
		status = "active"
	}

	rec := service.OrganizationAgent{
		ID:                id,
		OrganizationID:    oa.OrganizationID,
		AgentID:           oa.AgentID,
		Role:              oa.Role,
		Title:             oa.Title,
		ParentAgentID:     oa.ParentAgentID,
		Status:            status,
		HeartbeatSchedule: oa.HeartbeatSchedule,
		MemoryModel:       oa.MemoryModel,
		MemoryProvider:    oa.MemoryProvider,
		MemoryEnabled:     oa.MemoryEnabled,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	m.mu.Lock()
	m.organizationAgents[id] = rec
	m.orgAgentIndex[oa.OrganizationID+"::"+oa.AgentID] = id
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateOrganizationAgent(_ context.Context, id string, oa service.OrganizationAgent) (*service.OrganizationAgent, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.organizationAgents[id]
	if !ok {
		return nil, nil
	}

	existing.Role = oa.Role
	existing.Title = oa.Title
	existing.ParentAgentID = oa.ParentAgentID
	existing.Status = oa.Status
	existing.HeartbeatSchedule = oa.HeartbeatSchedule
	existing.MemoryModel = oa.MemoryModel
	existing.MemoryProvider = oa.MemoryProvider
	existing.MemoryEnabled = oa.MemoryEnabled
	existing.UpdatedAt = now
	m.organizationAgents[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteOrganizationAgent(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	oa, ok := m.organizationAgents[id]
	if ok {
		delete(m.orgAgentIndex, oa.OrganizationID+"::"+oa.AgentID)
		delete(m.organizationAgents, id)
	}

	return nil
}

func (m *Memory) DeleteOrganizationAgentByPair(_ context.Context, orgID, agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := orgID + "::" + agentID
	id, ok := m.orgAgentIndex[key]
	if ok {
		delete(m.organizationAgents, id)
		delete(m.orgAgentIndex, key)
	}

	return nil
}
