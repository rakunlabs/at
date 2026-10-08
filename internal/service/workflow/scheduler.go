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
	"errors"
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

// RunRecorder opens a run-history record and returns the function that
// closes it with the run's outcome.
type RunRecorder func(ctx context.Context, runID, workflowID, source, triggerID string) (finish func(*RunResult, error))

// CronRun is an open cron run record. Ctx carries the run so launchers that
// outlive the tick (organization tasks) can close it later.
type CronRun struct {
	ID              string
	Source          string
	LinkWorkflowRun func(runID string)
	LinkTask        func(taskID, identifier string)
	Log             func(level, message string)
	Report          func(status, summary string)
	Finish          func(status, errMsg, result string)
}

func noopCronRun() *CronRun {
	return &CronRun{
		LinkWorkflowRun: func(string) {},
		LinkTask:        func(string, string) {},
		Log:             func(string, string) {},
		Report:          func(string, string) {},
		Finish:          func(string, string, string) {},
	}
}

type cronRunContextKey struct{}

// ContextWithCronRun attaches an open cron run to ctx.
func ContextWithCronRun(ctx context.Context, run *CronRun) context.Context {
	return context.WithValue(ctx, cronRunContextKey{}, run)
}

// CronRunFromContext returns the cron run attached to ctx, or a no-op run.
func CronRunFromContext(ctx context.Context) *CronRun {
	if run, ok := LookupCronRun(ctx); ok {
		return run
	}
	return noopCronRun()
}

// LookupCronRun reports whether ctx carries a recorded cron run.
func LookupCronRun(ctx context.Context) (*CronRun, bool) {
	run, ok := ctx.Value(cronRunContextKey{}).(*CronRun)
	return run, ok && run != nil && run.ID != ""
}

// CronRunRecorder opens a cron run record under the trigger's bound
// execution identity. It never fails the run; a nil result means unrecorded.
type CronRunRecorder func(ctx context.Context, trigger service.Trigger, source, triggeredBy string) *CronRun

// ErrCronUnavailable reports that cron execution is not possible right now.
var ErrCronUnavailable = errors.New("cron execution unavailable")

// Scheduler manages cron-based workflow triggers.
type Scheduler struct {
	triggerStore          service.TriggerStorer
	workflowStore         service.WorkflowStorer
	workflowVersionStore  service.WorkflowVersionStorer
	providerLookup        ProviderLookup
	scopedProviderLookup  func(context.Context, string) (service.LLMProvider, string, error)
	skillLookup           SkillLookup
	mcpSetToolLister      MCPSetToolListerFunc
	mcpSetToolCaller      MCPSetToolCallerFunc
	varLookup             VarLookup
	varLister             VarLister
	scopedVarLister       func(context.Context) (map[string]string, error)
	nodeConfigLookup      NodeConfigLookup
	agentStore            service.AgentStorer
	varSave               VarSaveFunc
	builtinToolDispatcher BuiltinToolDispatcher
	builtinToolDefs       []BuiltinToolDef
	chatMessageCreator    ChatMessageCreatorFunc
	chatSessionLookup     ChatSessionLookupFunc
	recordUsage           RecordUsageFunc
	checkBudget           CheckBudgetFunc
	recordObservation     RecordObservationFunc
	goalAncestry          GoalAncestryFunc
	connectionLookup      ConnectionLookup
	workflowByNameLookup  WorkflowByNameLookupFunc
	workflowExecutor      WorkflowExecutorFunc
	loopGov               LoopGovernor
	runRegistrar          RunRegistrar
	runRecorder           RunRecorder
	enabledCheck          func(context.Context) bool
	executionContext      func(context.Context, string) (context.Context, error)
	durableLaunch         func(context.Context, string, service.WorkflowGraph, map[string]any, []string, string) error
	organizationLauncher  func(context.Context, service.Trigger) error
	cronRunRecorder       CronRunRecorder
	cluster               *cluster.Cluster

	mu     sync.Mutex
	cron   cronRunner
	cancel context.CancelFunc
	ctx    context.Context // parent context from Start()
}

func (s *Scheduler) SetMCPSetTools(lister MCPSetToolListerFunc, caller MCPSetToolCallerFunc) {
	s.mcpSetToolLister = lister
	s.mcpSetToolCaller = caller
}

