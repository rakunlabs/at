//go:build linux

package workflow

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

type executionReadyWriter struct {
	once  sync.Once
	ready chan struct{}
}

func (w *executionReadyWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.ready) })
	return len(p), nil
}

func TestExecutionProcessRevocationAndCancellationKillsChildren(t *testing.T) {
	for _, reason := range []string{"cancel", "revocation"} {
		t.Run(reason, func(t *testing.T) {
			var revoked atomic.Bool
			ctx, err := service.BindExecution(t.Context(), service.ExecutionProvenance{RunID: "r", UserID: "u", WorkspaceID: "w", Source: "test"}, t.TempDir(), func(context.Context, service.ExecutionProvenance, service.ExecutionAction) (service.ExecutionValidation, error) {
				return service.ExecutionValidation{Allowed: !revoked.Load(), Policy: service.ExecutionPolicy{WorkspaceID: "w", Mode: service.ExecutionTrustedHost, GrantedBy: "platform"}}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			marker := filepath.Join(t.TempDir(), "escaped-child")
			cmd := exec.CommandContext(ctx, "bash", "-c", fmt.Sprintf("(sleep 1; touch %q) & echo ready; wait", marker))
			ready := &executionReadyWriter{ready: make(chan struct{})}
			cmd.Stdout = ready
			done := make(chan error, 1)
			go func() { done <- RunExecutionProcess(ctx, cmd) }()
			select {
			case <-ready.ready:
			case <-time.After(2 * time.Second):
				t.Fatal("process did not start")
			}
			if reason == "cancel" {
				cancel()
			} else {
				revoked.Store(true)
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("revoked process completed successfully")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("revoked process did not stop")
			}
			time.Sleep(1100 * time.Millisecond)
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("child survived authority cancellation")
			}
		})
	}
}
