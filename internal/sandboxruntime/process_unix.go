//go:build !windows

package sandboxruntime

import (
	"os"
	"os/signal"
	"syscall"
)

func replaceProcess(file string, argv, env []string) error {
	return syscall.Exec(file, argv, env)
}

func idle() error {
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM, syscall.SIGCHLD)
	defer signal.Stop(done)
	for sig := range done {
		if sig != syscall.SIGCHLD {
			return nil
		}
		// PID 1 adopts orphaned grandchildren after command cancellation.
		for {
			pid, err := syscall.Wait4(-1, nil, syscall.WNOHANG, nil)
			if err != nil || pid <= 0 {
				break
			}
		}
	}
	return nil
}
