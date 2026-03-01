package nodes

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rytsh/mugo/templatex"

	"github.com/rakunlabs/at/internal/render"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// execNode executes shell commands in a sandboxed working directory.
//
// The node runs commands via /bin/sh -c, restricting execution to a
// configurable sandbox root directory. No path traversal outside the
// sandbox is allowed.
//
// Config (node.Data):
//
//	"command":      string  — shell command to execute (required; supports Go template syntax from inputs)
//	"working_dir":  string  — subdirectory within sandbox (default: sandbox root)
//	"timeout":      float64 — execution timeout in seconds (default: 60, max: 600)
//	"sandbox_root": string  — root directory for sandboxed execution (default: /tmp/at-sandbox)
//	"env":          map     — extra environment variables (merged with minimal defaults)
//	"input_count":  float64 — number of input ports (default 1, max 10)
//
// Input ports: "data" (or "data1".."dataN" when input_count > 1)
//
// The command string supports Go template syntax: {{.variable}} references
// are resolved from the input data.
//
// Output ports: index 0 = "false" (non-zero exit), index 1 = "true" (exit 0), index 2 = "always"
//
// Output data:
//
//	"stdout":    string — standard output
//	"stderr":    string — standard error
//	"exit_code": int    — process exit code (0 = success)
//	"result"     string — same as stdout (for convenience)
//
// Returns NodeResultSelection.
type execNode struct {
	command     string
	workingDir  string
	timeout     time.Duration
	sandboxRoot string
	env         map[string]string
	inputCount  int
}

// defaultSandboxRoot is the default root directory for exec sandboxing.
const defaultSandboxRoot = "/tmp/at-sandbox"

// defaultExecTimeout is the default command execution timeout.
const defaultExecTimeout = 60 * time.Second

// maxExecTimeout is the maximum allowed timeout.
const maxExecTimeout = 600 * time.Second

func init() {
	workflow.RegisterNodeType("exec", newExecNode)
}

func newExecNode(node service.WorkflowNode) (workflow.Noder, error) {
	command, _ := node.Data["command"].(string)

	workingDir, _ := node.Data["working_dir"].(string)

	sandboxRoot, _ := node.Data["sandbox_root"].(string)
	if sandboxRoot == "" {
		sandboxRoot = defaultSandboxRoot
	}

	timeout := defaultExecTimeout
	if t, ok := node.Data["timeout"].(float64); ok && t > 0 {
		timeout = time.Duration(t) * time.Second
		if timeout > maxExecTimeout {
			timeout = maxExecTimeout
		}
	}

	env := make(map[string]string)
	if envMap, ok := node.Data["env"].(map[string]any); ok {
		for k, v := range envMap {
			env[k] = fmt.Sprintf("%v", v)
		}
	}

	inputCount := 1
	if c, ok := node.Data["input_count"].(float64); ok && c >= 1 {
		inputCount = int(c)
		if inputCount > 10 {
			inputCount = 10
		}
	}

	return &execNode{
		command:     command,
		workingDir:  workingDir,
		timeout:     timeout,
		sandboxRoot: sandboxRoot,
		env:         env,
		inputCount:  inputCount,
	}, nil
}

func (n *execNode) Type() string { return "exec" }

func (n *execNode) Validate(_ context.Context, _ *workflow.Registry) error {
	if n.command == "" {
		return fmt.Errorf("exec: 'command' is required")
	}
	return nil
}

