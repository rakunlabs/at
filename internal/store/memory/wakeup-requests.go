package memory

import (
	"context"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
)

func (m *Memory) CreateOrCoalesce(_ context.Context, req service.WakeupRequest) (*service.WakeupRequest, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check idempotency key — if non-empty and matches an existing request, return it.
	if req.IdempotencyKey != "" {
		for _, existing := range m.wakeupRequests {
			if existing.IdempotencyKey == req.IdempotencyKey {
				return &existing, nil
			}
		}
	}

	// Check for an existing pending request for the same agent+org — coalesce.
	for id, existing := range m.wakeupRequests {
		if existing.AgentID == req.AgentID && existing.OrganizationID == req.OrganizationID && existing.Status == service.WakeupStatusPending {
			// Merge context.
			if existing.Context == nil {
				existing.Context = make(map[string]any)
			}
			for k, v := range req.Context {
				existing.Context[k] = v
			}
			existing.CoalescedCount++
			existing.UpdatedAt = now
			m.wakeupRequests[id] = existing

			return &existing, nil
		}
	}

	// No existing request to coalesce — create new.
	id := ulid.Make().String()

	rec := service.WakeupRequest{
		ID:             id,
		AgentID:        req.AgentID,
		OrganizationID: req.OrganizationID,
		Status:         service.WakeupStatusPending,
		IdempotencyKey: req.IdempotencyKey,
		Context:        req.Context,
		CoalescedCount: 1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	m.wakeupRequests[id] = rec

	return &rec, nil
}

func (m *Memory) GetWakeupRequest(_ context.Context, id string) (*service.WakeupRequest, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	req, ok := m.wakeupRequests[id]
	if !ok {
		return nil, nil
	}

	return &req, nil
}

func (m *Memory) ListPendingForAgent(_ context.Context, agentID string) ([]service.WakeupRequest, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.WakeupRequest
	for _, req := range m.wakeupRequests {
		if req.AgentID == agentID && req.Status == service.WakeupStatusPending {
			result = append(result, req)
		}
	}

	// Sort by created_at ascending (FIFO).
	slices.SortFunc(result, func(a, b service.WakeupRequest) int {
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

func (m *Memory) MarkDispatched(_ context.Context, id, runID string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.wakeupRequests[id]
	if !ok {
		return nil
	}

	existing.Status = service.WakeupStatusDispatched
	existing.RunID = runID
	existing.UpdatedAt = now
	m.wakeupRequests[id] = existing

	return nil
}

func (m *Memory) PromoteDeferred(_ context.Context, agentID string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Find the oldest deferred request for this agent and promote it.
	var oldestID string
	var oldestCreatedAt string
	for id, req := range m.wakeupRequests {
		if req.AgentID == agentID && req.Status == service.WakeupStatusDeferredIssueExecution {
			if oldestID == "" || req.CreatedAt < oldestCreatedAt {
				oldestID = id
				oldestCreatedAt = req.CreatedAt
			}
		}
	}

	if oldestID != "" {
		req := m.wakeupRequests[oldestID]
		req.Status = service.WakeupStatusPending
		req.UpdatedAt = now
		m.wakeupRequests[oldestID] = req
	}

	return nil
}
