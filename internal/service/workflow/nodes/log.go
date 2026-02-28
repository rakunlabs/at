package nodes

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/rakunlabs/at/internal/render"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
	"github.com/rakunlabs/logi"
)

// logNode logs the incoming data at a configurable level and passes it through
// unchanged. The message field supports Go text/template syntax.
//
// Config (node.Data):
//
//	"level":   string — "info" | "warn" | "error" | "debug" (default "info")
//	"message": string — Go template rendered with input data (optional)
//
// Input ports:  "data" — upstream data
// Output ports: "data" — same data passed through unchanged
type logNode struct {
	level   slog.Level
	message string
}

var validLevels = map[string]slog.Level{
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
	"debug": slog.LevelDebug,
}

func init() {
	workflow.RegisterNodeType("log", newLogNode)
}

func newLogNode(node service.WorkflowNode) (workflow.Noder, error) {
	levelStr, _ := node.Data["level"].(string)
	if levelStr == "" {
		levelStr = "info"
	}

	level, ok := validLevels[strings.ToLower(levelStr)]
	if !ok {
		return nil, fmt.Errorf("log: invalid level %q (must be info, warn, error, or debug)", levelStr)
	}

	message, _ := node.Data["message"].(string)

	return &logNode{level: level, message: message}, nil
}

func (n *logNode) Type() string { return "log" }

func (n *logNode) Validate(_ context.Context, _ *workflow.Registry) error {
	return nil
}

func (n *logNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	// Build the log message.
	msg := "log"
	if n.message != "" {
		// Flatten input data for template context (same pattern as template node).
		tmplCtx := any(inputs)
		if len(inputs) == 1 {
			if data, ok := inputs["data"]; ok {
				if m, ok := data.(map[string]any); ok {
					tmplCtx = m
				}
			}
		}

		rendered, err := render.ExecuteWithFuncs(n.message, tmplCtx, varFuncMap(reg))
		if err != nil {
			return nil, fmt.Errorf("log: template error: %w", err)
		}
		msg = string(rendered)
	}

	// Collect the input data for structured logging.
	var attrs []any
	if data, ok := inputs["data"]; ok {
		attrs = append(attrs, "data", data)
	}

	logi.Ctx(ctx).Log(ctx, n.level, "script: "+msg, attrs...)

	// Pass data through unchanged.
	out := make(map[string]any, len(inputs))
	for k, v := range inputs {
		out[k] = v
	}

	return workflow.NewResult(out), nil
}
