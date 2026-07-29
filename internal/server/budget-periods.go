package server

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func utcMonthPeriod(now time.Time) (time.Time, time.Time) {
	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, 0)
}

func organizationBudgetPeriod(org *service.Organization, now time.Time) (time.Time, time.Time, error) {
	if org != nil && org.BudgetPeriod != "" {
		return service.BudgetPeriodBounds(now, org.BudgetSchedule)
	}
	if org != nil && org.BudgetResetAt != "" {
		start, err := time.Parse(time.RFC3339, org.BudgetResetAt)
		if err == nil {
			schedule := budgetScheduleFromReset(start)
			return service.BudgetPeriodBounds(now, schedule)
		}
	}
	start, end := utcMonthPeriod(now)
	return start, end, nil
}

func effectiveOrganizationBudgetSchedule(org *service.Organization) (service.BudgetSchedule, error) {
	if org != nil && org.BudgetPeriod != "" {
		return service.NormalizeBudgetSchedule(org.BudgetSchedule)
	}
	if org != nil && org.BudgetResetAt != "" {
		if reset, err := time.Parse(time.RFC3339, org.BudgetResetAt); err == nil {
			return budgetScheduleFromReset(reset), nil
		}
	}
	return service.NormalizeBudgetSchedule(service.BudgetSchedule{})
}

func budgetScheduleFromReset(reset time.Time) service.BudgetSchedule {
	reset = reset.UTC()
	return service.BudgetSchedule{
		BudgetPeriod:    service.BudgetPeriodMonthly,
		BudgetResetDay:  reset.Day(),
		BudgetResetTime: reset.Format("15:04"),
		BudgetTimezone:  "UTC",
	}
}

func agentBudgetPeriod(budget *service.AgentBudget, now time.Time) (time.Time, time.Time, error) {
	if budget != nil && budget.BudgetPeriod != "" {
		return service.BudgetPeriodBounds(now, budget.BudgetSchedule)
	}
	if budget != nil {
		start, startErr := time.Parse(time.RFC3339, budget.PeriodStart)
		end, endErr := time.Parse(time.RFC3339, budget.PeriodEnd)
		if startErr == nil && endErr == nil && !end.Before(start) && !now.Before(start) && now.Before(end) {
			return start, end, nil
		}
	}
	start, end := utcMonthPeriod(now)
	return start, end, nil
}

func (s *Server) deriveAgentBudget(ctx context.Context, budget *service.AgentBudget, now time.Time) (*service.AgentBudget, error) {
	if budget == nil {
		return nil, nil
	}
	start, end, err := agentBudgetPeriod(budget, now)
	if err != nil {
		return nil, fmt.Errorf("calculate agent budget period: %w", err)
	}
	if s.costEventStore == nil {
		return nil, fmt.Errorf("cost event store not configured")
	}
	summary, err := s.costEventStore.GetUsageSummary(ctx, service.UsageFilter{
		From:     start.Format(time.RFC3339),
		To:       end.Format(time.RFC3339),
		AgentIDs: []string{budget.AgentID},
	})
	if err != nil {
		return nil, fmt.Errorf("get agent budget spend: %w", err)
	}

	derived := *budget
	if derived.BudgetPeriod == "" {
		derived.BudgetSchedule = budgetScheduleFromReset(start)
	}
	derived.CurrentSpend = summary.CostCents / 100
	derived.PeriodStart = start.Format(time.RFC3339)
	derived.PeriodEnd = end.Format(time.RFC3339)
	return &derived, nil
}

func (s *Server) organizationBudgetStatus(ctx context.Context, org *service.Organization, now time.Time) (*service.BudgetStatus, error) {
	if org == nil {
		return nil, fmt.Errorf("organization is required")
	}
	start, end, err := organizationBudgetPeriod(org, now)
	if err != nil {
		return nil, fmt.Errorf("calculate organization budget period: %w", err)
	}
	if s.costEventStore == nil {
		return nil, fmt.Errorf("cost event store not configured")
	}
	summary, err := s.costEventStore.GetUsageSummary(ctx, service.UsageFilter{
		From:   start.Format(time.RFC3339),
		To:     end.Format(time.RFC3339),
		OrgIDs: []string{org.ID},
	})
	if err != nil {
		return nil, fmt.Errorf("get organization budget spend: %w", err)
	}

	remaining := float64(org.BudgetMonthlyCents) - summary.CostCents
	if remaining < 0 {
		remaining = 0
	}
	usagePercent := 0.0
	if org.BudgetMonthlyCents > 0 {
		usagePercent = summary.CostCents / float64(org.BudgetMonthlyCents) * 100
	}
	schedule, err := effectiveOrganizationBudgetSchedule(org)
	if err != nil {
		return nil, fmt.Errorf("normalize organization budget schedule: %w", err)
	}

	return &service.BudgetStatus{
		BudgetSchedule: schedule,
		LimitCents:     org.BudgetMonthlyCents,
		SpendCents:     summary.CostCents,
		RemainingCents: remaining,
		UsagePercent:   usagePercent,
		PeriodStart:    start.Format(time.RFC3339),
		PeriodEnd:      end.Format(time.RFC3339),
		NextResetAt:    end.Format(time.RFC3339),
	}, nil
}

func validAgentBudgetLimit(limit float64) bool {
	return !math.IsNaN(limit) && !math.IsInf(limit, 0) && limit >= 0
}
