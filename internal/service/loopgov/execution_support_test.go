package loopgov

import (
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// requireExecutionFileAccess skips when the platform cannot perform
// symlink-free workspace file access, which the tool-output dump path needs.
func requireExecutionFileAccess(t testing.TB) {
	t.Helper()
	if !service.ExecutionFileAccessSupported() {
		t.Skip("symlink-free workspace file access requires Linux openat2")
	}
}
