package devfs

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fixture builds a project tree with the shapes the containment rules care
// about: nested files, a skipped directory, and symlinks inside and outside.
func fixture(t *testing.T) (workspace, outside string) {
	t.Helper()
	base := t.TempDir()
	workspace = filepath.Join(base, "workspace")
	outside = filepath.Join(base, "outside")
	files := map[string]string{
		"proj/README.md":             "hello\nworld\n",
		"proj/src/main.go":           "package main\n\nfunc main() {}\n",
		"proj/src/B.txt":             "Beta\r\nneedle here\r\n",
		"proj/src/a.txt":             "alpha needle\n",
		"proj/node_modules/x/i.js":   "needle\n",
		"proj/.git/HEAD":             "ref: needle\n",
		"proj/bin.dat":               "\xff\xfe\x00binary",
		"proj/Zdir/inner/deep.txt":   "deep needle\n",
		"other/secret.txt":           "other project\n",
		"../outside/host-secret.txt": "host\n",
	}
	for rel, content := range files {
		p := filepath.Join(workspace, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	links := map[string]string{
		"proj/escape":      outside,
		"proj/root-link":   "/",
		"proj/rel-escape":  "../../outside",
		"proj/sibling":     "../other",
		"proj/inner-link":  "src/a.txt",
		"proj/dir-link":    "src",
		"proj/dangling":    "missing-target",
		"proj/src/up-link": "..",
	}
	for rel, target := range links {
		if err := os.Symlink(target, filepath.Join(workspace, rel)); err != nil {
			t.Fatal(err)
		}
	}
	return workspace, outside
}

func runGo(t *testing.T, workspace string, stdin string, argv ...string) (int, map[string]any) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(workspace, argv, strings.NewReader(stdin), &stdout, &stderr)
	return code, decode(t, code, stdout.Bytes(), stderr.String())
}

func decode(t *testing.T, code int, stdout []byte, stderr string) map[string]any {
	t.Helper()
	if code != 0 && code != 3 {
		return map[string]any{"__exit": code, "__stderr": stderr}
	}
	var out map[string]any
	if err := json.Unmarshal(stdout, &out); err != nil {
		t.Fatalf("output is not JSON (code %d): %q stderr=%q", code, stdout, stderr)
	}
	return out
}

func TestContainment(t *testing.T) {
	workspace, _ := fixture(t)
	refused := []struct {
		name string
		argv []string
		want string
	}{
		{"absolute symlink", []string{"read", "proj", "escape/host-secret.txt"}, "path escapes the project"},
		{"root symlink", []string{"list", "proj", "root-link"}, "path escapes the project"},
		{"relative symlink", []string{"read", "proj", "rel-escape/host-secret.txt"}, "path escapes the project"},
		{"sibling project", []string{"read", "proj", "sibling/secret.txt"}, "path escapes the project"},
		{"dotdot", []string{"read", "proj", "../other/secret.txt"}, "path escapes the project"},
		{"root outside workspace", []string{"list", "../outside", ""}, "path escapes the workspace"},
		{"write through symlink", []string{"write", "proj", "escape/new.txt"}, "path escapes the project"},
		{"create through symlink", []string{"create", "proj", "file", "escape/new.txt"}, "path escapes the project"},
		{"rename out", []string{"rename", "proj", "README.md", "escape/README.md"}, "path escapes the project"},
		{"walk out", []string{"walk", "proj", "escape"}, "path escapes the project"},
		{"search out", []string{"search", "proj", "host", "escape"}, "path escapes the project"},
		{"delete through link dir", []string{"delete", "proj", "escape/host-secret.txt"}, "not found"},
		{"delete dotdot", []string{"delete", "proj", "../other/secret.txt"}, "not found"},
		{"delete root", []string{"delete", "proj", "/"}, "cannot delete the project root"},
		{"rename root", []string{"rename", "proj", "src/up-link", "x"}, "cannot rename the project root"},
		{"missing", []string{"read", "proj", "nope.txt"}, "not found: nope.txt"},
		{"unknown op", []string{"chmod", "proj", "x"}, "unknown operation"},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			code, out := runGo(t, workspace, "", tt.argv...)
			msg, _ := out["error"].(string)
			if code != 3 || !strings.HasPrefix(msg, tt.want) {
				t.Fatalf("got code=%d out=%v, want refusal %q", code, out, tt.want)
			}
		})
	}
	if data, _ := os.ReadFile(filepath.Join(workspace, "..", "outside", "host-secret.txt")); string(data) != "host\n" {
		t.Fatal("a file outside the project was modified")
	}
	if _, err := os.Stat(filepath.Join(workspace, "..", "outside", "new.txt")); err == nil {
		t.Fatal("a file was created outside the project")
	}

	// Deleting a symlink removes the link, never its target.
	if code, out := runGo(t, workspace, "", "delete", "proj", "escape"); code != 0 {
		t.Fatalf("delete link: %v", out)
	}
	if _, err := os.Stat(filepath.Join(workspace, "..", "outside", "host-secret.txt")); err != nil {
		t.Fatal("deleting a symlink removed its target")
	}
}

