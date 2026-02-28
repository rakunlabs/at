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

	"github.com/rakunlabs/at/internal/cluster"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/logi"
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
	triggerStore         service.TriggerStorer
	workflowStore        service.WorkflowStorer
	workflowVersionStore service.WorkflowVersionStorer
	providerLookup       ProviderLookup
	skillLookup          SkillLookup
	varLookup            VarLookup
	varLister            VarLister
	nodeConfigLookup     NodeConfigLookup
	runRegistrar         RunRegistrar

	cluster *cluster.Cluster

	mu     sync.Mutex
	cron   cronRunner
	cancel context.CancelFunc
	ctx    context.Context // parent context from Start()
}

// NewScheduler creates a new cron trigger scheduler.
func NewScheduler(ts service.TriggerStorer, ws service.WorkflowStorer, wvs service.WorkflowVersionStorer, lookup ProviderLookup, skillLookup SkillLookup, varLookup VarLookup, varLister VarLister, nodeConfigLookup NodeConfigLookup, cl *cluster.Cluster) *Scheduler {
	return &Scheduler{
		triggerStore:         ts,
		workflowStore:        ws,
		workflowVersionStore: wvs,
		providerLookup:       lookup,
		skillLookup:          skillLookup,
		varLookup:            varLookup,
		varLister:            varLister,
		nodeConfigLookup:     nodeConfigLookup,
		cluster:              cl,
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

	// If clustering is enabled, run the lock loop in the background.
	if s.cluster != nil {
		go s.runLockLoop(ctx)
		// We don't start the cron runner immediately; runLockLoop will do it
		// when it acquires the lock.
		return nil
	}

	// Single instance mode: just start immediately.
	return s.reload()
}

// runLockLoop attempts to acquire the scheduler lock. When acquired, it
// starts the cron runner. When lost, it stops the cron runner.
func (s *Scheduler) runLockLoop(ctx context.Context) {
	logger := logi.Ctx(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		logger.Info("scheduler: attempting to acquire leader lock")
		if err := s.cluster.LockScheduler(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.Error("scheduler: failed to acquire lock, retrying", "error", err)
			time.Sleep(5 * time.Second)
			continue
		}

		// Lock acquired!
		logger.Info("scheduler: acquired leader lock, starting cron triggers")

		// Start the cron runner.
		s.mu.Lock()
		if err := s.reload(); err != nil {
			logger.Error("scheduler: failed to start cron runner", "error", err)
		}
		s.mu.Unlock()

		// Hold the lock until we lose it or context is cancelled.
		// Since alan.Lock blocks until acquired, and doesn't return a channel
		// to signal loss (it's a simple mutex-style lock), in this implementation
		// holding the lock means we are the leader. We only release it on shutdown.
		//
		// However, alan's lock implementation (based on consul/redis/etc) usually
		// has a session TTL. If we crash, the lock is released.
		// If we want to actively monitor lock health or handle session invalidation,
		// we'd need a more advanced API from the cluster package.
		//
		// For now, assuming LockScheduler blocks indefinitely once acquired is incorrect
		// for most distributed locks (they usually return immediately if acquired,
		// or block until available). Based on typical patterns:
		// 1. Lock() blocks until acquired.
		// 2. Once acquired, we are the leader.
		// 3. We should keep running until shutdown.

		// Wait for context cancellation to release lock.
		<-ctx.Done()

		logger.Info("scheduler: releasing leader lock")
		s.Stop() // Stop the runner
		s.cluster.UnlockScheduler()
		return
	}
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
		logi.Ctx(s.ctx).Info("scheduler: no enabled cron triggers found")
		return nil
	}

	// Build hardloop Cron jobs from triggers.
	crons := make([]hardloop.Cron, 0, len(triggers))
	for _, t := range triggers {
		schedule, _ := t.Config["schedule"].(string)
		if schedule == "" {
			logi.Ctx(s.ctx).Warn("scheduler: cron trigger has no schedule, skipping",
				"trigger_id", t.ID, "workflow_id", t.WorkflowID)
			continue
		}

		// Capture for closure.
		trigger := t
		cronSpec := schedule
		timezone, _ := t.Config["timezone"].(string)

		if timezone != "" {
			cronSpec = "CRON_TZ=" + timezone + " " + cronSpec
		}

		crons = append(crons, hardloop.Cron{
			Name:  fmt.Sprintf("trigger-%s", trigger.ID),
			Specs: []string{cronSpec},
			Func:  s.makeCronFunc(trigger),
		})
	}

	if len(crons) == 0 {
		logi.Ctx(s.ctx).Info("scheduler: no valid cron specs after filtering")
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

	logi.Ctx(s.ctx).Info("scheduler: started cron triggers", "count", len(crons))

	return nil
}

// makeCronFunc returns the function that hardloop will call on each cron tick
// for a given trigger. It loads the workflow, builds inputs with trigger
// metadata, and runs the engine. If a RunRegistrar is set, the run is
// registered for tracking and cancellation.
func (s *Scheduler) makeCronFunc(trigger service.Trigger) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		logi.Ctx(ctx).Info("scheduler: cron triggered",
			"trigger_id", trigger.ID,
			"workflow_id", trigger.WorkflowID)

		// Load the workflow from the store.
		wf, err := s.workflowStore.GetWorkflow(ctx, trigger.WorkflowID)
		if err != nil {
			logi.Ctx(ctx).Error("scheduler: get workflow failed",
				"trigger_id", trigger.ID,
				"workflow_id", trigger.WorkflowID,
				"error", err)
			return nil // don't stop the cron loop on transient errors
		}

		if wf == nil {
			logi.Ctx(ctx).Warn("scheduler: workflow not found, skipping",
				"trigger_id", trigger.ID,
				"workflow_id", trigger.WorkflowID)
			return nil
		}

		// Use the active version's graph if available.
		graphToRun := wf.Graph
		if wf.ActiveVersion != nil && s.workflowVersionStore != nil {
			ver, err := s.workflowVersionStore.GetWorkflowVersion(ctx, trigger.WorkflowID, *wf.ActiveVersion)
			if err != nil {
				logi.Ctx(ctx).Error("scheduler: get active version failed",
					"trigger_id", trigger.ID,
					"workflow_id", trigger.WorkflowID,
					"version", *wf.ActiveVersion,
					"error", err)
				// Fall back to wf.Graph on error.
			} else if ver != nil {
				graphToRun = ver.Graph
			}
		}

		// Build trigger metadata inputs (merged with static payload by the
		// cron_trigger node).
		schedule, _ := trigger.Config["schedule"].(string)
		timezone, _ := trigger.Config["timezone"].(string)
		inputs := map[string]any{
			"trigger_type": "cron",
			"trigger_id":   trigger.ID,
			"triggered_at": time.Now().UTC().Format(time.RFC3339),
			"schedule":     schedule,
			"timezone":     timezone,
		}

		// Register the run for tracking if a registrar is available.
		var runID string
		runCtx := ctx
		if s.runRegistrar != nil {
			var cleanup func()
			runID, runCtx, cleanup = s.runRegistrar(ctx, trigger.WorkflowID, "cron")
			defer cleanup()
		}

		// Enrich context with workflow metadata for structured logging.
		runCtx = logi.WithContext(runCtx, slog.With(
			slog.String("workflow_id", trigger.WorkflowID),
			slog.String("workflow_name", wf.Name),
		))

		// Build a workflow lookup function for workflow_call nodes.
		var workflowLookup WorkflowLookup
		if s.workflowStore != nil {
			workflowLookup = func(ctx context.Context, id string) (*service.Workflow, error) {
				return s.workflowStore.GetWorkflow(ctx, id)
			}
		}

		engine := NewEngine(s.providerLookup, s.skillLookup, s.varLookup, s.varLister, s.nodeConfigLookup, workflowLookup)

		// Find the specific cron_trigger node that matches this trigger's ID.
		var entryNodeIDs []string
		for _, n := range graphToRun.Nodes {
			if n.Type == "cron_trigger" {
				if tid, _ := n.Data["trigger_id"].(string); tid == trigger.ID {
					entryNodeIDs = append(entryNodeIDs, n.ID)
				}
			}
		}

		logi.Ctx(runCtx).Info("scheduler: workflow started",
			"trigger_id", trigger.ID,
			"workflow_id", trigger.WorkflowID,
			"run_id", runID)
		result, err := engine.Run(runCtx, graphToRun, inputs, entryNodeIDs, nil)
		if err != nil {
			logi.Ctx(runCtx).Error("scheduler: workflow execution failed",
				"trigger_id", trigger.ID,
				"workflow_id", trigger.WorkflowID,
				"run_id", runID,
				"error", err)
			return nil // don't stop the cron loop
		}

		logi.Ctx(runCtx).Info("scheduler: workflow completed",
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
