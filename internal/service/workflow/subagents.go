package workflow

import "context"

type subagentDepthContextKey struct{}

func SubagentDepth(ctx context.Context) int {
	depth, _ := ctx.Value(subagentDepthContextKey{}).(int)
	return depth
}

func ContextWithSubagentDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, subagentDepthContextKey{}, depth)
}
