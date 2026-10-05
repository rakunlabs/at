package server

import (
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestIsResumableTaskStatus(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{service.TaskStatusInProgress, true},
		{service.TaskStatusTodo, true},
		{service.TaskStatusBlocked, true},
		{service.TaskStatusCancelled, true},
		{service.TaskStatusDone, true},
		{service.TaskStatusInReview, false},
		{service.TaskStatusBacklog, false},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			if got := isResumableTaskStatus(tt.status); got != tt.want {
				t.Fatalf("isResumableTaskStatus(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}
