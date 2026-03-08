package memory

import (
	"context"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
)

func (m *Memory) CreateRevision(_ context.Context, rev service.AgentConfigRevision) (*service.AgentConfigRevision, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Compute next version for this agent.
	maxVersion := 0
	for _, existing := range m.agentConfigRevisions {
		if existing.AgentID == rev.AgentID && existing.Version > maxVersion {
			maxVersion = existing.Version
		}
	}

	rec := service.AgentConfigRevision{
		ID:           ulid.Make().String(),
		AgentID:      rev.AgentID,
		Version:      maxVersion + 1,
		ConfigBefore: rev.ConfigBefore,
		ConfigAfter:  rev.ConfigAfter,
		ChangedBy:    rev.ChangedBy,
		ChangeNote:   rev.ChangeNote,
		CreatedAt:    now,
	}

	m.agentConfigRevisions = append(m.agentConfigRevisions, rec)

	return &rec, nil
}

func (m *Memory) ListRevisions(_ context.Context, agentID string) ([]service.AgentConfigRevision, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.AgentConfigRevision
	for _, rev := range m.agentConfigRevisions {
		if rev.AgentID == agentID {
			result = append(result, rev)
		}
	}

	// Sort by version descending (newest first).
	slices.SortFunc(result, func(a, b service.AgentConfigRevision) int {
		if a.Version > b.Version {
			return -1
		}
		if a.Version < b.Version {
			return 1
		}
		return 0
	})

	return result, nil
}

func (m *Memory) GetRevision(_ context.Context, id string) (*service.AgentConfigRevision, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, rev := range m.agentConfigRevisions {
		if rev.ID == id {
			return &rev, nil
		}
	}

	return nil, nil
}

func (m *Memory) GetLatestRevision(_ context.Context, agentID string) (*service.AgentConfigRevision, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var latest *service.AgentConfigRevision
	for _, rev := range m.agentConfigRevisions {
		if rev.AgentID == agentID {
			if latest == nil || rev.Version > latest.Version {
				r := rev
				latest = &r
			}
		}
	}

	return latest, nil
}
