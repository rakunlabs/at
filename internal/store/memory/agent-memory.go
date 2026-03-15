package memory

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
)

// ─── Agent Memory CRUD ───

func (m *Memory) CreateAgentMemory(_ context.Context, mem service.AgentMemory) (*service.AgentMemory, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	// Round-trip tags through JSON to normalize.
	if mem.Tags != nil {
		raw, _ := json.Marshal(mem.Tags)
		var normalized []string
		_ = json.Unmarshal(raw, &normalized)
		mem.Tags = normalized
	}

	rec := service.AgentMemory{
		ID:             id,
		AgentID:        mem.AgentID,
		OrganizationID: mem.OrganizationID,
		TaskID:         mem.TaskID,
		TaskIdentifier: mem.TaskIdentifier,
		SummaryL0:      mem.SummaryL0,
		SummaryL1:      mem.SummaryL1,
		Tags:           mem.Tags,
		CreatedAt:      now,
	}

	m.mu.Lock()
	m.agentMemory[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) GetAgentMemory(_ context.Context, id string) (*service.AgentMemory, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mem, ok := m.agentMemory[id]
	if !ok {
		return nil, nil
	}

	return &mem, nil
}

func (m *Memory) ListAgentMemories(_ context.Context, agentID, orgID string) ([]service.AgentMemory, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.AgentMemory
	for _, mem := range m.agentMemory {
		if mem.AgentID == agentID && mem.OrganizationID == orgID {
			result = append(result, mem)
		}
	}

	// Sort by created_at descending (newest first).
	slices.SortFunc(result, func(a, b service.AgentMemory) int {
		if a.CreatedAt > b.CreatedAt {
			return -1
		}
		if a.CreatedAt < b.CreatedAt {
			return 1
		}
		return 0
	})

	return result, nil
}

func (m *Memory) ListOrgMemories(_ context.Context, orgID string) ([]service.AgentMemory, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.AgentMemory
	for _, mem := range m.agentMemory {
		if mem.OrganizationID == orgID {
			result = append(result, mem)
		}
	}

	// Sort by created_at descending (newest first).
	slices.SortFunc(result, func(a, b service.AgentMemory) int {
		if a.CreatedAt > b.CreatedAt {
			return -1
		}
		if a.CreatedAt < b.CreatedAt {
			return 1
		}
		return 0
	})

	return result, nil
}

func (m *Memory) SearchAgentMemories(_ context.Context, agentID, orgID, query string) ([]service.AgentMemory, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	queryLower := strings.ToLower(query)
	var result []service.AgentMemory

	for _, mem := range m.agentMemory {
		if mem.OrganizationID != orgID {
			continue
		}
		// If agentID is specified, filter by it.
		if agentID != "" && mem.AgentID != agentID {
			continue
		}
		// Match against L0, L1, and tags.
		if strings.Contains(strings.ToLower(mem.SummaryL0), queryLower) ||
			strings.Contains(strings.ToLower(mem.SummaryL1), queryLower) {
			result = append(result, mem)
			continue
		}
		for _, tag := range mem.Tags {
			if strings.Contains(strings.ToLower(tag), queryLower) {
				result = append(result, mem)
				break
			}
		}
	}

	// Sort by created_at descending.
	slices.SortFunc(result, func(a, b service.AgentMemory) int {
		if a.CreatedAt > b.CreatedAt {
			return -1
		}
		if a.CreatedAt < b.CreatedAt {
			return 1
		}
		return 0
	})

	return result, nil
}

func (m *Memory) DeleteAgentMemory(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.agentMemory, id)
	delete(m.agentMemoryMessages, id)

	return nil
}

func (m *Memory) GetAgentMemoryMessages(_ context.Context, memoryID string) (*service.AgentMemoryMessages, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	msgs, ok := m.agentMemoryMessages[memoryID]
	if !ok {
		return nil, nil
	}

	return &msgs, nil
}

func (m *Memory) CreateAgentMemoryMessages(_ context.Context, msgs service.AgentMemoryMessages) error {
	m.mu.Lock()
	m.agentMemoryMessages[msgs.MemoryID] = msgs
	m.mu.Unlock()

	return nil
}
