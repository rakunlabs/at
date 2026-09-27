package server

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseDeveloperGitStatus(t *testing.T) {
	out := strings.Join([]string{
		"# branch.oid 1234",
		"# branch.head feature/x",
		"# branch.upstream origin/feature/x",
		"# branch.ab +2 -1",
		"1 M. N... 100644 100644 100644 aaa bbb staged only.go",
		"1 .M N... 100644 100644 100644 aaa bbb dir/with space.go",
		"1 MM N... 100644 100644 100644 aaa bbb both.go",
		"2 R. N... 100644 100644 100644 aaa bbb R100 new name.go",
		"old name.go",
		"u UU N... 100644 100644 100644 100644 aaa bbb ccc conflict.go",
		"? untracked file.txt",
		"",
	}, "\x00")
	got := parseDeveloperGitStatus(out)
	if got.Branch != "feature/x" || got.Upstream != "origin/feature/x" || got.Ahead != 2 || got.Behind != 1 {
		t.Fatalf("branch info: %+v", got)
	}
	want := []developerGitFile{
		{Path: "staged only.go", Index: "M", Worktree: "."},
		{Path: "both.go", Index: "M", Worktree: "M"},
		{Path: "new name.go", OrigPath: "old name.go", Index: "R", Worktree: "."},
	}
	if !reflect.DeepEqual(got.Staged, want) {
		t.Fatalf("staged = %+v", got.Staged)
	}
	if len(got.Unstaged) != 2 || got.Unstaged[0].Path != "dir/with space.go" || got.Unstaged[1].Path != "both.go" {
		t.Fatalf("unstaged = %+v", got.Unstaged)
	}
	if !reflect.DeepEqual(got.Untracked, []string{"untracked file.txt"}) || !reflect.DeepEqual(got.Conflicted, []string{"conflict.go"}) {
		t.Fatalf("untracked/conflicted = %+v %+v", got.Untracked, got.Conflicted)
	}
}

func TestParseDeveloperGitNumstat(t *testing.T) {
	out := strings.Join([]string{
		"3\t1\tdir/with space.go",
		"-\t-\timage.png",
		"0\t2\t",
		"old name.go",
		"new name.go",
		"",
	}, "\x00")
	want := []developerGitChange{
		{Path: "dir/with space.go", Additions: 3, Deletions: 1},
		{Path: "image.png", Binary: true},
		{Path: "new name.go", Deletions: 2},
	}
	if got := parseDeveloperGitNumstat(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("numstat = %+v", got)
	}
}
