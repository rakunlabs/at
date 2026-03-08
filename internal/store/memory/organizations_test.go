package memory

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestOrganization_HeadAgentID_And_MaxDelegationDepth(t *testing.T) {
	ctx := context.Background()
	store := New()

	t.Run("create with HeadAgentID and MaxDelegationDepth", func(t *testing.T) {
		org := service.Organization{
			Name:               "Test Org",
			Description:        "A test org",
			HeadAgentID:        "agent-1",
			MaxDelegationDepth: 5,
			CreatedBy:          "tester",
			UpdatedBy:          "tester",
		}

		created, err := store.CreateOrganization(ctx, org)
		if err != nil {
			t.Fatalf("CreateOrganization: %v", err)
		}
		if created.HeadAgentID != "agent-1" {
			t.Errorf("HeadAgentID: got %q, want %q", created.HeadAgentID, "agent-1")
		}
		if created.MaxDelegationDepth != 5 {
			t.Errorf("MaxDelegationDepth: got %d, want %d", created.MaxDelegationDepth, 5)
		}

		// Read back
		fetched, err := store.GetOrganization(ctx, created.ID)
		if err != nil {
			t.Fatalf("GetOrganization: %v", err)
		}
		if fetched.HeadAgentID != "agent-1" {
			t.Errorf("fetched HeadAgentID: got %q, want %q", fetched.HeadAgentID, "agent-1")
		}
		if fetched.MaxDelegationDepth != 5 {
			t.Errorf("fetched MaxDelegationDepth: got %d, want %d", fetched.MaxDelegationDepth, 5)
		}
	})

	t.Run("create without MaxDelegationDepth defaults to 10", func(t *testing.T) {
		org := service.Organization{
			Name:        "Default Depth Org",
			Description: "Testing default depth",
			CreatedBy:   "tester",
			UpdatedBy:   "tester",
		}

		created, err := store.CreateOrganization(ctx, org)
		if err != nil {
			t.Fatalf("CreateOrganization: %v", err)
		}
		if created.MaxDelegationDepth != 10 {
			t.Errorf("MaxDelegationDepth: got %d, want %d (default)", created.MaxDelegationDepth, 10)
		}
	})

	t.Run("JSON marshaling includes head_agent_id and max_delegation_depth", func(t *testing.T) {
		org := service.Organization{
			ID:                 "test-id",
			Name:               "JSON Org",
			HeadAgentID:        "agent-2",
			MaxDelegationDepth: 7,
		}

		data, err := json.Marshal(org)
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}

		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}

		if v, ok := m["head_agent_id"]; !ok {
			t.Error("JSON missing head_agent_id field")
		} else if v != "agent-2" {
			t.Errorf("head_agent_id: got %v, want %q", v, "agent-2")
		}

		if v, ok := m["max_delegation_depth"]; !ok {
			t.Error("JSON missing max_delegation_depth field")
		} else if v != float64(7) {
			t.Errorf("max_delegation_depth: got %v, want %v", v, 7)
		}
	})
}

