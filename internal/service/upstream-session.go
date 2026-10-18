package service

import "context"

type upstreamSessionKey struct{}

// ContextWithUpstreamSession names the conversation an upstream model call
// belongs to. Providers that route and cache per conversation (OpenCode Go
// requires x-opencode-session) receive it as a header.
func ContextWithUpstreamSession(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, upstreamSessionKey{}, id)
}

// UpstreamSessionFromContext returns the explicit upstream session, else the
// session or trace of the enclosing agent run, else "".
func UpstreamSessionFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(upstreamSessionKey{}).(string); ok && id != "" {
		return id
	}
	if tp, ok := TraceParentFromContext(ctx); ok {
		if tp.SessionID != "" {
			return tp.SessionID
		}
		return tp.TraceID
	}
	return ""
}
