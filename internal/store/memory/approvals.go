package memory

import (
	"context"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

func (m *Memory) ListApprovals(_ context.Context, q *query.Query) (*service.ListResult[service.Approval], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.Approval, 0, len(m.approvals))
	for _, a := range m.approvals {
		result = append(result, a)
	}

	// Sort by created_at descending (newest first).
	slices.SortFunc(result, func(a, b service.Approval) int {
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

func (m *Memory) GetApproval(_ context.Context, id string) (*service.Approval, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	a, ok := m.approvals[id]
	if !ok {
		return nil, nil
	}

	return &a, nil
}

func (m *Memory) CreateApproval(_ context.Context, approval service.Approval) (*service.Approval, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.Approval{
		ID:              id,
		OrganizationID:  approval.OrganizationID,
		Type:            approval.Type,
		Status:          approval.Status,
		RequestedByType: approval.RequestedByType,
		RequestedByID:   approval.RequestedByID,
		RequestDetails:  approval.RequestDetails,
		DecisionNote:    approval.DecisionNote,
		DecidedByUserID: approval.DecidedByUserID,
		DecidedAt:       approval.DecidedAt,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	m.mu.Lock()
	m.approvals[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateApproval(_ context.Context, id string, approval service.Approval) (*service.Approval, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.approvals[id]
	if !ok {
		return nil, nil
	}

	existing.Status = approval.Status
	existing.DecisionNote = approval.DecisionNote
	existing.DecidedByUserID = approval.DecidedByUserID
	existing.DecidedAt = approval.DecidedAt
	existing.UpdatedAt = now
	m.approvals[id] = existing

	return &existing, nil
}

func (m *Memory) ListPendingApprovals(_ context.Context, orgID string) ([]service.Approval, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.Approval
	for _, a := range m.approvals {
		if a.OrganizationID == orgID && a.Status == service.ApprovalStatusPending {
			result = append(result, a)
		}
	}

	// Sort by created_at ascending (oldest first — FIFO for review).
	slices.SortFunc(result, func(a, b service.Approval) int {
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
