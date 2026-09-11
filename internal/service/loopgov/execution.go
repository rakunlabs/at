package loopgov

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

// ScopedToolResults keeps the same single global byte cap, but writes results
// beneath one execution workspace. It never mutates the shared governor's root.
func (g *Governor) ScopedToolResults(ctx context.Context) func(string, string, string) (string, bool) {
	p, root, ok := service.ExecutionFromContext(ctx)
	if !ok {
		return func(_, _, _ string) (string, bool) { return "execution authority denied", true }
	}
	cfg := g.cfg
	cfg.WorkspaceRoot = root
	scoped := New(cfg, g.summarizer)
	run := p.RunID + "-" + ulid.Make().String()
	return func(_ string, tool, body string) (string, bool) { return scoped.TruncateToolResult(run, tool, body) }
}

func (g *Governor) dumpToolOutputRooted(runID, toolName, body string) (string, error) {
	if g.cfg.WorkspaceRoot == "" || !filepath.IsLocal(runID) {
		return "", fmt.Errorf("invalid workspace root or run ID")
	}
	if err := os.MkdirAll(g.cfg.WorkspaceRoot, 0700); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(g.cfg.WorkspaceRoot)
	if err != nil {
		return "", err
	}
	defer root.Close()
	dir := filepath.Join(".at-tool-output", runID)
	if err := service.MkdirExecutionAll(root, dir, 0700); err != nil {
		return "", err
	}
	name := filepath.Join(dir, fmt.Sprintf("%s-%d.txt", sanitizeForFilename(toolName), g.nextDumpSeq(runID)))
	if _, err := service.ReplaceExecutionFile(root, name, strings.NewReader(body)); err != nil {
		return "", err
	}
	return name, nil
}
