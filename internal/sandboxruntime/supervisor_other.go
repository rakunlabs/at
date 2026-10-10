//go:build !linux && !windows

package sandboxruntime

import "fmt"

func supervise(string, string, []string, []string) error {
	return fmt.Errorf("the sandbox supervisor requires Linux")
}
func cancelRun(string) error { return fmt.Errorf("the sandbox supervisor requires Linux") }
