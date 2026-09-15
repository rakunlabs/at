package service

import "testing"

// requireExecutionFileAccess skips when the platform cannot perform
// symlink-free workspace file access. The implementation uses Linux openat2
// and deliberately fails closed elsewhere rather than degrading to unchecked
// os calls, so these tests can only assert real behaviour on Linux.
func requireExecutionFileAccess(t testing.TB) {
	t.Helper()
	if !ExecutionFileAccessSupported() {
		t.Skip("symlink-free workspace file access requires Linux openat2")
	}
}
