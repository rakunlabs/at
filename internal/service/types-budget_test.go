package service

import (
	"testing"
	"time"
)

func TestBudgetPeriodBounds(t *testing.T) {
	tests := []struct {
		name         string
		now          string
		schedule     BudgetSchedule
		wantStart    string
		wantEnd      string
		wantDuration time.Duration
	}{
		{
			name:      "defaults to UTC month",
			now:       "2026-07-15T12:00:00Z",
			wantStart: "2026-07-01T00:00:00Z",
			wantEnd:   "2026-08-01T00:00:00Z",
		},
		{
			name: "daily before reset",
			now:  "2026-07-15T07:59:00Z",
			schedule: BudgetSchedule{
				BudgetPeriod: BudgetPeriodDaily, BudgetResetTime: "08:00", BudgetTimezone: "UTC",
			},
			wantStart: "2026-07-14T08:00:00Z",
			wantEnd:   "2026-07-15T08:00:00Z",
		},
		{
			name: "weekly ISO Monday in Tokyo",
			now:  "2026-07-15T03:00:00Z",
			schedule: BudgetSchedule{
				BudgetPeriod: BudgetPeriodWeekly, BudgetResetDay: 1, BudgetResetTime: "09:30", BudgetTimezone: "Asia/Tokyo",
			},
			wantStart: "2026-07-13T09:30:00+09:00",
			wantEnd:   "2026-07-20T09:30:00+09:00",
		},
		{
			name: "monthly day clamps in February",
			now:  "2026-02-28T20:00:00Z",
			schedule: BudgetSchedule{
				BudgetPeriod: BudgetPeriodMonthly, BudgetResetDay: 31, BudgetResetTime: "12:00", BudgetTimezone: "UTC",
			},
			wantStart: "2026-02-28T12:00:00Z",
			wantEnd:   "2026-03-31T12:00:00Z",
		},
		{
			name: "monthly before clamped February reset",
			now:  "2026-02-27T20:00:00Z",
			schedule: BudgetSchedule{
				BudgetPeriod: BudgetPeriodMonthly, BudgetResetDay: 31, BudgetResetTime: "12:00", BudgetTimezone: "UTC",
			},
			wantStart: "2026-01-31T12:00:00Z",
			wantEnd:   "2026-02-28T12:00:00Z",
		},
		{
			name: "daily spring DST interval is 23 hours",
			now:  "2026-03-08T17:00:00Z",
			schedule: BudgetSchedule{
				BudgetPeriod: BudgetPeriodDaily, BudgetTimezone: "America/New_York",
			},
			wantStart:    "2026-03-08T00:00:00-05:00",
			wantEnd:      "2026-03-09T00:00:00-04:00",
			wantDuration: 23 * time.Hour,
		},
		{
			name: "daily fall DST interval is 25 hours",
			now:  "2026-11-01T18:00:00Z",
			schedule: BudgetSchedule{
				BudgetPeriod: BudgetPeriodDaily, BudgetTimezone: "America/New_York",
			},
			wantStart:    "2026-11-01T00:00:00-04:00",
			wantEnd:      "2026-11-02T00:00:00-05:00",
			wantDuration: 25 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := mustParseBudgetTime(t, tt.now)
			start, end, err := BudgetPeriodBounds(now, tt.schedule)
			if err != nil {
				t.Fatalf("BudgetPeriodBounds: %v", err)
			}
			wantStart := mustParseBudgetTime(t, tt.wantStart)
			wantEnd := mustParseBudgetTime(t, tt.wantEnd)
			if !start.Equal(wantStart) {
				t.Errorf("start = %s, want %s", start.Format(time.RFC3339), tt.wantStart)
			}
			if !end.Equal(wantEnd) {
				t.Errorf("end = %s, want %s", end.Format(time.RFC3339), tt.wantEnd)
			}
			if tt.wantDuration > 0 && end.Sub(start) != tt.wantDuration {
				t.Errorf("duration = %s, want %s", end.Sub(start), tt.wantDuration)
			}
		})
	}
}

func TestNormalizeBudgetScheduleValidation(t *testing.T) {
	tests := []struct {
		name     string
		schedule BudgetSchedule
		wantErr  bool
	}{
		{name: "defaults", schedule: BudgetSchedule{}},
		{name: "weekly Sunday", schedule: BudgetSchedule{BudgetPeriod: BudgetPeriodWeekly, BudgetResetDay: 7}},
		{name: "invalid period", schedule: BudgetSchedule{BudgetPeriod: "yearly"}, wantErr: true},
		{name: "invalid weekly day", schedule: BudgetSchedule{BudgetPeriod: BudgetPeriodWeekly, BudgetResetDay: 8}, wantErr: true},
		{name: "daily rejects day", schedule: BudgetSchedule{BudgetPeriod: BudgetPeriodDaily, BudgetResetDay: 1}, wantErr: true},
		{name: "invalid time", schedule: BudgetSchedule{BudgetResetTime: "9:00"}, wantErr: true},
		{name: "invalid timezone", schedule: BudgetSchedule{BudgetTimezone: "Mars/Olympus"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeBudgetSchedule(tt.schedule)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NormalizeBudgetSchedule() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.name == "defaults" && (got.BudgetPeriod != BudgetPeriodMonthly || got.BudgetResetDay != 1 || got.BudgetResetTime != "00:00" || got.BudgetTimezone != "UTC") {
				t.Errorf("defaults = %+v", got)
			}
		})
	}
}

func mustParseBudgetTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed
}
