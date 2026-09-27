// Package devfs performs one developer-space file operation inside the space
// container. It is compiled into the small static at-devfs helper that AT
// copies into each container, so the file explorer and the agent's file tools
// work in any image — including ones without python3.
//
// It is the single place that resolves paths: symlinks are resolved and the
// result must stay inside the project root, re-checked after resolution rather
// than trusting the string. It must stay behaviourally identical to the
// python3 fallback (PythonScript), which is still used
// when no helper is available for the container's platform.
//
// argv: op root [args...]; bulk payloads (file content) arrive on stdin. It
// prints one JSON document on success and exits 3 with {"error": ...} for a
// refusal the caller should surface verbatim. Unexpected failures exit 1 with
// the reason on stderr.
package devfs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Workspace is the persistent volume mount inside a developer space.
const Workspace = "/workspace"

const (
	maxRead        = 2 * 1024 * 1024
	maxList        = 5000
	maxWalk        = 10000
	maxSearchHits  = 2000
	maxSearchBytes = 4 * 1024 * 1024
	maxSearchLine  = 400
	maxLinkHops    = 40
)

// refusal is reported to the caller verbatim with exit code 3.
type refusal struct{ message string }

func (r *refusal) Error() string { return r.message }

func refuse(message string) error { return &refusal{message: message} }

// Run executes one operation against workspace and returns the exit code.
func Run(workspace string, argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	result, err := run(workspace, argv, stdin)
	if err != nil {
		var r *refusal
		if errors.As(err, &r) {
			_ = writeJSON(stdout, map[string]any{"error": r.message})
			return 3
		}
		_, _ = fmt.Fprintln(stderr, err.Error())
		return 1
	}
	if err := writeJSON(stdout, result); err != nil {
		_, _ = fmt.Fprintln(stderr, err.Error())
		return 1
	}
	return 0
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

type fsOp struct {
	root string
}

func run(workspace string, argv []string, stdin io.Reader) (any, error) {
	if len(argv) < 2 {
		return nil, refuse("unknown operation")
	}
	op, rootArg, args := argv[0], argv[1], argv[2:]

	ws, err := resolvePath(workspace, true)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	root := ws
	if rootArg != "" {
		if root, err = resolvePath(filepath.Join(ws, rootArg), false); err != nil {
			return nil, err
		}
	}
	if !within(root, ws) {
		return nil, refuse("path escapes the workspace")
	}
	o := &fsOp{root: root}

	arg := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	need := func(n int) error {
		if len(args) < n {
			return refuse("missing arguments")
		}
		return nil
	}

	switch op {
	case "list":
		return o.list(arg(0))
	case "read":
		if err := need(1); err != nil {
			return nil, err
		}
		return o.read(args[0])
	case "write":
		if err := need(1); err != nil {
			return nil, err
		}
		return o.write(args[0], arg(1), stdin)
	case "create":
		if err := need(2); err != nil {
			return nil, err
		}
		return o.create(args[0], args[1])
	case "rename":
		if err := need(2); err != nil {
			return nil, err
		}
		return o.rename(args[0], args[1])
	case "delete":
		if err := need(1); err != nil {
			return nil, err
		}
		return o.remove(args[0])
	case "walk":
		return o.walk(arg(0))
	case "search":
		if err := need(1); err != nil {
			return nil, err
		}
		return o.search(args[0], arg(1))
	default:
		return nil, refuse("unknown operation")
	}
}

// resolvePath returns the absolute path with every symlink resolved. With
// strict unset, a missing tail is appended lexically once the first missing
// component is reached (pathlib's resolve(strict=False)).
func resolvePath(p string, strict bool) (string, error) {
	hops := 0
	return resolveFrom("/", strings.Split(filepath.Clean("/"+p), "/"), strict, &hops)
}

func resolveFrom(cur string, parts []string, strict bool, hops *int) (string, error) {
	for i, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, part)
		info, err := os.Lstat(next)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && !strict {
				return filepath.Join(append([]string{cur}, parts[i:]...)...), nil
			}
			return "", err
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			cur = next
			continue
		}
		*hops++
		if *hops > maxLinkHops {
			return "", fmt.Errorf("too many levels of symbolic links: %s", next)
		}
		target, err := os.Readlink(next)
		if err != nil {
			return "", err
		}
		base := cur
		if filepath.IsAbs(target) {
			base = "/"
		}
		if cur, err = resolveFrom(base, strings.Split(target, "/"), strict, hops); err != nil {
			return "", err
		}
	}
	return cur, nil
}

