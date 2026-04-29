package loopgov

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTruncateUnderCap(t *testing.T) {
	g := New(Config{ToolResultMaxBytes: 1024}, nil)
	body := strings.Repeat("a", 500)
	got, did := g.TruncateToolResult("run-1", "bash_execute", body)
	if did {
		t.Fatal("should not truncate when under cap")
	}
	if got != body {
		t.Fatal("body should be returned unchanged")
	}
}

func TestTruncateOverCapEmitsMarker(t *testing.T) {
	tmp := t.TempDir()
	g := New(Config{
		ToolResultMaxBytes: 100,
		WorkspaceRoot:      tmp,
		ToolCapOverrides:   map[string]int{"bash_execute": 100},
	}, nil)
	body := strings.Repeat("a", 1000)
	got, did := g.TruncateToolResult("run-XYZ", "bash_execute", body)
	if !did {
		t.Fatal("should truncate when over cap")
	}
	if !strings.Contains(got, "[truncated:") {
		t.Fatalf("marker missing: %s", got[len(got)-200:])
	}
	if !strings.Contains(got, "of 1000 bytes shown") {
		t.Fatal("marker should include the original byte count")
	}
	if !strings.Contains(got, ".at-tool-output/run-XYZ/bash_execute-1.txt") {
		t.Fatalf("marker should reference the dump file path: %s", got)
	}
	// Verify the dump file exists with the full content.
	full := filepath.Join(tmp, ".at-tool-output", "run-XYZ", "bash_execute-1.txt")
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("dump file missing: %v", err)
	}
	if string(data) != body {
		t.Fatal("dump file content does not match original")
	}
}

func TestTruncateRespectsUTF8Boundary(t *testing.T) {
	tmp := t.TempDir()
	// Cap at 100 — but place a multi-byte rune so we'd split it if naive.
	// "a"*98 + "ñ" (2 bytes) + "a"*100 — cap would otherwise fall on the
	// second byte of "ñ" which is invalid UTF-8.
	body := strings.Repeat("a", 98) + "ñ" + strings.Repeat("a", 100)
	g := New(Config{
		ToolResultMaxBytes: 99,
		WorkspaceRoot:      tmp,
		ToolCapOverrides:   map[string]int{"bash_execute": 99},
	}, nil)
	got, did := g.TruncateToolResult("r", "bash_execute", body)
	if !did {
		t.Fatal("should truncate")
	}
	// The kept portion (before the marker) must be valid UTF-8 — i.e.
	// not include the leading byte of "ñ" without its continuation byte.
	end := strings.Index(got, "\n\n[truncated:")
	if end < 0 {
		t.Fatalf("marker missing: %s", got)
	}
	head := got[:end]
	if strings.HasSuffix(head, "\xc3") {
		t.Fatal("kept portion ends mid-rune")
	}
}

func TestTruncatePerToolOverride(t *testing.T) {
	tmp := t.TempDir()
	g := New(Config{
		ToolResultMaxBytes: 100,
		WorkspaceRoot:      tmp,
		ToolCapOverrides:   map[string]int{"task_get": 100_000},
	}, nil)
	body := strings.Repeat("b", 5_000)
	_, did := g.TruncateToolResult("r", "task_get", body)
	if did {
		t.Fatal("override should keep body under cap")
	}
}

func TestTruncateClassDefaultStructured(t *testing.T) {
	tmp := t.TempDir()
	g := New(Config{
		ToolResultMaxBytes: 100, // would normally truncate
		WorkspaceRoot:      tmp,
	}, nil)
	// task_list classifies as "structured" (32 KB default cap). 5000
	// bytes should pass through.
	body := strings.Repeat("c", 5_000)
	_, did := g.TruncateToolResult("r", "task_list", body)
	if did {
		t.Fatal("structured class default should keep this body")
	}
}

func TestTruncateWorkspaceUnavailable(t *testing.T) {
	g := New(Config{
		ToolResultMaxBytes: 50,
		ToolCapOverrides:   map[string]int{"bash_execute": 50},
	}, nil) // no WorkspaceRoot
	body := strings.Repeat("d", 1000)
	got, did := g.TruncateToolResult("r", "bash_execute", body)
	if !did {
		t.Fatal("should still truncate even with no workspace")
	}
	if !strings.Contains(got, "full output unavailable") {
		t.Fatalf("marker should signal the dump failure: %s", got)
	}
}

func TestTruncateMonotonicSeq(t *testing.T) {
	tmp := t.TempDir()
	g := New(Config{
		ToolResultMaxBytes: 50,
		WorkspaceRoot:      tmp,
		ToolCapOverrides:   map[string]int{"bash_execute": 50},
	}, nil)
	body := strings.Repeat("e", 1000)
	out1, _ := g.TruncateToolResult("R", "bash_execute", body)
	out2, _ := g.TruncateToolResult("R", "bash_execute", body)
	if !strings.Contains(out1, "bash_execute-1.txt") {
		t.Fatalf("first dump should be -1: %s", out1)
	}
	if !strings.Contains(out2, "bash_execute-2.txt") {
		t.Fatalf("second dump should be -2: %s", out2)
	}
}

func TestTruncateDisabledIsPassThrough(t *testing.T) {
	g := New(Config{Disabled: true, ToolResultMaxBytes: 10}, nil)
	body := strings.Repeat("f", 1000)
	got, did := g.TruncateToolResult("r", "bash_execute", body)
	if did || got != body {
		t.Fatal("disabled mode should pass through unchanged")
	}
}

func TestSanitizeForFilename(t *testing.T) {
	cases := map[string]string{
		"":                 "tool",
		"plain":            "plain",
		"with/slash":       "with_slash",
		"with.dot":         "with_dot",
		"weird:name space": "weird_name_space",
	}
	for in, want := range cases {
		if got := sanitizeForFilename(in); got != want {
			t.Errorf("sanitizeForFilename(%q): got %q want %q", in, got, want)
		}
	}
}
