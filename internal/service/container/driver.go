package container

import (
	"context"
	"io"
)

// Driver is the backend that actually runs sandboxes. The Manager owns
// everything that does not depend on where a sandbox runs — which scopes are
// live, activity and idle expiry, workspace quotas, the /workspace boundary —
// and delegates the rest here, so another backend (Kubernetes, Podman, a
// remote host) is a new Driver rather than a change to its callers.
//
// A handle is the driver's identifier for one running sandbox (a container
// ID for Docker). A scope is the caller's stable name for the environment; a
// driver derives any backend names from it deterministically, so persistent
// storage and leftovers from an earlier process are found again.
type Driver interface {
	// Name identifies the backend in logs and diagnostics.
	Name() string
	// Create starts the sandbox for scope and returns its handle. When a
	// sandbox for the scope already exists with the same configuration it is
	// resumed (keeping whatever was installed in it); otherwise it is replaced.
	Create(ctx context.Context, scope string, cfg Config) (string, error)
	// Running reports whether handle is still running.
	Running(ctx context.Context, handle string) bool
	// Exec runs one command to completion. A non-zero exit is reported as the
	// exit code with a nil error; the error is for failing to run it at all.
	Exec(ctx context.Context, handle string, req ExecRequest) (int, error)
	// Attach starts an interactive shell. Closing the Terminal ends only that
	// shell, never the sandbox.
	Attach(ctx context.Context, handle string, workDir string, cols, rows uint16) (Terminal, error)
	// Remove stops and deletes the sandbox; persistent storage survives.
	Remove(ctx context.Context, handle string) error
	// Stop halts the sandbox but keeps it, including changes made inside it,
	// so a later Create for the same scope and configuration resumes it.
	Stop(ctx context.Context, handle string) error
	// Purge deletes everything the driver keeps for scope, including
	// persistent storage. Missing resources are not an error.
	Purge(ctx context.Context, scope string) error
}

// ExecRequest describes one command. Argv is executed directly, never through
// a shell unless Argv itself names one.
type ExecRequest struct {
	Argv    []string
	Env     map[string]string
	WorkDir string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

// Terminal is an interactive shell attachment: a byte stream plus resizing.
// It is an interface rather than a PTY file because not every backend has
// one — a Kubernetes exec stream carries resize events over the connection.
type Terminal interface {
	io.ReadWriteCloser
	Resize(cols, rows uint16) error
}
