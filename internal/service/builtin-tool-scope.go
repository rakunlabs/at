package service

import "context"

type builtinToolScopeKey struct{}

type builtinToolScope struct {
	allowed func(name string) bool
}

// ContextWithBuiltinToolScope records which built-in tools the calling surface
// (an MCP server, an agent's tool list, a Chats setup) exposes. Tools that call
// other tools by name, such as batch_execute, must stay within it. A nil
// allowed func marks a surface whose caller may already call every built-in
// directly.
func ContextWithBuiltinToolScope(ctx context.Context, allowed func(name string) bool) context.Context {
	return context.WithValue(ctx, builtinToolScopeKey{}, builtinToolScope{allowed: allowed})
}

// BuiltinToolInScope reports whether name is exposed by the calling surface.
// Without a recorded scope it answers false, so a new surface that forgets to
// declare one fails closed.
func BuiltinToolInScope(ctx context.Context, name string) bool {
	scope, ok := ctx.Value(builtinToolScopeKey{}).(builtinToolScope)
	if !ok {
		return false
	}
	return scope.allowed == nil || scope.allowed(name)
}
