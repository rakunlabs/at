package terminal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// Serialize lifecycle operations across AT processes sharing this host. Files
// remain until reboot: unlinking a lock file would race existing waiters.
func lockSession(ctx context.Context, id string) (func(), error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	const root = "/run/at-terminal-locks"
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, fmt.Errorf("create terminal lock directory: %w", err)
	}
	f, err := os.OpenFile(root+"/"+id, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open terminal lock: %w", err)
	}
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() }, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			f.Close()
			return nil, fmt.Errorf("lock terminal: %w", err)
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}
