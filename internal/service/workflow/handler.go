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
//
// JSHandlerOptions holds optional lookups for JS handler execution.
type JSHandlerOptions struct {
	VarLookup      VarLookup
	UserPrefLookup UserPrefLookup
}

func ExecuteJSHandler(handler string, args map[string]any, varLookup ...VarLookup) (string, error) {
	return ExecuteJSHandlerWithOptions(handler, args, JSHandlerOptions{
		VarLookup: firstVarLookup(varLookup),
	})
}

// ExecuteJSHandlerWithOptions is like ExecuteJSHandler but accepts all optional lookups.
func ExecuteJSHandlerWithOptions(handler string, args map[string]any, opts JSHandlerOptions) (string, error) {
	vm := goja.New()

	// Register all shared helpers (toString, jsonParse, btoa, atob,
	// JSON_stringify, httpGet, httpPost, httpPut, httpDelete, getVar, getUserPref).
	if err := SetupGojaVM(vm, map[string]any{"args": args}, opts.VarLookup); err != nil {
		return "", fmt.Errorf("js handler: setup VM: %w", err)
	}

	// Register getUserPref if a lookup function was provided.
	if opts.UserPrefLookup != nil {
		if err := registerUserPrefHelper(vm, opts.UserPrefLookup); err != nil {
			return "", fmt.Errorf("js handler: register getUserPref: %w", err)
		}
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

// ctxKeyContainerScope is a context key for routing execution to a Docker container.
type ctxKeyContainerScope struct{}

// ContainerScope identifies which container to use for command execution.
type ContainerScope struct {
	OrgID  string // organization ID (for per-org containers)
	UserID string // user ID (for per-user containers, e.g., telegram chat_id)
}

// ContextWithContainerScope returns a context with a container scope.
func ContextWithContainerScope(ctx context.Context, scope ContainerScope) context.Context {
	return context.WithValue(ctx, ctxKeyContainerScope{}, scope)
}

// ContainerScopeFromContext returns the container scope from context, if set.
func ContainerScopeFromContext(ctx context.Context) (ContainerScope, bool) {
	v, ok := ctx.Value(ctxKeyContainerScope{}).(ContainerScope)
	return v, ok
}

// ctxKeyWorkDir is a context key for injecting a working directory into bash handlers.
// When set, ExecuteBashHandler uses it as cmd.Dir so all commands run in that directory.
type ctxKeyWorkDir struct{}

// ContextWithWorkDir returns a new context with the given working directory.
// This is used by the delegation engine to give all agents in a chain the same workspace.
func ContextWithWorkDir(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, ctxKeyWorkDir{}, dir)
}

// WorkDirFromContext returns the working directory from context, if set.
func WorkDirFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyWorkDir{}).(string); ok {
		return v
	}
	return ""
}

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

	// Allow pip/uv to install packages on externally managed Python (macOS Homebrew).
	env = append(env, "PIP_BREAK_SYSTEM_PACKAGES=1", "UV_SYSTEM_PYTHON=1")

	// Overlay tool arguments as ARG_<NAME>.
	// Strings are passed as-is; all other types (arrays, objects, numbers,
	// booleans) are JSON-encoded so that bash scripts can parse them with
	// standard JSON tools (e.g., python3 -c "import json; ...").
	for k, v := range args {
		envKey := "ARG_" + strings.ToUpper(
			strings.NewReplacer(".", "_", "-", "_").Replace(k),
		)
		var envVal string
		switch tv := v.(type) {
		case string:
			envVal = tv
		default:
			if b, err := json.Marshal(tv); err == nil {
				envVal = string(b)
			} else {
				envVal = fmt.Sprintf("%v", tv)
			}
		}
		env = append(env, envKey+"="+envVal)
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

	// Set working directory from context if available (shared workspace for delegation chains).
	// Also inject AT_WORK_DIR env var so scripts can reference the shared workspace explicitly.
	//
	// Just-in-time create the directory: org-delegation calls MkdirAll optimistically at the
	// start of a delegation chain but treats the failure as non-fatal, and child delegations
	// reuse the parent workdir from context without re-validating it. If we hand a missing
	// directory to exec.Cmd, Go's os/exec aborts process start with `chdir ...: no such file
	// or directory` BEFORE the command runs, which the LLM sees as a recoverable tool error
	// and retries until it burns the entire iteration budget. Idempotent MkdirAll here is
	// cheap and prevents that runaway loop.
	if workDir := WorkDirFromContext(ctx); workDir != "" {
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			logi.Ctx(ctx).Warn("bash handler: failed to ensure work_dir exists, falling back to inherited cwd",
				"work_dir", workDir, "error", err.Error())
			// Don't set cmd.Dir to a guaranteed-broken path; let the command run from
			// the inherited cwd and surface a real error if the script needs the workdir.
		} else {
			cmd.Dir = workDir
			env = append(env, "AT_WORK_DIR="+workDir)
		}
	}

	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	logi.Ctx(ctx).Debug("bash handler: executing", "handler_length", len(handler), "arg_count", len(args), "work_dir", cmd.Dir)

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

// firstVarLookup returns the first VarLookup from a variadic slice, or nil.
func firstVarLookup(lookups []VarLookup) VarLookup {
	if len(lookups) > 0 {
		return lookups[0]
	}
	return nil
}

// registerUserPrefHelper registers the getUserPref(key) function on a Goja VM.
func registerUserPrefHelper(vm *goja.Runtime, lookup UserPrefLookup) error {
	return vm.Set("getUserPref", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(vm.NewTypeError("getUserPref: key is required"))
		}
		key := call.Arguments[0].String()
		val, err := lookup(key)
		if err != nil {
			panic(vm.NewTypeError(fmt.Sprintf("getUserPref: %v", err)))
		}
		return vm.ToValue(val)
	})
}
