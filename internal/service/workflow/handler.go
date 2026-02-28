package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/rakunlabs/logi"
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

// defaultBashTimeout is the default execution timeout for bash handlers.
const defaultBashTimeout = 60 * time.Second

// ExecuteBashHandler runs a bash command with tool arguments and variables as
// environment variables. The parent process environment is inherited, then tool
// arguments are overlaid as ARG_<NAME> (uppercased, dots/hyphens replaced with
// underscores) and all variables as VAR_<KEY> (uppercased).
// The timeout parameter controls execution duration; zero means the default 60s.
// The command's stdout is returned as the tool result.
func ExecuteBashHandler(ctx context.Context, handler string, args map[string]any, varLister VarLister, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = defaultBashTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", handler)

	// Start with the parent process environment so that PATH, HOME,
	// SSH_AUTH_SOCK, git config, etc. are available to the subprocess.
	env := os.Environ()

	// Overlay tool arguments as ARG_<NAME>.
	for k, v := range args {
		envKey := "ARG_" + strings.ToUpper(
			strings.NewReplacer(".", "_", "-", "_").Replace(k),
		)
		env = append(env, envKey+"="+fmt.Sprintf("%v", v))
	}

	// Overlay all variables as VAR_<KEY> env vars.
	if varLister != nil {
		vars, err := varLister()
		if err != nil {
			logi.Ctx(ctx).Warn("bash handler: failed to list variables", "error", err)
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

	logi.Ctx(ctx).Debug("bash handler: executing", "handler_length", len(handler), "arg_count", len(args))

	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		if stderrStr != "" {
			logi.Ctx(ctx).Warn("bash handler: stderr", "stderr", stderrStr)
		}
		return "", fmt.Errorf("bash handler failed: %w: %s", err, stderrStr)
	}

	if stderrStr := strings.TrimSpace(stderr.String()); stderrStr != "" {
		logi.Ctx(ctx).Debug("bash handler: stderr", "stderr", stderrStr)
	}

	return strings.TrimSpace(stdout.String()), nil
}
