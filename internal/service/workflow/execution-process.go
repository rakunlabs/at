package workflow

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// RunExecutionProcess fences host execution and reaps the entire process group
// on cancellation or a live authority revocation. It is not a sandbox runner.
func RunExecutionProcess(ctx context.Context, cmd *exec.Cmd) error {
	action := service.ExecutionAction{Kind: "handler", Name: "bash"}
	if err := service.CheckExecution(ctx, action); err != nil {
		return err
	}
	setProcessGroup(cmd)
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start execution process: %w", err)
	}
	done, stopped := make(chan struct{}), make(chan struct{})
	var authorityErr error
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				authorityErr = ctx.Err()
				killProcessGroup(cmd)
				return
			case <-ticker.C:
				if err := service.CheckExecution(ctx, action); err != nil {
					authorityErr = err
					killProcessGroup(cmd)
					return
				}
			}
		}
	}()
	err := cmd.Wait()
	close(done)
	<-stopped
	if authorityErr != nil {
		return fmt.Errorf("execution process authority revoked: %w", authorityErr)
	}
	return err
}
