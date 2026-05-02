//go:build windows

package workflow

import "os/exec"

// setProcessGroup is a no-op on Windows; there is no portable
// equivalent of POSIX process groups, so the kill-on-cancel path
// degrades to "kill bash; orphan grandchildren survive". Production
// AT runs on Linux (Dockerfile.agent-runtime); this exists only so
// `go build` succeeds on a Windows developer machine.
func setProcessGroup(_ *exec.Cmd) {}

// killProcessGroup is a no-op on Windows for the same reason.
func killProcessGroup(_ *exec.Cmd) {}
