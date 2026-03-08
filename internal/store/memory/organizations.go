package memory

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

func (m *Memory) ListOrganizations(_ context.Context, q *query.Query) (*service.ListResult[service.Organization], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.Organization, 0, len(m.organizations))
	for _, o := range m.organizations {
		result = append(result, o)
	}

	slices.SortFunc(result, func(a, b service.Organization) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})

	return paginate(result, q), nil
}

func (m *Memory) GetOrganization(_ context.Context, id string) (*service.Organization, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	o, ok := m.organizations[id]
	if !ok {
		return nil, nil
	}

	return &o, nil
}

func (m *Memory) CreateOrganization(_ context.Context, org service.Organization) (*service.Organization, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	maxDepth := org.MaxDelegationDepth
	if maxDepth == 0 {
		maxDepth = 10
	}

	rec := service.Organization{
		ID:                   id,
		Name:                 org.Name,
		Description:          org.Description,
		IssuePrefix:          org.IssuePrefix,
		IssueCounter:         org.IssueCounter,
		BudgetMonthlyCents:   org.BudgetMonthlyCents,
		SpentMonthlyCents:    org.SpentMonthlyCents,
		BudgetResetAt:        org.BudgetResetAt,
		RequireBoardApproval: org.RequireBoardApproval,
		HeadAgentID:          org.HeadAgentID,
		MaxDelegationDepth:   maxDepth,
		CanvasLayout:         org.CanvasLayout,
		CreatedAt:            now,
		UpdatedAt:            now,
		CreatedBy:            org.CreatedBy,
		UpdatedBy:            org.UpdatedBy,
	}

	m.mu.Lock()
	m.organizations[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateOrganization(_ context.Context, id string, org service.Organization) (*service.Organization, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.organizations[id]
	if !ok {
		return nil, nil
	}

	existing.Name = org.Name
	existing.Description = org.Description
	if len(org.CanvasLayout) > 0 {
		existing.CanvasLayout = org.CanvasLayout
	}
	if org.IssuePrefix != "" {
		existing.IssuePrefix = org.IssuePrefix
	}
	existing.HeadAgentID = org.HeadAgentID // always write — empty means clear
	if org.MaxDelegationDepth > 0 {
		existing.MaxDelegationDepth = org.MaxDelegationDepth
	}
	existing.BudgetMonthlyCents = org.BudgetMonthlyCents
	existing.SpentMonthlyCents = org.SpentMonthlyCents
	existing.BudgetResetAt = org.BudgetResetAt
	existing.RequireBoardApproval = org.RequireBoardApproval
	existing.UpdatedAt = now
	existing.UpdatedBy = org.UpdatedBy
	m.organizations[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteOrganization(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.organizations, id)
	m.mu.Unlock()

	return nil
}

func (m *Memory) IncrementIssueCounter(_ context.Context, orgID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	org, ok := m.organizations[orgID]
	if !ok {
		return 0, fmt.Errorf("organization %q not found", orgID)
	}

	org.IssueCounter++
	m.organizations[orgID] = org

	return org.IssueCounter, nil
}
