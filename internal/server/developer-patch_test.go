package server

import (
	"strings"
	"testing"
)

func TestParsePatch(t *testing.T) {
	ops, err := parsePatch(`*** Begin Patch
*** Add File: docs/new.md
+# Title
+
+body
*** Update File: main.go
*** Move to: cmd/main.go
@@ func main() {
-	println("old")
+	println("new")
*** Delete File: old.txt
*** End Patch
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 3 {
		t.Fatalf("ops = %d", len(ops))
	}
	if ops[0].kind != patchAdd || ops[0].path != "docs/new.md" || ops[0].content != "# Title\n\nbody\n" {
		t.Fatalf("add = %+v", ops[0])
	}
	if ops[1].kind != patchUpdate || ops[1].moveTo != "cmd/main.go" || len(ops[1].hunks) != 1 || ops[1].hunks[0].anchor != "func main() {" {
		t.Fatalf("update = %+v", ops[1])
	}
	if ops[2].kind != patchDelete || ops[2].path != "old.txt" {
		t.Fatalf("delete = %+v", ops[2])
	}

	for _, bad := range []string{
		"no header",
		"*** Begin Patch\n*** Update File: a\n*** End Patch",
		"*** Begin Patch\n*** Add File: a\nnot plus\n*** End Patch",
		"*** Begin Patch\n*** Frobnicate: a\n*** End Patch",
		"*** Begin Patch\n*** End Patch",
	} {
		if _, err := parsePatch(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestApplyPatchHunks(t *testing.T) {
	content := "package main\n\nfunc a() {\n\treturn 1\n}\n\nfunc b() {\n\treturn 1\n}\n"
	ops, err := parsePatch("*** Begin Patch\n*** Update File: x.go\n@@ func b() {\n-\treturn 1\n+\treturn 2\n*** End Patch")
	if err != nil {
		t.Fatal(err)
	}
	got, err := applyPatchHunks("x.go", content, ops[0].hunks)
	if err != nil {
		t.Fatal(err)
	}
	if got != "package main\n\nfunc a() {\n\treturn 1\n}\n\nfunc b() {\n\treturn 2\n}\n" {
		t.Fatalf("anchor did not select the second function:\n%s", got)
	}

	// Two hunks applied in order, context lines and trailing-space tolerance.
	ops, _ = parsePatch("*** Begin Patch\n*** Update File: x.go\n func a() {\n-\treturn 1   \n+\treturn 10\n@@\n func b() {\n-\treturn 1\n+\treturn 20\n*** End Patch")
	got, err = applyPatchHunks("x.go", content, ops[0].hunks)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "return 10") || !strings.Contains(got, "return 20") {
		t.Fatalf("got:\n%s", got)
	}

	// Pure insertion after an anchor.
	ops, _ = parsePatch("*** Begin Patch\n*** Update File: x.go\n@@ package main\n+import \"fmt\"\n*** End Patch")
	got, err = applyPatchHunks("x.go", content, ops[0].hunks)
	if err != nil || !strings.HasPrefix(got, "package main\nimport \"fmt\"\n") {
		t.Fatalf("insert: %q %v", got, err)
	}

	// A hunk that does not match is an error naming the lines.
	ops, _ = parsePatch("*** Begin Patch\n*** Update File: x.go\n-missing line\n+x\n*** End Patch")
	if _, err := applyPatchHunks("x.go", content, ops[0].hunks); err == nil || !strings.Contains(err.Error(), "missing line") {
		t.Fatalf("mismatch: %v", err)
	}

	// End of file anchoring.
	ops, _ = parsePatch("*** Begin Patch\n*** Update File: x.go\n-}\n+} // end\n*** End of File\n*** End Patch")
	got, err = applyPatchHunks("x.go", content, ops[0].hunks)
	if err != nil || !strings.HasSuffix(got, "\treturn 1\n} // end\n") {
		t.Fatalf("eof: %q %v", got, err)
	}
}