func within(p, root string) bool {
	return p == root || strings.HasPrefix(p, strings.TrimSuffix(root, "/")+"/")
}

func (o *fsOp) resolve(rel string, mustExist, allowRoot bool) (string, error) {
	rel = strings.Trim(strings.TrimSpace(rel), "/")
	if rel == "" || rel == "." {
		if allowRoot {
			return o.root, nil
		}
		return "", refuse("path is required")
	}
	resolved, err := resolvePath(filepath.Join(o.root, rel), mustExist)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", refuse("not found: " + rel)
		}
		return "", err
	}
	if !within(resolved, o.root) {
		return "", refuse("path escapes the project: " + rel)
	}
	return resolved, nil
}

func (o *fsOp) relpath(p string) string {
	rel, err := filepath.Rel(o.root, p)
	if err != nil || rel == "." {
		return ""
	}
	return rel
}

type entry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Type  string `json:"type"`
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"`
}

func (o *fsOp) entry(p string) (entry, error) {
	info, err := os.Lstat(p)
	if err != nil {
		return entry{}, err
	}
	kind := "file"
	switch {
	case info.IsDir():
		kind = "dir"
	case info.Mode()&fs.ModeSymlink != 0:
		kind = "symlink"
	}
	return entry{Name: filepath.Base(p), Path: o.relpath(p), Type: kind, Size: info.Size(), Mtime: info.ModTime().Unix()}, nil
}

func versionOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:24]
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

func isSymlink(p string) bool {
	info, err := os.Lstat(p)
	return err == nil && info.Mode()&fs.ModeSymlink != 0
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (o *fsOp) list(rel string) (any, error) {
	target, err := o.resolve(rel, true, true)
	if err != nil {
		return nil, err
	}
	if !isDir(target) {
		return nil, refuse("not a directory")
	}
	children, err := os.ReadDir(target)
	if err != nil {
		return nil, err
	}
	type child struct {
		path  string
		later bool
		key   string
	}
	sorted := make([]child, 0, len(children))
	for _, c := range children {
		p := filepath.Join(target, c.Name())
		sorted = append(sorted, child{path: p, later: !isDir(p) || isSymlink(p), key: strings.ToLower(c.Name())})
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].later != sorted[j].later {
			return !sorted[i].later
		}
		if sorted[i].key != sorted[j].key {
			return sorted[i].key < sorted[j].key
		}
		return filepath.Base(sorted[i].path) < filepath.Base(sorted[j].path)
	})
	items := make([]entry, 0, len(sorted))
	for _, c := range sorted {
		e, err := o.entry(c.path)
		if err != nil {
			continue
		}
		items = append(items, e)
		if len(items) >= maxList {
			break
		}
	}
	return map[string]any{"path": o.relpath(target), "entries": items, "truncated": len(items) >= maxList}, nil
}

func (o *fsOp) read(rel string) (any, error) {
	target, err := o.resolve(rel, true, false)
	if err != nil {
		return nil, err
	}
	if !isFile(target) {
		return nil, refuse("not a file")
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxRead {
		return map[string]any{"path": o.relpath(target), "size": info.Size(), "too_large": true}, nil
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return nil, err
	}
	if utf8.Valid(data) {
		return map[string]any{"path": o.relpath(target), "size": info.Size(), "content": string(data), "version": versionOf(data)}, nil
	}
	return map[string]any{"path": o.relpath(target), "size": info.Size(), "binary": true, "version": versionOf(data)}, nil
}

func (o *fsOp) write(rel, expected string, stdin io.Reader) (any, error) {
	target, err := o.resolve(rel, false, false)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return nil, fmt.Errorf("read content: %w", err)
	}
	if exists(target) {
		if isDir(target) {
			return nil, refuse("a directory exists at this path")
		}
		if expected != "" {
			current, err := os.ReadFile(target)
			if err != nil {
				return nil, err
			}
			if versionOf(current) != expected {
				return nil, refuse("conflict: the file changed since it was opened")
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o777); err != nil {
		return nil, err
	}
	tmp := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".at-tmp")
	if err := os.WriteFile(tmp, data, 0o666); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	return map[string]any{"path": o.relpath(target), "size": len(data), "version": versionOf(data)}, nil
}

func (o *fsOp) create(kind, rel string) (any, error) {
	target, err := o.resolve(rel, false, false)
	if err != nil {
		return nil, err
	}
	if exists(target) || isSymlink(target) {
		return nil, refuse("already exists: " + rel)
	}
	if kind == "dir" {
		if err := os.MkdirAll(target, 0o777); err != nil {
			return nil, err
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(target), 0o777); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o666)
		if err != nil {
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
	}
	return o.entry(target)
}

func (o *fsOp) rename(from, to string) (any, error) {
	source, err := o.resolve(from, true, false)
	if err != nil {
		return nil, err
	}
	if source == o.root {
		return nil, refuse("cannot rename the project root")
	}
	dest, err := o.resolve(to, false, false)
	if err != nil {
		return nil, err
	}
	if exists(dest) {
		return nil, refuse("destination already exists")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o777); err != nil {
		return nil, err
	}
	if err := os.Rename(source, dest); err != nil {
		return nil, err
	}
	return o.entry(dest)
}

func (o *fsOp) remove(rel string) (any, error) {
	rel = strings.Trim(strings.TrimSpace(rel), "/")
	if rel == "" || rel == "." {
		return nil, refuse("cannot delete the project root")
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." {
			return nil, refuse("not found: " + rel)
		}
	}
	// The parent is resolved, the final component is not: deleting a symlink
	// removes the link, never its target.
	parent, err := resolvePath(filepath.Join(o.root, filepath.Dir(rel)), true)
	if err != nil || !within(parent, o.root) {
		return nil, refuse("not found: " + rel)
	}
	p := filepath.Join(parent, filepath.Base(rel))
	switch {
	case isSymlink(p) || isFile(p):
		if err := os.Remove(p); err != nil {
			return nil, err
		}
	case isDir(p):
		if err := os.RemoveAll(p); err != nil {
			return nil, err
		}
	default:
		return nil, refuse("not found: " + rel)
	}
	return map[string]any{"deleted": rel}, nil
}

