package memory

import (
	"context"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
)

func (m *Memory) GetAgentTaskSession(_ context.Context, agentID, taskKey string) (*service.AgentTaskSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mapKey := agentID + "::" + taskKey
	session, ok := m.agentTaskSessions[mapKey]
	if !ok {
		return nil, nil
	}

	return &session, nil
}

func (m *Memory) UpsertAgentTaskSession(_ context.Context, session service.AgentTaskSession) error {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	mapKey := session.AgentID + "::" + session.TaskKey

	if existing, ok := m.agentTaskSessions[mapKey]; ok {
		existing.AdapterType = session.AdapterType
		existing.SessionParamsJSON = session.SessionParamsJSON
		existing.SessionDisplayID = session.SessionDisplayID
		existing.UpdatedAt = now
		m.agentTaskSessions[mapKey] = existing
	} else {
		session.ID = ulid.Make().String()
		session.CreatedAt = now
		session.UpdatedAt = now
		m.agentTaskSessions[mapKey] = session
	}

	return nil
}

func (m *Memory) DeleteAgentTaskSession(_ context.Context, agentID, taskKey string) error {
	m.mu.Lock()
	mapKey := agentID + "::" + taskKey
	delete(m.agentTaskSessions, mapKey)
	m.mu.Unlock()

	return nil
}

func (m *Memory) ListAgentTaskSessions(_ context.Context, agentID string) ([]service.AgentTaskSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.AgentTaskSession
	for _, session := range m.agentTaskSessions {
		if session.AgentID == agentID {
			result = append(result, session)
		}
	}

	// Sort by created_at ascending.
	slices.SortFunc(result, func(a, b service.AgentTaskSession) int {
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
