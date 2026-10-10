package container

import (
	"context"
	"io"
	"io/fs"
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
	// reused; otherwise it is replaced. Root-filesystem preservation across
	// Stop depends on RuntimeCapabilities, not the stable scope name.
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
	// Stop halts the sandbox while preserving its persistent storage. Whether
	// the writable root filesystem survives is declared by RuntimeCapabilities.
	Stop(ctx context.Context, handle string) error
	// Purge deletes everything the driver keeps for scope, including
	// persistent storage. Missing resources are not an error.
	Purge(ctx context.Context, scope string) error
}

// RuntimeCapabilities describes guarantees rather than inferring them from a
// backend name. Unknown drivers make no guarantees.
type RuntimeCapabilities struct {
	Backend             string `json:"backend"`
	PreservesRootOnStop bool   `json:"preserves_root_on_stop"`
	PersistentHome      bool   `json:"persistent_home"`
	MultiReplica        bool   `json:"multi_replica"`
	FileHelperPath      string `json:"file_helper_path,omitempty"`
	Notice              string `json:"notice,omitempty"`
}

type CapabilityProvider interface {
	Capabilities() RuntimeCapabilities
}

// DriverCloser releases backend control resources after sandbox shutdown.
// It never deletes persistent storage.
type DriverCloser interface{ Close() error }

// ShutdownDriver drains remote work and stops workloads before relinquishing
// exclusive backend ownership. Persistent volumes must not be removed.
type ShutdownDriver interface{ Shutdown(context.Context) error }

// ScopeStopper stops a persistent workload even when this process has never
// tracked its handle. Empty local memory is not proof that a workload stopped.
type ScopeStopper interface {
	StopScope(context.Context, string) error
}

func (m *Manager) Close() error {
	if closer, ok := m.driver.(DriverCloser); ok {
		return closer.Close()
	}
	return nil
}

func (m *Manager) Capabilities() RuntimeCapabilities {
	if driver, ok := m.driver.(CapabilityProvider); ok {
		return driver.Capabilities()
	}
	return RuntimeCapabilities{Backend: m.driver.Name()}
}

// FileInstaller is implemented by drivers that can place a file into a
// sandbox without running anything inside it, so a helper can be installed
// into an image that has no shell. It is optional: callers fall back when a
// driver does not provide it.
type FileInstaller interface {
	// Platform reports the sandbox's platform as "os/arch" (e.g.
	// "linux/arm64"), optionally followed by "/variant".
	Platform(ctx context.Context, handle string) (string, error)
	// InstallFile writes data to the absolute path inside the sandbox,
	// creating missing parent directories and replacing an existing file.
	InstallFile(ctx context.Context, handle, path string, data []byte, mode fs.FileMode) error
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
