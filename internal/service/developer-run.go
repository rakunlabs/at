package service

import "context"

type developerControlContextKey struct{}

func ContextWithDeveloperSpaceControl(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, developerControlContextKey{}, id)
}

func DeveloperSpaceControlFromContext(ctx context.Context) string {
	id, _ := ctx.Value(developerControlContextKey{}).(string)
	return id
}

// DeveloperSpaceControlStorer serializes Start/Stop/Reset across replicas and
// closes admission before runtime effects. Control receipts do not expire:
// losing an owner cannot authorize conflicting workload/storage changes.
type DeveloperSpaceControlStorer interface {
	AcquireDeveloperSpaceControl(context.Context, string, string, string) error
	ReleaseDeveloperSpaceControl(context.Context, string, string, bool) error
}
