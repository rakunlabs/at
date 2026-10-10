// Command at-sandbox is the static Kubernetes sandbox launcher, not a host tool.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/rakunlabs/at/internal/sandboxruntime"
)

func main() {
	if err := sandboxruntime.Run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.ExitCode())
		}
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
