// Package devfsbin carries the prebuilt at-devfs helpers. `make build-devfs`
// (run by `make build`, `make run` and the goreleaser hooks) writes them to
// bin/ before AT is compiled. A binary built without them still works: the
// developer-space file API falls back to the python3 script.
package devfsbin

import (
	"embed"
	"strings"
)

//go:embed all:bin
var helpers embed.FS

// Available reports whether any helper was embedded.
func Available() bool {
	entries, _ := helpers.ReadDir("bin")
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "at-devfs-") {
			return true
		}
	}
	return false
}

// For returns the helper for a container platform such as "linux/amd64", or
// nil when none was embedded for it.
func For(platform string) []byte {
	platform = strings.ToLower(strings.TrimSpace(platform))
	osName, arch, ok := strings.Cut(platform, "/")
	if !ok {
		return nil
	}
	arch, _, _ = strings.Cut(arch, "/") // drop a variant such as arm64/v8
	switch arch {
	case "x86_64":
		arch = "amd64"
	case "aarch64":
		arch = "arm64"
	}
	data, err := helpers.ReadFile("bin/at-devfs-" + osName + "-" + arch)
	if err != nil {
		return nil
	}
	return data
}
