package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service/workflow"
)

// TestMaybeSpillBriefSmall keeps short briefs inline.
func TestMaybeSpillBriefSmall(t *testing.T) {
	s := &Server{}
	ctx := context.Background()
	short := strings.Repeat("a", 800)
	got, spilled := s.maybeSpillBrief(ctx, short, "", "Test Title")
	if spilled {
		t.Fatalf("short brief should not be spilled")
	}
	if got != short {
		t.Fatalf("short brief should be returned unchanged")
	}
}

// TestMaybeSpillBriefLarge spills large briefs to the workspace and
// replaces the description with a reference pointing at the file.
func TestMaybeSpillBriefLarge(t *testing.T) {
	tmp := t.TempDir()
	ctx := workflow.ContextWithWorkDir(context.Background(), tmp)

	s := &Server{}
	long := strings.Repeat("the brief body. ", 200) // ~3.2 KB
	got, spilled := s.maybeSpillBrief(ctx, long, "", "Top 5 Animals With Crazy Births")
	if !spilled {
		t.Fatalf("long brief (%d bytes) should be spilled (threshold=%d)", len(long), briefSpillThresholdBytes)
	}
	if !strings.Contains(got, "shared workspace") {
		t.Fatalf("returned reference should mention shared workspace, got: %s", got)
	}
	if !strings.Contains(got, "cat ") {
		t.Fatalf("returned reference should include a cat command for the agent")
	}
	if len(got) >= len(long) {
		t.Fatalf("reference (%d bytes) should be smaller than original brief (%d bytes)", len(got), len(long))
	}

	// File should exist in the brief subdir.
	briefDir := filepath.Join(tmp, briefSpillSubdir)
	entries, err := os.ReadDir(briefDir)
	if err != nil {
		t.Fatalf("brief dir not created: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 brief file, got %d", len(entries))
	}
	// Filename should be a slugified version of the title.
	name := entries[0].Name()
	if !strings.HasPrefix(name, "top-5-animals-with-crazy-births-") {
		t.Fatalf("brief filename should start with slugified title; got %q", name)
	}
	if !strings.HasSuffix(name, ".md") {
		t.Fatalf("brief filename should end .md; got %q", name)
	}

	// Content of the spilled file should match the original brief.
	content, err := os.ReadFile(filepath.Join(briefDir, name))
	if err != nil {
		t.Fatalf("read brief: %v", err)
	}
	if string(content) != long {
		t.Fatalf("spilled brief content does not match original")
	}
}

// TestMaybeSpillBriefIdempotent writes the same content twice and asserts
// only one file results (hash-based dedup).
func TestMaybeSpillBriefIdempotent(t *testing.T) {
	tmp := t.TempDir()
	ctx := workflow.ContextWithWorkDir(context.Background(), tmp)
	s := &Server{}
	long := strings.Repeat("identical. ", 200)

	_, _ = s.maybeSpillBrief(ctx, long, "", "Same Title")
	_, _ = s.maybeSpillBrief(ctx, long, "", "Same Title")

	briefDir := filepath.Join(tmp, briefSpillSubdir)
	entries, err := os.ReadDir(briefDir)
	if err != nil {
		t.Fatalf("brief dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file (dedup by content hash), got %d", len(entries))
	}
}

// TestMaybeSpillBriefNoWorkspace falls back to inline when no workspace
// can be resolved (no ctx workdir, no parent_id, no current task).
func TestMaybeSpillBriefNoWorkspace(t *testing.T) {
	s := &Server{}
	long := strings.Repeat("x", 5000)
	got, spilled := s.maybeSpillBrief(context.Background(), long, "", "Title")
	if spilled {
		t.Fatalf("should not spill when no workspace anchor exists")
	}
	if got != long {
		t.Fatalf("inline fallback should return original brief")
	}
}

func TestSlugifyForBrief(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"Top 5 Animals With Crazy Births 🐍🐨", "top-5-animals-with-crazy-births"},
		{"   leading and trailing   ", "leading-and-trailing"},
		{"all_underscores_become_dashes", "all-underscores-become-dashes"},
		{"super-long-title-that-goes-on-and-on-and-on-forever-and-ever", "super-long-title-that-goes-on-and-on-and"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := slugifyForBrief(tt.in)
			if got != tt.want {
				t.Errorf("slugifyForBrief(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
