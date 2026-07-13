package server

import (
	"context"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

type taskOrganizationStore struct {
	mockTaskStore
	children []service.Task
}

func (m *taskOrganizationStore) ListChildTasks(_ context.Context, _ string) ([]service.Task, error) {
	return m.children, nil
}

func TestTaskOrganizationID(t *testing.T) {
	tests := []struct {
		name     string
		task     *service.Task
		children []service.Task
		want     string
	}{
		{
			name: "uses task organization",
			task: &service.Task{ID: "parent", OrganizationID: "org-parent"},
			children: []service.Task{
				{OrganizationID: "org-child"},
			},
			want: "org-parent",
		},
		{
			name: "recovers unanimous child organization",
			task: &service.Task{ID: "parent"},
			children: []service.Task{
				{OrganizationID: "org-1"},
				{OrganizationID: "org-1"},
			},
			want: "org-1",
		},
		{
			name: "rejects conflicting child organizations",
			task: &service.Task{ID: "parent"},
			children: []service.Task{
				{OrganizationID: "org-1"},
				{OrganizationID: "org-2"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &taskOrganizationStore{children: tt.children}
			s := &Server{taskStore: store}
			if got := s.taskOrganizationID(context.Background(), tt.task); got != tt.want {
				t.Fatalf("taskOrganizationID() = %q, want %q", got, tt.want)
			}
		})
	}
}
