package service

import "context"

// CodeModeToolset is what a code_execute script may call: the tools of the
// surrounding run, already admitted the way that run admits them.
type CodeModeToolset struct {
	Tools []Tool
	Call  func(ctx context.Context, name string, args map[string]any) (string, error)
}

// CodeModeProvider builds the toolset lazily, with the bound execution context
// the code_execute call runs under. The returned cleanup (may be nil) runs when
// the script ends.
type CodeModeProvider func(ctx context.Context) (*CodeModeToolset, func(), error)

type codeModeKey struct{}

// ContextWithCodeModeTools records which tools a code_execute call on this
// context may reach. Surfaces that do not record one fall back to the
// built-in tools in scope (ContextWithBuiltinToolScope).
func ContextWithCodeModeTools(ctx context.Context, provider CodeModeProvider) context.Context {
	return context.WithValue(ctx, codeModeKey{}, provider)
}

// CodeModeToolsFromContext returns the recorded provider, if any.
func CodeModeToolsFromContext(ctx context.Context) (CodeModeProvider, bool) {
	provider, ok := ctx.Value(codeModeKey{}).(CodeModeProvider)
	return provider, ok && provider != nil
}
