package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func TestAgentBudgetSchedulePersistence(t *testing.T) {
	store := newTestStore(t, nil)
	now := time.Now().UTC().Truncate(time.Second)
	want := service.AgentBudget{
		AgentID:      "budget-agent-1",
		MonthlyLimit: 25,
		CurrentSpend: 3.5,
		PeriodStart:  now.Format(time.RFC3339),
		PeriodEnd:    now.AddDate(0, 1, 0).Format(time.RFC3339),
		BudgetSchedule: service.BudgetSchedule{
			BudgetPeriod: service.BudgetPeriodMonthly, BudgetResetDay: 31,
			BudgetResetTime: "06:45", BudgetTimezone: "Asia/Tokyo",
		},
	}

	if err := store.SetAgentBudget(context.Background(), want); err != nil {
		t.Fatalf("SetAgentBudget: %v", err)
	}
	got, err := store.GetAgentBudget(context.Background(), want.AgentID)
	if err != nil {
		t.Fatalf("GetAgentBudget: %v", err)
	}
	if got == nil {
		t.Fatal("GetAgentBudget returned nil")
	}
	if got.BudgetSchedule != want.BudgetSchedule || got.MonthlyLimit != want.MonthlyLimit {
		t.Errorf("budget = %+v, want schedule %+v and limit %v", got, want.BudgetSchedule, want.MonthlyLimit)
	}
}