type ScheduleStorer interface {
	service.TriggerStorer
	service.WorkflowStorer
	service.WorkflowVersionStorer
	service.AgentStorer
}

// NewScheduler creates a new cron trigger scheduler.
func NewScheduler(st ScheduleStorer, lookup ProviderLookup, skillLookup SkillLookup, varLookup VarLookup, varLister VarLister, nodeConfigLookup NodeConfigLookup, varSave VarSaveFunc, builtinDispatcher BuiltinToolDispatcher, builtinDefs []BuiltinToolDef, chatMessageCreator ChatMessageCreatorFunc, chatSessionLookup ChatSessionLookupFunc, recordUsage RecordUsageFunc, checkBudget CheckBudgetFunc, recordObservation RecordObservationFunc, goalAncestry GoalAncestryFunc, cl *cluster.Cluster) *Scheduler {
	return &Scheduler{
		triggerStore:          st,
		workflowStore:         st,
		workflowVersionStore:  st,
		providerLookup:        lookup,
		skillLookup:           skillLookup,
		varLookup:             varLookup,
		varLister:             varLister,
		nodeConfigLookup:      nodeConfigLookup,
		agentStore:            st,
		varSave:               varSave,
		builtinToolDispatcher: builtinDispatcher,
		builtinToolDefs:       builtinDefs,
		chatMessageCreator:    chatMessageCreator,
		chatSessionLookup:     chatSessionLookup,
		recordUsage:           recordUsage,
		checkBudget:           checkBudget,
		recordObservation:     recordObservation,
		goalAncestry:          goalAncestry,
		cluster:               cl,
	}
}

// SetRunRegistrar sets the callback used to register runs for tracking.
// Must be called before Start.
func (s *Scheduler) SetRunRegistrar(r RunRegistrar) {
	s.runRegistrar = r
}

// SetRunRecorder sets the callback that records cron runs in run history.
func (s *Scheduler) SetRunRecorder(r RunRecorder) {
	s.runRecorder = r
}

func (s *Scheduler) SetDurableLauncher(launch func(context.Context, string, service.WorkflowGraph, map[string]any, []string, string) error) {
	s.durableLaunch = launch
}

// SetOrganizationLauncher installs the callback that opens a task for
// organization-target cron triggers. It runs under the trigger's bound
// execution identity. Must be called before Start.
func (s *Scheduler) SetOrganizationLauncher(launch func(context.Context, service.Trigger) error) {
	s.organizationLauncher = launch
}

// SetCronRunRecorder installs the cron run history recorder. Optional.
func (s *Scheduler) SetCronRunRecorder(r CronRunRecorder) {
	s.cronRunRecorder = r
}

// SetExecutionContext installs a resolver for persisted trigger initiators.
// Must be called before Start. A missing resolver disables execution, not auth.
func (s *Scheduler) SetExecutionContext(resolve func(context.Context, string) (context.Context, error)) {
	s.executionContext = resolve
}

func (s *Scheduler) SetScopedVarLister(list func(context.Context) (map[string]string, error)) {
	s.scopedVarLister = list
}

func (s *Scheduler) SetScopedProviderLookup(lookup func(context.Context, string) (service.LLMProvider, string, error)) {
	s.scopedProviderLookup = lookup
}

// SetEnabledCheck installs a runtime guard for cron dispatch. When it returns
// false, the scheduler does not load or execute cron triggers.
func (s *Scheduler) SetEnabledCheck(f func(context.Context) bool) {
	s.enabledCheck = f
}

// SetLoopGov installs the loop governor used by the agent_call node
// inside scheduled workflows. Optional — if unset, the node falls back
// to legacy unbounded behaviour.
func (s *Scheduler) SetLoopGov(gov LoopGovernor) {
	s.loopGov = gov
}

// SetConnectionLookup sets the callback used by agent_call nodes to resolve
// a named Connection by ID. Optional — when nil, provider-scoped variable
// keys inside agent loops resolve only against global variables.
func (s *Scheduler) SetConnectionLookup(f ConnectionLookup) {
	s.connectionLookup = f
}