func skippedDir(name string) bool { return name == ".git" || name == "node_modules" }

// walkTree visits directories depth-first in sorted order, reporting each
// directory's files before descending (os.walk, top-down). Symlinked
// directories are never followed. visit returns false to stop.
func walkTree(dir string, visit func(path string, isLink bool) bool) bool {
	children, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	var dirs, files []string
	for _, c := range children {
		p := filepath.Join(dir, c.Name())
		if isDir(p) {
			dirs = append(dirs, c.Name())
		} else {
			files = append(files, c.Name())
		}
	}
	sort.Strings(dirs)
	sort.Strings(files)
	for _, name := range files {
		p := filepath.Join(dir, name)
		if !visit(p, isSymlink(p)) {
			return false
		}
	}
	for _, name := range dirs {
		p := filepath.Join(dir, name)
		if skippedDir(name) || isSymlink(p) {
			continue
		}
		if !walkTree(p, visit) {
			return false
		}
	}
	return true
}

func (o *fsOp) walk(rel string) (any, error) {
	target, err := o.resolve(rel, true, true)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0)
	if isDir(target) {
		walkTree(target, func(p string, isLink bool) bool {
			if isLink {
				return true
			}
			out = append(out, o.relpath(p))
			return len(out) < maxWalk
		})
	}
	return map[string]any{"files": out, "truncated": len(out) >= maxWalk}, nil
}

type hit struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

func (o *fsOp) search(pattern, startRel string) (any, error) {
	rx, err := regexp.Compile(pattern)
	if err != nil {
		return nil, refuse("invalid pattern: " + err.Error())
	}
	target, err := o.resolve(startRel, true, true)
	if err != nil {
		return nil, err
	}
	hits := make([]hit, 0)
	scan := func(p string) bool {
		if isSymlink(p) || !isFile(p) {
			return true
		}
		info, err := os.Stat(p)
		if err != nil || info.Size() > maxSearchBytes {
			return true
		}
		data, err := os.ReadFile(p)
		if err != nil || !utf8.Valid(data) {
			return true
		}
		for no, line := range splitLines(data) {
			if !rx.Match(line) {
				continue
			}
			hits = append(hits, hit{Path: o.relpath(p), Line: no + 1, Text: truncateRunes(string(line), maxSearchLine)})
			if len(hits) >= maxSearchHits {
				return false
			}
		}
		return true
	}
	if isFile(target) {
		scan(target)
	} else {
		walkTree(target, func(p string, _ bool) bool { return scan(p) })
	}
	return map[string]any{"matches": hits, "truncated": len(hits) >= maxSearchHits}, nil
}

// splitLines splits on \n, \r\n and \r without a trailing empty line.
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	for len(data) > 0 {
		i := bytes.IndexAny(data, "\r\n")
		if i < 0 {
			lines = append(lines, data)
			break
		}
		lines = append(lines, data[:i])
		if data[i] == '\r' && i+1 < len(data) && data[i+1] == '\n' {
			i++
		}
		data = data[i+1:]
	}
	return lines
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}
