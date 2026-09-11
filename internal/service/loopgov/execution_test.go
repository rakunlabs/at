package loopgov

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service/executiontest"
)

func TestScopedToolResultsKeepWorkspaceRoots(t *testing.T) {
	global := t.TempDir()
	g := New(Config{WorkspaceRoot: global, ToolResultMaxBytes: 16}, nil)
	for _, text := range []string{"workspace-one", "workspace-two"} {
		root := t.TempDir()
		truncate := g.ScopedToolResults(executiontest.WithRoot(t, root))
		body := strings.Repeat(text, 10)
		kept, truncated := truncate("same-task", "same-tool", body)
		if !truncated || !strings.Contains(kept, "full output: .at-tool-output/") {
			t.Fatalf("bad marker: %s", kept)
		}
		files, err := filepath.Glob(filepath.Join(root, ".at-tool-output", "*", "*.txt"))
		if err != nil || len(files) != 1 {
			t.Fatalf("scoped dump files: %v %v", files, err)
		}
		got, err := os.ReadFile(files[0])
		if err != nil || string(got) != body {
			t.Fatal("dump lost original payload")
		}
	}
	if _, err := os.Stat(filepath.Join(global, ".at-tool-output")); !os.IsNotExist(err) {
		t.Fatal("scoped run wrote into shared dump root")
	}
	if g.Config().WorkspaceRoot != global {
		t.Fatal("scoped truncation mutated global governor")
	}
}
