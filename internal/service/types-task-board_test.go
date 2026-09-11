package service

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateTaskBoardColumns(t *testing.T) {
	t.Run("the shipped default is valid and covers every status", func(t *testing.T) {
		cleaned, err := ValidateTaskBoardColumns(DefaultTaskBoardColumns())
		if err != nil {
			t.Fatalf("default layout rejected: %v", err)
		}
		board := TaskBoardSettings{Columns: cleaned}
		if missing := board.UncoveredStatuses(); len(missing) != 0 {
			t.Errorf("default board hides %v", missing)
		}
	})

	t.Run("blocked is not folded into done", func(t *testing.T) {
		for _, column := range DefaultTaskBoardColumns() {
			if column.ID == "done" {
				for _, status := range column.Statuses {
					if status == TaskStatusBlocked {
						t.Error(`"Done" collects blocked tasks, which tells a reader stuck work has finished`)
					}
				}
			}
		}
	})

	t.Run("a status cannot sit in two columns", func(t *testing.T) {
		_, err := ValidateTaskBoardColumns([]TaskBoardColumn{
			{Label: "Left", Statuses: []string{TaskStatusTodo}},
			{Label: "Right", Statuses: []string{TaskStatusTodo}},
		})
		if !errors.Is(err, ErrInvalidTaskBoard) {
			t.Fatalf("err = %v, want ErrInvalidTaskBoard", err)
		}
		for _, want := range []string{"todo", "Left", "Right"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name %q", err, want)
			}
		}
	})

	t.Run("the same column may repeat a status without erroring", func(t *testing.T) {
		cleaned, err := ValidateTaskBoardColumns([]TaskBoardColumn{
			{Label: "Open", Statuses: []string{TaskStatusTodo, TaskStatusTodo}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(cleaned[0].Statuses) != 1 {
			t.Errorf("statuses = %v, want the repeat dropped", cleaned[0].Statuses)
		}
	})

	t.Run("leaving a status off the board is allowed but reported", func(t *testing.T) {
		cleaned, err := ValidateTaskBoardColumns([]TaskBoardColumn{
			{Label: "Active", Statuses: []string{TaskStatusInProgress}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		missing := TaskBoardSettings{Columns: cleaned}.UncoveredStatuses()
		if len(missing) != len(TaskStatuses)-1 {
			t.Errorf("uncovered = %v, want every status but in_progress", missing)
		}
	})

	t.Run("rejects layouts that could never render", func(t *testing.T) {
		cases := map[string][]TaskBoardColumn{
			"no columns":     {},
			"no label":       {{Statuses: []string{TaskStatusTodo}}},
			"no statuses":    {{Label: "Empty"}},
			"unknown status": {{Label: "Bad", Statuses: []string{"almost-done"}}},
			"duplicate id":   {{ID: "x", Label: "One", Statuses: []string{TaskStatusTodo}}, {ID: "x", Label: "Two", Statuses: []string{TaskStatusDone}}},
		}
		for name, columns := range cases {
			if _, err := ValidateTaskBoardColumns(columns); !errors.Is(err, ErrInvalidTaskBoard) && !errors.Is(err, ErrInvalidTaskStatus) {
				t.Errorf("%s: err = %v, want a validation error", name, err)
			}
		}
	})

	t.Run("legacy status names are folded, not rejected", func(t *testing.T) {
		cleaned, err := ValidateTaskBoardColumns([]TaskBoardColumn{
			{Label: "Done", Statuses: []string{"completed"}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cleaned[0].Statuses[0] != TaskStatusDone {
			t.Errorf("status = %q, want %q", cleaned[0].Statuses[0], TaskStatusDone)
		}
	})

	t.Run("ids are derived from labels when omitted", func(t *testing.T) {
		cleaned, err := ValidateTaskBoardColumns([]TaskBoardColumn{
			{Label: "Needs Review!", Statuses: []string{TaskStatusInReview}},
			{Label: "🙂", Statuses: []string{TaskStatusTodo}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cleaned[0].ID != "needs-review" {
			t.Errorf("id = %q, want %q", cleaned[0].ID, "needs-review")
		}
		if cleaned[1].ID == "" {
			t.Error("a label with no slug-able characters produced an empty id")
		}
	})

	t.Run("drop status is the column's first status", func(t *testing.T) {
		column := TaskBoardColumn{Label: "Done", Statuses: []string{TaskStatusDone, TaskStatusCancelled}}
		if column.DropStatus() != TaskStatusDone {
			t.Errorf("DropStatus = %q, want %q", column.DropStatus(), TaskStatusDone)
		}
		if (TaskBoardColumn{}).DropStatus() != "" {
			t.Error("an empty column reported a drop status")
		}
	})
}
