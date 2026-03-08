package memory

import (
	"context"
	"slices"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func (m *Memory) RecordHeartbeat(_ context.Context, agentID string, metadata map[string]any) error {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.agentHeartbeats[agentID] = service.AgentHeartbeat{
		AgentID:         agentID,
		Status:          "healthy",
		LastHeartbeatAt: now,
		Metadata:        metadata,
		UpdatedAt:       now,
	}

	return nil
}

func (m *Memory) GetHeartbeat(_ context.Context, agentID string) (*service.AgentHeartbeat, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	hb, ok := m.agentHeartbeats[agentID]
	if !ok {
		return nil, nil
	}

	return &hb, nil
}

func (m *Memory) ListHeartbeats(_ context.Context) ([]service.AgentHeartbeat, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.AgentHeartbeat, 0, len(m.agentHeartbeats))
	for _, hb := range m.agentHeartbeats {
		result = append(result, hb)
	}

	// Sort by last_heartbeat_at descending (newest first).
	slices.SortFunc(result, func(a, b service.AgentHeartbeat) int {
		if a.LastHeartbeatAt > b.LastHeartbeatAt {
			return -1
		}
		if a.LastHeartbeatAt < b.LastHeartbeatAt {
			return 1
		}
		return 0
	})

	return result, nil
}

func (m *Memory) MarkStale(_ context.Context, threshold time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-threshold).Format(time.RFC3339)
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for id, hb := range m.agentHeartbeats {
		if hb.Status == "healthy" && hb.LastHeartbeatAt < cutoff {
			hb.Status = "stale"
			hb.UpdatedAt = now
			m.agentHeartbeats[id] = hb
			count++
		}
	}

	return count, nil
}
