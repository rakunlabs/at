package service

import "context"

// ExecutionServiceBinding is a revocable server-side machine principal. Its ID
// is not a bearer credential. Only authenticated bot/webhook dispatch or the
// scheduler may resolve it into execution authority. Membership and policy
// ceilings are renewed explicitly; originating browser sessions need not live
// forever for scheduled automation to work.
type ExecutionServiceBinding struct {
	ID                string `json:"id" db:"id"`
	WorkspaceID       string `json:"workspace_id" db:"workspace_id"`
	UserID            string `json:"user_id" db:"user_id"`
	OwnerUserID       string `json:"owner_user_id" db:"owner_user_id"`
	Kind              string `json:"kind" db:"kind"`
	SubjectID         string `json:"subject_id" db:"subject_id"`
	Version           int64  `json:"version" db:"version"`
	MembershipVersion int64  `json:"membership_version" db:"membership_version"`
	PolicyVersion     int64  `json:"policy_version" db:"policy_version"`
	Revoked           bool   `json:"revoked" db:"revoked"`
}

type ExecutionServiceStorer interface {
	GetExecutionServiceBinding(context.Context, string, string) (*ExecutionServiceBinding, error)
	SaveExecutionServiceBinding(context.Context, ExecutionServiceBinding) (*ExecutionServiceBinding, error)
}

type ExecutionServiceLister interface {
	ListExecutionServiceBindings(context.Context, string) ([]ExecutionServiceBinding, error)
}

type ExecutionCleanupStorer interface {
	GetExecutionCleanupTask(context.Context, string, string) (*Task, error)
}

type executionMaintenanceKey struct{}

// WithExecutionMaintenance is a boot-only catalog enumeration credential. It
// grants no workspace/model/tool authority; each discovered subject must still
// resolve its own live service binding before any business-store access.
func WithExecutionMaintenance(ctx context.Context) context.Context {
	return context.WithValue(ctx, executionMaintenanceKey{}, true)
}
func HasExecutionMaintenance(ctx context.Context) bool {
	v, _ := ctx.Value(executionMaintenanceKey{}).(bool)
	return v
}
