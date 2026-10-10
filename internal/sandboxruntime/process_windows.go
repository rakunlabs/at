package sandboxruntime

import "fmt"

func replaceProcess(string, []string, []string) error {
	return fmt.Errorf("the sandbox launcher requires a Unix environment")
}

func idle() error { return fmt.Errorf("the sandbox launcher requires a Unix environment") }

func supervise(string, string, []string, []string) error {
	return fmt.Errorf("the sandbox supervisor requires Linux")
}
func cancelRun(string) error { return fmt.Errorf("the sandbox supervisor requires Linux") }