// SetWorkflowByNameLookup sets the callback used by agent_call nodes to
// resolve AgentConfig.Workflows by name. Optional — when nil, agents cannot
// attach workflows directly.
func (s *Scheduler) SetWorkflowByNameLookup(f WorkflowByNameLookupFunc) {
	s.workflowByNameLookup = f
}

// SetWorkflowExecutor sets the callback used by agent_call nodes to dispatch
// `wf_*` tool calls to the workflow engine.
func (s *Scheduler) SetWorkflowExecutor(f WorkflowExecutorFunc) {
	s.workflowExecutor = f
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
	if s.enabledCheck != nil && !s.enabledCheck(s.ctx) {
		logi.Ctx(s.ctx).Info("scheduler: disabled by feature flag")
		return nil
	}

	var triggers []service.Trigger
	if services, ok := s.triggerStore.(service.ExecutionServiceLister); ok && s.executionContext != nil {
		bindings, err := services.ListExecutionServiceBindings(s.ctx, "trigger")
		if err != nil {
			return fmt.Errorf("scheduler: enumerate execution bindings: %w", err)
		}
		for _, binding := range bindings {
			ctx, err := s.executionContext(s.ctx, binding.SubjectID)
			if err != nil {
				continue
			}
			trigger, err := s.triggerStore.GetTrigger(ctx, binding.SubjectID)
			if err != nil || trigger == nil || !trigger.Enabled || trigger.Type != "cron" {
				continue
			}
			triggers = append(triggers, *trigger)
		}
	} else {
		// Compatibility stores require explicit scoped boot authority too.
		if err := service.CheckExecution(s.ctx, service.ExecutionAction{Kind: "resource", Name: "execution.run"}); err != nil {
			return fmt.Errorf("scheduler: boot authority unavailable: %w", err)
		}
		var err error
		triggers, err = s.triggerStore.ListEnabledCronTriggers(s.ctx)
		if err != nil {
			return fmt.Errorf("scheduler: load cron triggers: %w", err)
		}
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
		if s.enabledCheck != nil && !s.enabledCheck(ctx) {
			logi.Ctx(ctx).Info("scheduler: cron skipped because automation is disabled", "trigger_id", trigger.ID)
			return nil
		}
		_ = s.fire(ctx, trigger, service.CronRunSourceSchedule, "")
		return nil // never stop the cron loop
	}
}

// RunNow fires a cron trigger once, outside its schedule. The trigger is
// re-read under its own bound execution identity and runs exactly as a
// scheduled tick would (same identity, history and notifications), in the
// background. The caller must already be admitted to manage the trigger.
func (s *Scheduler) RunNow(triggerID, triggeredBy string) error {
	if s.ctx == nil || s.executionContext == nil {
		return ErrCronUnavailable
	}
	if s.enabledCheck != nil && !s.enabledCheck(s.ctx) {
		return fmt.Errorf("%w: cron triggers are disabled", ErrCronUnavailable)
	}
	boundCtx, err := s.executionContext(s.ctx, triggerID)
	if err != nil {
		return fmt.Errorf("%w: bind an execution identity (Run as) for this schedule first", service.ErrExecutionDenied)
	}
	trigger, err := s.triggerStore.GetTrigger(boundCtx, triggerID)
	if err != nil {
		return fmt.Errorf("load trigger: %w", err)
	}
	if trigger == nil || trigger.Type != "cron" {
		return service.ErrAccessResourceNotFound
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logi.Ctx(s.ctx).Error("scheduler: manual run panicked", "trigger_id", triggerID, "panic", r)
			}
		}()
		_ = s.fire(s.ctx, *trigger, service.CronRunSourceManual, triggeredBy)
	}()
	return nil
}