func TestOrganization_AllFieldsPersistence(t *testing.T) {
	ctx := context.Background()
	store := New()

	t.Run("create with all enhanced fields", func(t *testing.T) {
		org := service.Organization{
			Name:                 "Full Org",
			Description:          "Testing all fields",
			IssuePrefix:          "PAP",
			IssueCounter:         41,
			BudgetMonthlyCents:   100000,
			SpentMonthlyCents:    5000,
			BudgetResetAt:        "2026-03-01T00:00:00Z",
			RequireBoardApproval: true,
			HeadAgentID:          "agent-head",
			MaxDelegationDepth:   3,
			CreatedBy:            "tester",
			UpdatedBy:            "tester",
		}

		created, err := store.CreateOrganization(ctx, org)
		if err != nil {
			t.Fatalf("CreateOrganization: %v", err)
		}

		// Read back and verify all fields
		fetched, err := store.GetOrganization(ctx, created.ID)
		if err != nil {
			t.Fatalf("GetOrganization: %v", err)
		}

		if fetched.IssuePrefix != "PAP" {
			t.Errorf("IssuePrefix: got %q, want %q", fetched.IssuePrefix, "PAP")
		}
		if fetched.IssueCounter != 41 {
			t.Errorf("IssueCounter: got %d, want %d", fetched.IssueCounter, 41)
		}
		if fetched.BudgetMonthlyCents != 100000 {
			t.Errorf("BudgetMonthlyCents: got %d, want %d", fetched.BudgetMonthlyCents, 100000)
		}
		if fetched.SpentMonthlyCents != 5000 {
			t.Errorf("SpentMonthlyCents: got %d, want %d", fetched.SpentMonthlyCents, 5000)
		}
		if fetched.BudgetResetAt != "2026-03-01T00:00:00Z" {
			t.Errorf("BudgetResetAt: got %q, want %q", fetched.BudgetResetAt, "2026-03-01T00:00:00Z")
		}
		if !fetched.RequireBoardApproval {
			t.Error("RequireBoardApproval: got false, want true")
		}
		if fetched.HeadAgentID != "agent-head" {
			t.Errorf("HeadAgentID: got %q, want %q", fetched.HeadAgentID, "agent-head")
		}
		if fetched.MaxDelegationDepth != 3 {
			t.Errorf("MaxDelegationDepth: got %d, want %d", fetched.MaxDelegationDepth, 3)
		}
	})

	t.Run("update copies enhanced fields", func(t *testing.T) {
		// Create initial org
		org := service.Organization{
			Name:        "Update Test",
			Description: "Before update",
			CreatedBy:   "tester",
			UpdatedBy:   "tester",
		}
		created, err := store.CreateOrganization(ctx, org)
		if err != nil {
			t.Fatalf("CreateOrganization: %v", err)
		}

		// Update with enhanced fields
		updated, err := store.UpdateOrganization(ctx, created.ID, service.Organization{
			Name:                 "Update Test",
			Description:          "After update",
			IssuePrefix:          "UPD",
			HeadAgentID:          "agent-new",
			MaxDelegationDepth:   8,
			BudgetMonthlyCents:   50000,
			SpentMonthlyCents:    1000,
			BudgetResetAt:        "2026-04-01T00:00:00Z",
			RequireBoardApproval: true,
			UpdatedBy:            "updater",
		})
		if err != nil {
			t.Fatalf("UpdateOrganization: %v", err)
		}

		if updated.IssuePrefix != "UPD" {
			t.Errorf("IssuePrefix: got %q, want %q", updated.IssuePrefix, "UPD")
		}
		if updated.HeadAgentID != "agent-new" {
			t.Errorf("HeadAgentID: got %q, want %q", updated.HeadAgentID, "agent-new")
		}
		if updated.MaxDelegationDepth != 8 {
			t.Errorf("MaxDelegationDepth: got %d, want %d", updated.MaxDelegationDepth, 8)
		}
		if updated.BudgetMonthlyCents != 50000 {
			t.Errorf("BudgetMonthlyCents: got %d, want %d", updated.BudgetMonthlyCents, 50000)
		}
		if !updated.RequireBoardApproval {
			t.Error("RequireBoardApproval: got false, want true")
		}
	})

	t.Run("update clears HeadAgentID with empty string", func(t *testing.T) {
		// Create org with head agent
		org := service.Organization{
			Name:        "Clear Head Test",
			HeadAgentID: "agent-to-clear",
			CreatedBy:   "tester",
			UpdatedBy:   "tester",
		}
		created, err := store.CreateOrganization(ctx, org)
		if err != nil {
			t.Fatalf("CreateOrganization: %v", err)
		}

		// Update with empty HeadAgentID to clear it
		updated, err := store.UpdateOrganization(ctx, created.ID, service.Organization{
			Name:      "Clear Head Test",
			UpdatedBy: "updater",
		})
		if err != nil {
			t.Fatalf("UpdateOrganization: %v", err)
		}

		if updated.HeadAgentID != "" {
			t.Errorf("HeadAgentID should be cleared: got %q, want empty", updated.HeadAgentID)
		}
	})
}
