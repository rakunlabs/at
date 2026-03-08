package memory

import (
	"context"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

func (m *Memory) RecordAudit(_ context.Context, entry service.AuditEntry) error {
	now := time.Now().UTC().Format(time.RFC3339)

	entry.ID = ulid.Make().String()
	entry.CreatedAt = now

	m.mu.Lock()
	m.auditLog = append(m.auditLog, entry)
	m.mu.Unlock()

	return nil
}

func (m *Memory) ListAuditEntries(_ context.Context, q *query.Query) (*service.ListResult[service.AuditEntry], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.AuditEntry, len(m.auditLog))
	copy(result, m.auditLog)

	// Sort by created_at descending (newest first).
	slices.SortFunc(result, func(a, b service.AuditEntry) int {
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

func (m *Memory) GetAuditTrail(_ context.Context, resourceType, resourceID string) ([]service.AuditEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.AuditEntry
	for _, e := range m.auditLog {
		if e.ResourceType == resourceType && e.ResourceID == resourceID {
			result = append(result, e)
		}
	}

	// Sort by created_at descending (newest first).
	slices.SortFunc(result, func(a, b service.AuditEntry) int {
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