// fire runs one firing of a trigger and records it in cron run history.
func (s *Scheduler) fire(ctx context.Context, trigger service.Trigger, source, triggeredBy string) error {
	logi.Ctx(ctx).Info("scheduler: cron triggered",
		"trigger_id", trigger.ID,
		"workflow_id", trigger.WorkflowID,
		"target_type", trigger.TargetType,
		"source", source)

	if s.executionContext == nil {
		logi.Ctx(ctx).Warn("scheduler: execution identity resolver not configured", "trigger_id", trigger.ID)
		return ErrCronUnavailable
	}
	boundCtx, authErr := s.executionContext(ctx, trigger.ID)
	run := noopCronRun()
	if authErr == nil && s.cronRunRecorder != nil {
		if r := s.cronRunRecorder(boundCtx, trigger, source, triggeredBy); r != nil {
			run = r
		}
	}
	if trigger.TargetType == service.TriggerTargetOrganization {
		if s.organizationLauncher == nil {
			logi.Ctx(ctx).Warn("scheduler: organization launcher not configured", "trigger_id", trigger.ID)
			run.Finish(service.CronRunFailed, "organization launcher not configured", "")
			return ErrCronUnavailable
		}
		if authErr == nil {
			authErr = service.CheckExecution(boundCtx, service.ExecutionAction{Kind: "resource", Name: "organizations.run", ResourceID: trigger.TargetID})
		}
		if authErr != nil {
			logi.Ctx(ctx).Warn("scheduler: execution authority denied", "trigger_id", trigger.ID, "error", authErr)
			run.Finish(service.CronRunFailed, "execution authority denied: "+authErr.Error(), "")
			return authErr
		}
		// The launcher owns the run from here: it stays open until the
		// task it starts finishes.
		if err := s.organizationLauncher(ContextWithCronRun(boundCtx, run), trigger); err != nil {
			logi.Ctx(ctx).Error("scheduler: organization task launch failed", "trigger_id", trigger.ID, "organization_id", trigger.TargetID, "error", err)
			run.Finish(service.CronRunFailed, err.Error(), "")
			return err
		}
		return nil
	}
	if authErr == nil {
		authErr = service.CheckExecution(boundCtx, service.ExecutionAction{Kind: "resource", Name: "workflows.run", ResourceID: trigger.WorkflowID})
	}
	if authErr != nil {
		logi.Ctx(ctx).Warn("scheduler: execution authority denied", "trigger_id", trigger.ID, "error", authErr)
		run.Finish(service.CronRunFailed, "execution authority denied: "+authErr.Error(), "")
		return authErr
	}
	ctx = boundCtx
	wf, err := s.workflowStore.GetWorkflow(ctx, trigger.WorkflowID)
	if err != nil {
		logi.Ctx(ctx).Error("scheduler: get workflow failed",
			"trigger_id", trigger.ID,
			"workflow_id", trigger.WorkflowID,
			"error", err)
		run.Finish(service.CronRunFailed, "load workflow: "+err.Error(), "")
		return err
	}

	if wf == nil {
		logi.Ctx(ctx).Warn("scheduler: workflow not found, skipping",
			"trigger_id", trigger.ID,
			"workflow_id", trigger.WorkflowID)
		run.Finish(service.CronRunFailed, "workflow not found", "")
		return service.ErrAccessResourceNotFound
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

	// Build an agent lookup function for agent_call nodes.
	var agentLookup AgentLookup
	if s.agentStore != nil {
		agentLookup = func(ctx context.Context, id string) (*service.Agent, error) {
			return s.agentStore.GetAgent(ctx, id)
		}
	}

	// Build a version lookup function for workflow_call nodes.
	var versionLookup VersionLookupFunc
	if s.workflowStore != nil && s.workflowVersionStore != nil {
		versionLookup = func(ctx context.Context, workflowID string) (*service.WorkflowGraph, error) {
			wf, err := s.workflowStore.GetWorkflow(ctx, workflowID)
			if err != nil {
				return nil, fmt.Errorf("get workflow %s: %w", workflowID, err)
			}
			if wf == nil || wf.ActiveVersion == nil {
				return nil, nil
			}
			ver, err := s.workflowVersionStore.GetWorkflowVersion(ctx, workflowID, *wf.ActiveVersion)
			if err != nil {
				return nil, fmt.Errorf("get workflow version %s v%d: %w", workflowID, *wf.ActiveVersion, err)
			}
			if ver == nil {
				return nil, nil
			}
			return &ver.Graph, nil
		}
	}

	engine := NewEngineWithDependencies(Dependencies{
		ProviderLookup:        s.providerLookup,
		ScopedProviderLookup:  s.scopedProviderLookup,
		SkillLookup:           s.skillLookup,
		MCPSetToolLister:      s.mcpSetToolLister,
		MCPSetToolCaller:      s.mcpSetToolCaller,
		VarLookup:             s.varLookup,
		VarLister:             s.varLister,
		ScopedVarLister:       s.scopedVarLister,
		NodeConfigLookup:      s.nodeConfigLookup,
		WorkflowLookup:        workflowLookup,
		WorkflowByNameLookup:  s.workflowByNameLookup,
		WorkflowExecutor:      s.workflowExecutor,
		AgentLookup:           agentLookup,
		ConnectionLookup:      s.connectionLookup,
		VarSave:               s.varSave,
		BuiltinToolDispatcher: s.builtinToolDispatcher,
		BuiltinToolDefs:       s.builtinToolDefs,
		ChatMessageCreator:    s.chatMessageCreator,
		ChatSessionLookup:     s.chatSessionLookup,
		RecordUsage:           s.recordUsage,
		CheckBudget:           s.checkBudget,
		RecordObservation:     s.recordObservation,
		GoalAncestry:          s.goalAncestry,
		VersionLookup:         versionLookup,
		LoopGov:               s.loopGov,
	})

	// Determine entry node(s) for this trigger.
	var entryNodeIDs []string
	if trigger.EntryNodeID != "" {
		// Trigger specifies a particular input node to start from.
		entryNodeIDs = []string{trigger.EntryNodeID}
	} else {
		// Fallback: use all input nodes (same as manual run).
		for _, n := range graphToRun.Nodes {
			if n.Type == "input" {
				entryNodeIDs = append(entryNodeIDs, n.ID)
			}
		}
	}

	if HasDurableWait(graphToRun, entryNodeIDs) {
		if s.durableLaunch == nil {
			logi.Ctx(runCtx).Error("scheduler: durable workflow launcher unavailable", "workflow_id", wf.ID)
			run.Finish(service.CronRunFailed, "durable workflow launcher unavailable", "")
			return ErrCronUnavailable
		}
		if err := s.durableLaunch(runCtx, wf.ID, graphToRun, inputs, entryNodeIDs, "cron"); err != nil {
			logi.Ctx(runCtx).Error("scheduler: durable workflow launch failed", "workflow_id", wf.ID, "error", err)
			run.Finish(service.CronRunFailed, "durable launch: "+err.Error(), "")
			return err
		}
		run.Log(service.CronRunLogSystem, "Queued as a durable workflow execution (it contains a Wait step); follow it under Runs.")
		run.Finish(service.CronRunCompleted, "", "queued as durable execution")
		return nil
	}

	logi.Ctx(runCtx).Info("scheduler: workflow started",
		"trigger_id", trigger.ID,
		"workflow_id", trigger.WorkflowID,
		"run_id", runID)
	finish := func(*RunResult, error) {}
	if s.runRecorder != nil && runID != "" {
		finish = s.runRecorder(runCtx, runID, trigger.WorkflowID, "cron", trigger.ID)
	}
	if runID != "" {
		run.LinkWorkflowRun(runID)
	}
	run.Log(service.CronRunLogSystem, "Workflow "+wf.Name+" started.")
	result, err := engine.Run(ContextWithCronRun(runCtx, run), graphToRun, inputs, entryNodeIDs, nil)
	finish(result, err)
	if err != nil {
		logi.Ctx(runCtx).Error("scheduler: workflow execution failed",
			"trigger_id", trigger.ID,
			"workflow_id", trigger.WorkflowID,
			"run_id", runID,
			"error", err)
		status := service.CronRunFailed
		if errors.Is(err, context.Canceled) {
			status = service.CronRunCancelled
		}
		run.Finish(status, err.Error(), "")
		return err
	}
	if result != nil {
		for _, h := range result.HandledErrors {
			run.Log(service.CronRunLogError, fmt.Sprintf("Step %s (%s) failed and was skipped: %s", h.NodeID, h.NodeType, h.Error))
		}
	}

	logi.Ctx(runCtx).Info("scheduler: workflow completed",
		"trigger_id", trigger.ID,
		"workflow_id", trigger.WorkflowID,
		"run_id", runID,
		"output_keys", mapKeys(result.Outputs))
	run.Finish(service.CronRunCompleted, "", "")

	return nil
}

// mapKeys returns the keys of a map for logging.
func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
