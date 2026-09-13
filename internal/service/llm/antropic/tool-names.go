package antropic

import (
	"crypto/sha256"
	"fmt"

	"github.com/rakunlabs/at/internal/service"
)

// OAuth's PascalCase conversion is not invertible (Read and read collide).
// Resolve names from the request's definitions, never by lowercasing replies.
func oauthToolNames(names []string) map[string]string {
	counts := make(map[string]int)
	for _, name := range names {
		counts[prefixToolName(name)]++
	}
	out := make(map[string]string, len(names))
	for _, name := range names {
		wire := prefixToolName(name)
		if counts[wire] > 1 {
			sum := sha256.Sum256([]byte(name))
			wire = fmt.Sprintf("mcp_Tool_%x", sum[:16])
		}
		out[name] = wire
	}
	return out
}

func restoreOAuthToolName(wire string, tools []service.Tool) string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	for original, encoded := range oauthToolNames(names) {
		if encoded == wire {
			return original
		}
	}
	return unprefixToolName(wire)
}
