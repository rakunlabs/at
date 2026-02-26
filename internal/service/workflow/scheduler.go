// Package workflow — scheduler.go implements a cron-based trigger scheduler
// that loads enabled cron triggers from the store and executes their associated
// workflows on schedule using the hardloop library.
//
// Because hardloop's cronJob does not support dynamic add/remove of jobs,
// the scheduler stops and recreates the internal cron runner whenever triggers
// are added, updated, or removed.
package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/worldline-go/hardloop"
)

// cronRunner is satisfied by hardloop's unexported *cronJob type
// (returned by hardloop.NewCron), allowing us to store it without
// referencing the unexported struct name directly.
type cronRunner interface {
	Start(ctx context.Context) error
	Stop()
}

// RunRegistrar is a callback that registers a workflow run for tracking and
// cancellation. It returns a run ID, a cancellable context derived from parent,
// and a cleanup function that must be deferred.
type RunRegistrar func(parent context.Context, workflowID, source string) (runID string, ctx context.Context, cleanup func())

// Scheduler manages cron-based workflow triggers.
type Scheduler struct {
	triggerStore   service.TriggerStorer
	workflowStore  service.WorkflowStorer
	providerLookup ProviderLookup
	skillLookup    SkillLookup
	secretLookup   SecretLookup
	secretLister   SecretLister
	runRegistrar   RunRegistrar

	mu     sync.Mutex
	cron   cronRunner
	cancel context.CancelFunc
	ctx    context.Context // parent context from Start()
}

// NewScheduler creates a new cron trigger scheduler.
func NewScheduler(ts service.TriggerStorer, ws service.WorkflowStorer, lookup ProviderLookup, skillLookup SkillLookup, secretLookup SecretLookup, secretLister SecretLister) *Scheduler {
	return &Scheduler{
		triggerStore:   ts,
		workflowStore:  ws,
		providerLookup: lookup,
		skillLookup:    skillLookup,
		secretLookup:   secretLookup,
		secretLister:   secretLister,
	}
}

// SetRunRegistrar sets the callback used to register runs for tracking.
// Must be called before Start.
func (s *Scheduler) SetRunRegistrar(r RunRegistrar) {
	s.runRegistrar = r
}

// Start loads all enabled cron triggers from the store and starts the
// scheduler. It should be called once during server initialization.
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ctx = ctx

	return s.reload()
}

// Reload stops the current cron runner (if any) and rebuilds it from the
// current set of enabled cron triggers in the database. Call this after
// creating, updating, or deleting a cron trigger.
func (s *Scheduler) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.reload()
}

// Stop stops the scheduler. Safe to call multiple times.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.stopLocked()
}

// stopLocked stops the current cron runner. Must be called with s.mu held.
func (s *Scheduler) stopLocked() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.cron != nil {
		s.cron.Stop()
		s.cron = nil
	}
}

// reload rebuilds the cron runner from the database. Must be called with s.mu held.
func (s *Scheduler) reload() error {
	// Stop any existing runner.
	s.stopLocked()

	if s.ctx == nil {
		return nil
	}

	triggers, err := s.triggerStore.ListEnabledCronTriggers(s.ctx)
	if err != nil {
		return fmt.Errorf("scheduler: load cron triggers: %w", err)
	}

	if len(triggers) == 0 {
		slog.Info("scheduler: no enabled cron triggers found")
		return nil
	}

	// Build hardloop Cron jobs from triggers.
	crons := make([]hardloop.Cron, 0, len(triggers))
	for _, t := range triggers {
		schedule, _ := t.Config["schedule"].(string)
		if schedule == "" {
			slog.Warn("scheduler: cron trigger has no schedule, skipping",
				"trigger_id", t.ID, "workflow_id", t.WorkflowID)
			continue
		}

		// Capture for closure.
		trigger := t
		cronSpec := schedule

		crons = append(crons, hardloop.Cron{
			Name:  fmt.Sprintf("trigger-%s", trigger.ID),
			Specs: []string{cronSpec},
			Func:  s.makeCronFunc(trigger),
		})
	}

	if len(crons) == 0 {
		slog.Info("scheduler: no valid cron specs after filtering")
		return nil
	}

	cronJob, err := hardloop.NewCron(crons...)
	if err != nil {
		return fmt.Errorf("scheduler: create cron runner: %w", err)
	}

	ctx, cancel := context.WithCancel(s.ctx)
	s.cancel = cancel
	s.cron = cronJob

	if err := cronJob.Start(ctx); err != nil {
		cancel()
		return fmt.Errorf("scheduler: start cron runner: %w", err)
	}

	slog.Info("scheduler: started cron triggers", "count", len(crons))

	return nil
}

// makeCronFunc returns the function that hardloop will call on each cron tick
// for a given trigger. It loads the workflow, builds inputs with trigger
// metadata, and runs the engine. If a RunRegistrar is set, the run is
// registered for tracking and cancellation.
func (s *Scheduler) makeCronFunc(trigger service.Trigger) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		slog.Info("scheduler: cron triggered",
			"trigger_id", trigger.ID,
			"workflow_id", trigger.WorkflowID)

		// Load the workflow from the store.
		wf, err := s.workflowStore.GetWorkflow(ctx, trigger.WorkflowID)
		if err != nil {
			slog.Error("scheduler: get workflow failed",
				"trigger_id", trigger.ID,
				"workflow_id", trigger.WorkflowID,
				"error", err)
			return nil // don't stop the cron loop on transient errors
		}

		if wf == nil {
			slog.Warn("scheduler: workflow not found, skipping",
				"trigger_id", trigger.ID,
				"workflow_id", trigger.WorkflowID)
			return nil
		}

		// Build trigger metadata inputs (merged with static payload by the
		// cron_trigger node).
		schedule, _ := trigger.Config["schedule"].(string)
		inputs := map[string]any{
			"trigger_type": "cron",
			"trigger_id":   trigger.ID,
			"triggered_at": time.Now().UTC().Format(time.RFC3339),
			"schedule":     schedule,
		}

		// Register the run for tracking if a registrar is available.
		var runID string
		runCtx := ctx
		if s.runRegistrar != nil {
			var cleanup func()
			runID, runCtx, cleanup = s.runRegistrar(ctx, trigger.WorkflowID, "cron")
			defer cleanup()
		}

		engine := NewEngine(s.providerLookup, s.skillLookup, s.secretLookup, s.secretLister)

		result, err := engine.Run(runCtx, wf.Graph, inputs)
		if err != nil {
			slog.Error("scheduler: workflow execution failed",
				"trigger_id", trigger.ID,
				"workflow_id", trigger.WorkflowID,
				"run_id", runID,
				"error", err)
			return nil // don't stop the cron loop
		}

		slog.Info("scheduler: workflow completed",
			"trigger_id", trigger.ID,
			"workflow_id", trigger.WorkflowID,
			"run_id", runID,
			"output_keys", mapKeys(result.Outputs))

		return nil
	}
}

// mapKeys returns the keys of a map for logging.
func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
