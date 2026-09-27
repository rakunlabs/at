// Command at-devfs is the file helper AT copies into developer-space
// containers. It is built for linux/amd64 and linux/arm64 and embedded into
// the AT binary (see internal/devfs/devfsbin); it is not run on the AT host.
package main

import (
	"os"

	"github.com/rakunlabs/at/internal/devfs"
)

func main() {
	os.Exit(devfs.Run(devfs.Workspace, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
