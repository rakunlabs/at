package service

import (
	"errors"
	"strings"
	"testing"
)

func TestTaskStatusVocabulary(t *testing.T) {
	t.Run("retired synonyms fold onto their canonical value", func(t *testing.T) {
		for raw, want := range map[string]string{
			"open":      TaskStatusTodo,
			"review":    TaskStatusInReview,
			"completed": TaskStatusDone,
			// Case and padding are normalized too: models emit both.
			"  OPEN ":   TaskStatusTodo,
			"Completed": TaskStatusDone,
		} {
			if got := NormalizeTaskStatus(raw); got != want {
				t.Errorf("NormalizeTaskStatus(%q) = %q, want %q", raw, got, want)
			}
		}
	})

	t.Run("every canonical status is valid and survives normalization", func(t *testing.T) {
		for _, status := range TaskStatuses {
			if !ValidTaskStatus(status) {
				t.Errorf("%q is listed in TaskStatuses but rejected by ValidTaskStatus", status)
			}
			if got := NormalizeTaskStatus(status); got != status {
				t.Errorf("NormalizeTaskStatus(%q) = %q, want unchanged", status, got)
			}
		}
	})

	t.Run("retired synonyms are not advertised", func(t *testing.T) {
		for _, status := range TaskStatuses {
			if _, retired := legacyTaskStatuses[status]; retired {
				t.Errorf("%q is retired but still advertised in TaskStatuses", status)
			}
		}
	})

	t.Run("unknown status is rejected with the value and the vocabulary", func(t *testing.T) {
		got, err := ParseTaskStatus("in-progress")
		if !errors.Is(err, ErrInvalidTaskStatus) {
			t.Fatalf("err = %v, want ErrInvalidTaskStatus", err)
		}
		if got != "" {
			t.Errorf("status = %q, want empty on error", got)
		}
		for _, want := range []string{"in-progress", "in_progress", "cancelled"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
	})

	t.Run("empty is not silently accepted", func(t *testing.T) {
		if _, err := ParseTaskStatus(""); !errors.Is(err, ErrInvalidTaskStatus) {
			t.Errorf("empty status err = %v, want ErrInvalidTaskStatus", err)
		}
	})

	t.Run("terminal set is exactly done, cancelled and blocked", func(t *testing.T) {
		want := map[string]bool{
			TaskStatusDone: true, TaskStatusCancelled: true, TaskStatusBlocked: true,
			TaskStatusBacklog: false, TaskStatusTodo: false,
			TaskStatusInProgress: false, TaskStatusInReview: false,
		}
		for status, terminal := range want {
			if got := IsTerminalTaskStatus(status); got != terminal {
				t.Errorf("IsTerminalTaskStatus(%q) = %v, want %v", status, got, terminal)
			}
		}
		// A retired synonym still resolves, so old rows and old clients are read
		// correctly rather than being treated as unfinished forever.
		if !IsTerminalTaskStatus("completed") {
			t.Error(`IsTerminalTaskStatus("completed") = false, want true`)
		}
	})
}
