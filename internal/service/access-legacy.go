package service

import "context"

type legacyWorkspaceKey struct{}

// WithLegacyWorkspaceAccess is an explicit installation compatibility boundary.
// Call only after installation authorization, or from trusted bootstrap code.
// It bounds business data to legacy-default; it is not a human membership grant.
func WithLegacyWorkspaceAccess(ctx context.Context) context.Context {
	return context.WithValue(ctx, legacyWorkspaceKey{}, true)
}
func LegacyWorkspaceAccessFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(legacyWorkspaceKey{}).(bool)
	return v
}
