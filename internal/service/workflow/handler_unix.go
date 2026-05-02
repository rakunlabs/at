//go:build !windows

package workflow

import (
	"log/slog"
	"os/exec"
	"syscall"
)

// setProcessGroup configures the command to start in its own process
// group so killProcessGroup can later terminate the entire subtree.
//
// Without Setpgid, exec.CommandContext's ctx-cancel behaviour only
// signals the immediate child (`bash`). When that bash script has
// spawned ffmpeg/python/curl, those grandchildren inherit init as
// their parent and keep running — pegging CPU long after the agentic
// task has been cancelled or timed out.
//
// Linux + Darwin both support this; on Windows we fall back to the
// default no-process-group behaviour (handler_windows.go).
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup sends SIGKILL to the entire process group of the
// running command. Safe to call multiple times and on a finished
// command (any error is logged at debug, not surfaced).
//
// We pass a negative pid to syscall.Kill which targets the process
// group whose pgid equals abs(pid). Since we set Setpgid above and
// did not specify Pgid, the new group's id equals the bash pid.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		// Common cases: ESRCH (already exited) — not worth surfacing.
		slog.Debug("handler: kill process group failed",
			"pid", pid,
			"error", err.Error())
	}
}