func (n *execNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	// Resolve the sandbox root to an absolute path.
	sandboxAbs, err := filepath.Abs(n.sandboxRoot)
	if err != nil {
		return nil, fmt.Errorf("exec: resolve sandbox root: %w", err)
	}

	// Ensure the sandbox directory exists.
	if err := os.MkdirAll(sandboxAbs, 0o755); err != nil {
		return nil, fmt.Errorf("exec: create sandbox dir: %w", err)
	}

	// Determine the working directory within the sandbox.
	workDir := sandboxAbs
	if n.workingDir != "" {
		// Resolve template references in working_dir.
		resolvedDir := resolveTemplate(n.workingDir, inputs, varFuncMap(reg))
		workDir = filepath.Join(sandboxAbs, resolvedDir)
	}

	// Also allow working_dir to come from inputs (edge data).
	if inputDir, ok := inputs["working_dir"].(string); ok && inputDir != "" {
		workDir = filepath.Join(sandboxAbs, inputDir)
	}

	// Validate the working directory is within the sandbox.
	workDirAbs, err := filepath.Abs(workDir)
	if err != nil {
		return nil, fmt.Errorf("exec: resolve working dir: %w", err)
	}

	if !isInsideSandbox(workDirAbs, sandboxAbs) {
		return nil, fmt.Errorf("exec: working directory %q escapes sandbox %q", workDirAbs, sandboxAbs)
	}

	// Create the working directory if needed.
	if err := os.MkdirAll(workDirAbs, 0o755); err != nil {
		return nil, fmt.Errorf("exec: create working dir: %w", err)
	}

	// Resolve template references in the command.
	command := resolveTemplate(n.command, inputs, varFuncMap(reg))

	// Also allow command to come from inputs (edge data), which overrides the static config.
	if inputCmd, ok := inputs["command"].(string); ok && inputCmd != "" {
		command = inputCmd
	}

	if command == "" {
		return nil, fmt.Errorf("exec: command is empty after template resolution")
	}

	// Create context with timeout.
	execCtx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()

	// Build the command.
	cmd := exec.CommandContext(execCtx, "/bin/sh", "-c", command)
	cmd.Dir = workDirAbs

	// Build environment: start with minimal defaults, add configured env,
	// then add any env from inputs.
	cmdEnv := []string{
		"HOME=" + sandboxAbs,
		"PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
		"TMPDIR=" + sandboxAbs,
		"SANDBOX_ROOT=" + sandboxAbs,
	}
	for k, v := range n.env {
		cmdEnv = append(cmdEnv, k+"="+v)
	}
	if inputEnv, ok := inputs["env"].(map[string]any); ok {
		for k, v := range inputEnv {
			cmdEnv = append(cmdEnv, fmt.Sprintf("%s=%v", k, v))
		}
	}
	cmd.Env = cmdEnv

	// Capture stdout and stderr.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Execute.
	runErr := cmd.Run()

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			// Command failed to start or was killed.
			return nil, fmt.Errorf("exec: %w", runErr)
		}
	}

	outData := make(map[string]any, len(inputs)+4)
	for k, v := range inputs {
		outData[k] = v
	}
	outData["stdout"] = stdout.String()
	outData["stderr"] = stderr.String()
	outData["exit_code"] = exitCode
	outData["result"] = stdout.String()

	// Port selection based on exit code.
	selection := []string{"always"}
	if exitCode == 0 {
		selection = append(selection, "true")
	} else {
		selection = append(selection, "false")
	}

	return workflow.NewSelectionResult(outData, selection), nil
}

// isInsideSandbox checks that dir is inside (or equal to) the sandbox root.
// Both paths must be absolute.
func isInsideSandbox(dir, sandbox string) bool {
	// Ensure both end cleanly.
	dir = filepath.Clean(dir)
	sandbox = filepath.Clean(sandbox)

	// dir must start with sandbox path + separator (or be exactly sandbox).
	if dir == sandbox {
		return true
	}
	return strings.HasPrefix(dir, sandbox+string(filepath.Separator))
}

// resolveTemplate renders a Go text/template string with the given data.
func resolveTemplate(s string, data map[string]any, funcs map[string]any) string {
	result, err := render.ExecuteWithData(s, data, templatex.WithExecFuncMap(funcs))
	if err != nil {
		// Fall back to original string on template errors to preserve
		// backwards compatibility with the previous simple replacement.
		return s
	}

	return string(result)
}
