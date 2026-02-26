package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// ExecuteJSHandler runs a JS function body with the tool arguments as input.
// The handler is expected to be a function body (not a full function declaration)
// that can access `args` as the input arguments object and returns a string result.
//
// Example handler:
//
//	"return 'Hello, ' + args.name;"
func ExecuteJSHandler(handler string, args map[string]any, varLookup ...VarLookup) (string, error) {
	vm := goja.New()

	// Register all shared helpers (toString, jsonParse, btoa, atob,
	// JSON_stringify, httpGet, httpPost, httpPut, httpDelete, getVar).
	var vl VarLookup
	if len(varLookup) > 0 {
		vl = varLookup[0]
	}
	if err := SetupGojaVM(vm, map[string]any{"args": args}, vl); err != nil {
		return "", fmt.Errorf("js handler: setup VM: %w", err)
	}

	// Wrap the handler body in a function and call it.
	script := "(function() {\n" + handler + "\n})()"
	val, err := vm.RunString(script)
	if err != nil {
		return "", fmt.Errorf("js handler execution failed: %w", err)
	}

	if val == nil || goja.IsUndefined(val) || goja.IsNull(val) {
		return "", nil
	}

	exported := val.Export()
	switch v := exported.(type) {
	case string:
		return v, nil
	default:
		// Marshal non-string results as JSON.
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v), nil
		}
		return string(data), nil
	}
}

// ExecuteBashHandler runs a bash command with tool arguments and variables as
// environment variables. Tool arguments are set as ARG_<NAME> (uppercased,
// dots/hyphens replaced with underscores). All variables are set as VAR_<KEY>
// (uppercased). The command's stdout is returned as the tool result.
func ExecuteBashHandler(ctx context.Context, handler string, args map[string]any, varLister VarLister) (string, error) {
	const bashTimeout = 60 * time.Second

	ctx, cancel := context.WithTimeout(ctx, bashTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", handler)

	// Build environment variables from tool arguments.
	var env []string
	for k, v := range args {
		envKey := "ARG_" + strings.ToUpper(
			strings.NewReplacer(".", "_", "-", "_").Replace(k),
		)
		env = append(env, envKey+"="+fmt.Sprintf("%v", v))
	}

	// Inject all variables as VAR_<KEY> env vars.
	if varLister != nil {
		vars, err := varLister()
		if err != nil {
			slog.Warn("bash handler: failed to list variables", "error", err)
		} else {
			for k, v := range vars {
				envKey := "VAR_" + strings.ToUpper(
					strings.NewReplacer(".", "_", "-", "_").Replace(k),
				)
				env = append(env, envKey+"="+v)
			}
		}
	}

	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	slog.Debug("bash handler: executing", "handler_length", len(handler), "arg_count", len(args))

	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		if stderrStr != "" {
			slog.Warn("bash handler: stderr", "stderr", stderrStr)
		}
		return "", fmt.Errorf("bash handler failed: %w: %s", err, stderrStr)
	}

	if stderrStr := strings.TrimSpace(stderr.String()); stderrStr != "" {
		slog.Debug("bash handler: stderr", "stderr", stderrStr)
	}

	return strings.TrimSpace(stdout.String()), nil
}
