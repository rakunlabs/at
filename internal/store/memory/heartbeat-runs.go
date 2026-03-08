package memory

import (
	"context"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

func (m *Memory) CreateHeartbeatRun(_ context.Context, run service.HeartbeatRun) (*service.HeartbeatRun, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.HeartbeatRun{
		ID:               id,
		AgentID:          run.AgentID,
		OrganizationID:   run.OrganizationID,
		InvocationSource: run.InvocationSource,
		TriggerDetail:    run.TriggerDetail,
		Status:           run.Status,
		ContextSnapshot:  run.ContextSnapshot,
		UsageJSON:        run.UsageJSON,
		ResultJSON:       run.ResultJSON,
		LogRef:           run.LogRef,
		LogBytes:         run.LogBytes,
		LogSHA256:        run.LogSHA256,
		StdoutExcerpt:    run.StdoutExcerpt,
		StderrExcerpt:    run.StderrExcerpt,
		SessionIDBefore:  run.SessionIDBefore,
		SessionIDAfter:   run.SessionIDAfter,
		StartedAt:        run.StartedAt,
		FinishedAt:       run.FinishedAt,
		CreatedAt:        now,
	}

	m.mu.Lock()
	m.heartbeatRuns[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) GetHeartbeatRun(_ context.Context, id string) (*service.HeartbeatRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	run, ok := m.heartbeatRuns[id]
	if !ok {
		return nil, nil
	}

	return &run, nil
}

func (m *Memory) UpdateHeartbeatRun(_ context.Context, id string, run service.HeartbeatRun) (*service.HeartbeatRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.heartbeatRuns[id]
	if !ok {
		return nil, nil
	}

	existing.OrganizationID = run.OrganizationID
	existing.Status = run.Status
	existing.ContextSnapshot = run.ContextSnapshot
	existing.UsageJSON = run.UsageJSON
	existing.ResultJSON = run.ResultJSON
	existing.LogRef = run.LogRef
	existing.LogBytes = run.LogBytes
	existing.LogSHA256 = run.LogSHA256
	existing.StdoutExcerpt = run.StdoutExcerpt
	existing.StderrExcerpt = run.StderrExcerpt
	existing.SessionIDBefore = run.SessionIDBefore
	existing.SessionIDAfter = run.SessionIDAfter
	existing.StartedAt = run.StartedAt
	existing.FinishedAt = run.FinishedAt
	m.heartbeatRuns[id] = existing

	return &existing, nil
}

func (m *Memory) ListHeartbeatRuns(_ context.Context, agentID string, q *query.Query) (*service.ListResult[service.HeartbeatRun], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.HeartbeatRun
	for _, run := range m.heartbeatRuns {
		if run.AgentID == agentID {
			result = append(result, run)
		}
	}

	// Sort by created_at descending (newest first).
	slices.SortFunc(result, func(a, b service.HeartbeatRun) int {
		if a.CreatedAt > b.CreatedAt {
			return -1
		}
		if a.CreatedAt < b.CreatedAt {
			return 1
		}
		return 0
	})

	return paginate(result, q), nil
}

func (m *Memory) GetActiveRun(_ context.Context, agentID string) (*service.HeartbeatRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, run := range m.heartbeatRuns {
		if run.AgentID == agentID && run.Status == service.RunStatusRunning {
			return &run, nil
		}
	}

	return nil, nil
}
