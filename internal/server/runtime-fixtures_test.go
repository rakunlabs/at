package server

import (
	"context"
	"sync"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
)

// installRuntimeFixture explicitly supplies installation authority and durable
// provenance to pre-existing orchestration tests. Security tests use real/scoped
// principals instead; no production missing-context fallback is involved.
func installRuntimeFixture(t *testing.T, s *Server) context.Context {
	t.Helper()
	s.ctx = executiontest.Context(t)
	s.store = &fixtureExecutionStore{ProviderStorer: s.store, tasks: s.taskStore, rows: map[string]service.ExecutionProvenance{}}
	return s.ctx
}

type fixtureExecutionStore struct {
	service.ProviderStorer
	tasks service.TaskStorer
	mu    sync.Mutex
	rows  map[string]service.ExecutionProvenance
}

func (f *fixtureExecutionStore) GetExecutionPolicy(ctx context.Context, id string) (*service.ExecutionPolicy, error) {
	return &service.ExecutionPolicy{WorkspaceID: id, Mode: service.ExecutionTrustedHost, GrantedBy: "test-platform-admin"}, nil
}
func (f *fixtureExecutionStore) SaveExecutionPolicy(context.Context, service.ExecutionPolicy) error {
	return service.ErrExecutionDenied
}
func (f *fixtureExecutionStore) GetExecutionProvenance(ctx context.Context, id string) (*service.ExecutionProvenance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.rows[id]
	if !ok {
		return nil, nil
	}
	return &v, nil
}
func (f *fixtureExecutionStore) CreateExecutionProvenance(ctx context.Context, p service.ExecutionProvenance) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.rows[p.RunID]; ok {
		return service.ErrExecutionDenied
	}
	f.rows[p.RunID] = p
	return nil
}
func (f *fixtureExecutionStore) CreateExecutionChildTask(ctx context.Context, task service.Task) (*service.Task, error) {
	return f.CreateExecutionTask(ctx, task)
}
func (f *fixtureExecutionStore) CreateExecutionTask(ctx context.Context, task service.Task) (*service.Task, error) {
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "execution.run"}); err != nil {
		return nil, err
	}
	taskPtr, err := f.tasks.CreateTask(ctx, task)
	if err != nil {
		return nil, err
	}
	bound, err := service.DeriveExecution(ctx, "task/"+taskPtr.ID, "task")
	if err != nil {
		return nil, err
	}
	p, _, _ := service.ExecutionFromContext(bound)
	return taskPtr, f.CreateExecutionProvenance(bound, p)
}
