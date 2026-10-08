package service

import (
	"context"
	"errors"
	"time"
)

// Cron run history: one row per firing of a cron trigger, scheduled or
// started by hand, with a bounded log. Organization tasks keep running after
// the tick that launched them, so the row stays open until the task finishes
// and the agents working on it can append milestones (run_log and
// telegram_notify) while it runs.

const (
	CronRunSourceSchedule = "schedule"
	CronRunSourceManual   = "manual"

	CronRunRunning   = "running"
	CronRunCompleted = "completed"
	CronRunFailed    = "failed"
	CronRunBlocked   = "blocked"
	CronRunCancelled = "cancelled"

	CronRunLogSystem    = "system"
	CronRunLogMilestone = "milestone"
	CronRunLogReport    = "report"
	CronRunLogError     = "error"

	// CronRunRetention is how many runs are kept per trigger.
	CronRunRetention = 200
	// CronRunLogMax bounds the entries of one run.
	CronRunLogMax = 200
)

// ErrCronRunLogFull reports that a run already holds CronRunLogMax entries.
var ErrCronRunLogFull = errors.New("run log is full")

// ValidCronRunReport lists the outcomes an agent may report for its run.
func ValidCronRunReport(status string) bool {
	switch status {
	case "completed", "partial", "failed":
		return true
	}
	return false
}

type CronRun struct {
	ID             string `json:"id"`
	WorkspaceID    string `json:"workspace_id"`
	TriggerID      string `json:"trigger_id"`
	TargetType     string `json:"target_type"`
	TargetID       string `json:"target_id"`
	Source         string `json:"source"`
	Status         string `json:"status"`
	TaskID         string `json:"task_id"`
	TaskIdentifier string `json:"task_identifier"`
	WorkflowRunID  string `json:"workflow_run_id"`
	TriggeredBy    string `json:"triggered_by"`
	Error          string `json:"error"`
	Result         string `json:"result"`
	// ReportedStatus and ReportedSummary are what the agent itself said about
	// the outcome (run_log with a status). They are a claim, shown next to
	// the system status rather than replacing it.
	ReportedStatus  string     `json:"reported_status"`
	ReportedSummary string     `json:"reported_summary"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
	LogCount        int        `json:"log_count"`
	// TaskStatus is the live status of the linked task, filled in by the API.
	TaskStatus string       `json:"task_status,omitempty"`
	Logs       []CronRunLog `json:"logs,omitempty"`
}

type CronRunLog struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

type CronRunQuery struct {
	TriggerID string
	Before    string // run ID cursor
	Limit     int
}

type CronRunStorer interface {
	// Writes are bound to the run's execution identity: the workspace comes
	// from the execution provenance, never from the caller.
	StartCronRun(ctx context.Context, run CronRun) (*CronRun, error)
	LinkCronRun(ctx context.Context, id, taskID, taskIdentifier, workflowRunID string) error
	FinishCronRun(ctx context.Context, id, status, errMsg, result string) error
	ReportCronRun(ctx context.Context, id, status, summary string) error
	AppendCronRunLog(ctx context.Context, runID, level, message string) error
	CronRunForTask(ctx context.Context, rootTaskID string) (string, error)

	// Reads follow trigger visibility.
	ListCronRuns(ctx context.Context, q CronRunQuery) ([]CronRun, error)
	GetCronRun(ctx context.Context, id string) (*CronRun, error)
}
