package service

import "context"

type developerRunContextKey struct{}
type developerControlContextKey struct{}

func ContextWithDeveloperSpaceControl(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, developerControlContextKey{}, id)
}

func DeveloperSpaceControlFromContext(ctx context.Context) string {
	id, _ := ctx.Value(developerControlContextKey{}).(string)
	return id
}

func ContextWithDeveloperRun(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, developerRunContextKey{}, id)
}

func DeveloperRunFromContext(ctx context.Context) string {
	id, _ := ctx.Value(developerRunContextKey{}).(string)
	return id
}

// DeveloperRun is a durable exclusion receipt, not an expiring lock. A stale
// heartbeat never authorizes another run to replay unknown tool side effects.
type DeveloperRun struct {
	ID              string `json:"id"`
	HeartbeatAt     string `json:"heartbeat_at"`
	CancelRequested bool   `json:"cancel_requested"`
	Interrupted     bool   `json:"interrupted"`
}

type DeveloperRunStorer interface {
	AcquireDeveloperRun(context.Context, string, string) error
	HeartbeatDeveloperRun(context.Context, string, string) error
	ReleaseDeveloperRun(context.Context, string, string) error
	CancelDeveloperRun(context.Context, string) error
	GetDeveloperRun(context.Context, string) (*DeveloperRun, error)
}

// DeveloperSpaceControlStorer serializes Start/Stop/Reset across replicas and
// closes admission before runtime effects. Control receipts do not expire:
// losing an owner cannot authorize conflicting workload/storage changes.
type DeveloperSpaceControlStorer interface {
	AcquireDeveloperSpaceControl(context.Context, string, string, string) error
	ReleaseDeveloperSpaceControl(context.Context, string, string, bool) error
}
