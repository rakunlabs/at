package memory

import (
	"context"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func (m *Memory) GetAgentRuntimeState(_ context.Context, agentID string) (*service.AgentRuntimeState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, ok := m.agentRuntimeState[agentID]
	if !ok {
		return nil, nil
	}

	return &state, nil
}

func (m *Memory) UpsertAgentRuntimeState(_ context.Context, state service.AgentRuntimeState) error {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.agentRuntimeState[state.AgentID]; ok {
		existing.SessionID = state.SessionID
		existing.StateJSON = state.StateJSON
		existing.LastRunID = state.LastRunID
		existing.LastRunStatus = state.LastRunStatus
		existing.LastError = state.LastError
		existing.UpdatedAt = now
		m.agentRuntimeState[state.AgentID] = existing
	} else {
		state.UpdatedAt = now
		m.agentRuntimeState[state.AgentID] = state
	}

	return nil
}

func (m *Memory) AccumulateUsage(_ context.Context, agentID string, inputTokens, outputTokens, costCents int64) error {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.agentRuntimeState[agentID]
	if !ok {
		existing = service.AgentRuntimeState{
			AgentID: agentID,
		}
	}

	existing.TotalInputTokens += inputTokens
	existing.TotalOutputTokens += outputTokens
	existing.TotalCostCents += costCents
	existing.UpdatedAt = now
	m.agentRuntimeState[agentID] = existing

	return nil
}