func TestWriteVersioning(t *testing.T) {
	workspace, _ := fixture(t)
	code, read := runGo(t, workspace, "", "read", "proj", "README.md")
	if code != 0 || read["content"] != "hello\nworld\n" {
		t.Fatalf("read: %v", read)
	}
	version := read["version"].(string)
	if code, out := runGo(t, workspace, "changed\n", "write", "proj", "README.md", version); code != 0 {
		t.Fatalf("write: %v", out)
	}
	if code, out := runGo(t, workspace, "again\n", "write", "proj", "README.md", version); code != 3 || !strings.HasPrefix(out["error"].(string), "conflict") {
		t.Fatalf("a stale version must conflict: %v", out)
	}
	big := strings.Repeat("x", 1<<20)
	if code, out := runGo(t, workspace, big, "write", "proj", "nested/new/big.txt"); code != 0 || out["size"].(float64) != float64(len(big)) {
		t.Fatalf("large write through stdin: %v", out)
	}
	if entries, _ := os.ReadDir(filepath.Join(workspace, "proj", "nested", "new")); len(entries) != 1 {
		t.Fatalf("temporary file left behind: %v", entries)
	}
}

// TestPythonParity runs every operation through both implementations on
// identical trees and requires identical JSON, so the helper and the python3
// fallback cannot drift apart.
func TestPythonParity(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	calls := []struct {
		stdin string
		argv  []string
	}{
		{"", []string{"list", "", ""}},
		{"", []string{"list", "proj", ""}},
		{"", []string{"list", "proj", "src"}},
		{"", []string{"list", "proj", "dir-link"}},
		{"", []string{"list", "proj", "README.md"}},
		{"", []string{"read", "proj", "README.md"}},
		{"", []string{"read", "proj", "inner-link"}},
		{"", []string{"read", "proj", "bin.dat"}},
		{"", []string{"read", "proj", "src"}},
		{"", []string{"read", "proj", "dangling"}},
		{"", []string{"read", "proj", ""}},
		{"", []string{"read", "proj", "escape/host-secret.txt"}},
		{"", []string{"read", "proj", "sibling/secret.txt"}},
		{"", []string{"list", "../outside", ""}},
		{"", []string{"walk", "proj", ""}},
		{"", []string{"walk", "proj", "src"}},
		{"", []string{"search", "proj", "needle", ""}},
		{"", []string{"search", "proj", "^Beta$", "src"}},
		{"", []string{"search", "proj", "needle", "src/a.txt"}},
		{"", []string{"search", "proj", "(unclosed", ""}},
		{"new\n", []string{"write", "proj", "src/new.txt", ""}},
		{"x", []string{"write", "proj", "src", ""}},
		{"x", []string{"write", "proj", "README.md", "stale"}},
		{"", []string{"create", "proj", "file", "made.txt"}},
		{"", []string{"create", "proj", "dir", "made/dir"}},
		{"", []string{"create", "proj", "file", "README.md"}},
		{"", []string{"create", "proj", "file", "dangling"}},
		{"", []string{"rename", "proj", "src/a.txt", "moved/a.txt"}},
		{"", []string{"rename", "proj", "README.md", "src/B.txt"}},
		{"", []string{"delete", "proj", "inner-link"}},
		{"", []string{"delete", "proj", "Zdir"}},
		{"", []string{"delete", "proj", "missing"}},
		{"", []string{"delete", "proj", ""}},
		{"", []string{"list", "proj", ""}},
	}

	goWS, _ := fixture(t)
	pyWS, _ := fixture(t)
	script := strings.Replace(PythonScript, `pathlib.Path("/workspace")`, "pathlib.Path("+strconvQuote(pyWS)+")", 1)
	if script == PythonScript {
		t.Fatal("could not redirect the python workspace")
	}
	for _, call := range calls {
		name := strings.Join(call.argv, " ")
		_, goOut := runGo(t, goWS, call.stdin, call.argv...)

		cmd := exec.Command("python3", append([]string{"-c", script}, call.argv...)...)
		cmd.Stdin = strings.NewReader(call.stdin)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		code := 0
		if err := cmd.Run(); err != nil {
			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(err)
			}
			code = exitErr.ExitCode()
		}
		pyOut := decode(t, code, stdout.Bytes(), stderr.String())
		normalize(goOut)
		normalize(pyOut)
		if !reflect.DeepEqual(goOut, pyOut) {
			t.Errorf("%s:\n go: %v\n py: %v", name, goOut, pyOut)
		}
	}
}

// normalize drops fields that legitimately differ between two trees built at
// different moments, and error text that embeds a regex engine's wording.
func normalize(v map[string]any) {
	delete(v, "mtime")
	if msg, ok := v["error"].(string); ok && strings.HasPrefix(msg, "invalid pattern:") {
		v["error"] = "invalid pattern:"
	}
	if _, ok := v["__stderr"]; ok {
		v["__stderr"] = ""
	}
	for _, key := range []string{"entries"} {
		if list, ok := v[key].([]any); ok {
			for _, item := range list {
				if m, ok := item.(map[string]any); ok {
					delete(m, "mtime")
					if m["type"] == "dir" {
						delete(m, "size") // directory sizes depend on the filesystem's history
					}
				}
			}
		}
	}
}

func strconvQuote(s string) string {
	encoded, _ := json.Marshal(s)
	return string(encoded)
}
