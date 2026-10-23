package server

import (
	"fmt"
	"strings"
)

// The patch format of OpenCode / Codex `apply_patch`:
//
//	*** Begin Patch
//	*** Add File: path
//	+line
//	*** Update File: path
//	*** Move to: new/path        (optional)
//	@@ optional context line
//	 context
//	-removed
//	+added
//	*** Delete File: path
//	*** End Patch
//
// Hunks are located by their context and removed lines, not by line numbers,
// so a model does not have to count lines.

type patchOpKind int

const (
	patchAdd patchOpKind = iota
	patchUpdate
	patchDelete
)

type patchOp struct {
	kind   patchOpKind
	path   string
	moveTo string
	// content is the new file for Add.
	content string
	hunks   []patchHunk
}

type patchHunk struct {
	// anchor is the text after @@, a line expected before the hunk.
	anchor string
	old    []string
	new    []string
	// endOfFile marks a hunk that must match at the end of the file.
	endOfFile bool
}

func parsePatch(text string) ([]patchOp, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "*** Begin Patch" {
		return nil, fmt.Errorf("patch must start with *** Begin Patch")
	}
	if strings.TrimSpace(lines[len(lines)-1]) != "*** End Patch" {
		return nil, fmt.Errorf("patch must end with *** End Patch")
	}
	lines = lines[1 : len(lines)-1]
	var ops []patchOp
	for i := 0; i < len(lines); {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			op := patchOp{kind: patchAdd, path: strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: "))}
			i++
			var body []string
			for i < len(lines) && !strings.HasPrefix(lines[i], "*** ") {
				if !strings.HasPrefix(lines[i], "+") {
					return nil, fmt.Errorf("Add File %s: every line must start with +, got %q", op.path, lines[i])
				}
				body = append(body, lines[i][1:])
				i++
			}
			op.content = strings.Join(body, "\n")
			if len(body) > 0 {
				op.content += "\n"
			}
			ops = append(ops, op)
		case strings.HasPrefix(line, "*** Delete File: "):
			ops = append(ops, patchOp{kind: patchDelete, path: strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: "))})
			i++
		case strings.HasPrefix(line, "*** Update File: "):
			op := patchOp{kind: patchUpdate, path: strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))}
			i++
			if i < len(lines) && strings.HasPrefix(lines[i], "*** Move to: ") {
				op.moveTo = strings.TrimSpace(strings.TrimPrefix(lines[i], "*** Move to: "))
				i++
			}
			var hunk *patchHunk
			flush := func() {
				if hunk != nil && (len(hunk.old) > 0 || len(hunk.new) > 0) {
					op.hunks = append(op.hunks, *hunk)
				}
				hunk = nil
			}
			for i < len(lines) && (!strings.HasPrefix(lines[i], "*** ") || lines[i] == "*** End of File") {
				l := lines[i]
				i++
				switch {
				case l == "*** End of File":
					if hunk != nil {
						hunk.endOfFile = true
					}
				case strings.HasPrefix(l, "@@"):
					flush()
					hunk = &patchHunk{anchor: strings.TrimSpace(strings.TrimPrefix(l, "@@"))}
				default:
					if hunk == nil {
						hunk = &patchHunk{}
					}
					switch {
					case l == "":
						hunk.old = append(hunk.old, "")
						hunk.new = append(hunk.new, "")
					case l[0] == ' ':
						hunk.old = append(hunk.old, l[1:])
						hunk.new = append(hunk.new, l[1:])
					case l[0] == '-':
						hunk.old = append(hunk.old, l[1:])
					case l[0] == '+':
						hunk.new = append(hunk.new, l[1:])
					default:
						return nil, fmt.Errorf("Update File %s: line must start with ' ', '-' or '+', got %q", op.path, l)
					}
				}
			}
			flush()
			if len(op.hunks) == 0 && op.moveTo == "" {
				return nil, fmt.Errorf("Update File %s has no changes", op.path)
			}
			ops = append(ops, op)
		case strings.TrimSpace(line) == "":
			i++
		default:
			return nil, fmt.Errorf("unexpected patch line %q; expected *** Add File, *** Update File or *** Delete File", line)
		}
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("patch contains no file operations")
	}
	for _, op := range ops {
		if op.path == "" {
			return nil, fmt.Errorf("patch operation without a path")
		}
	}
	return ops, nil
}

// applyPatchHunks applies an Update operation's hunks to content. Matching is
// exact first, then ignoring trailing whitespace, then ignoring surrounding
// whitespace, each searched forward from the previous hunk.
func applyPatchHunks(path, content string, hunks []patchHunk) (string, error) {
	trailingNewline := strings.HasSuffix(content, "\n")
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	if content == "" {
		lines = nil
	}
	cursor := 0
	for n, h := range hunks {
		start := cursor
		if h.anchor != "" {
			if at := findPatchLines(lines, []string{h.anchor}, cursor, false); at >= 0 {
				start = at + 1
			} else {
				return "", fmt.Errorf("%s: hunk %d: context line %q not found", path, n+1, h.anchor)
			}
		}
		if len(h.old) == 0 {
			// Pure insertion: after the anchor, or at the end of the file.
			at := len(lines)
			if h.anchor != "" {
				at = start
			}
			lines = append(lines[:at], append(append([]string{}, h.new...), lines[at:]...)...)
			cursor = at + len(h.new)
			continue
		}
		at := findPatchLines(lines, h.old, start, h.endOfFile)
		if at < 0 {
			return "", fmt.Errorf("%s: hunk %d: the lines to replace were not found; read the file and copy them exactly:\n%s", path, n+1, strings.Join(h.old, "\n"))
		}
		lines = append(lines[:at], append(append([]string{}, h.new...), lines[at+len(h.old):]...)...)
		cursor = at + len(h.new)
	}
	out := strings.Join(lines, "\n")
	if len(lines) > 0 && (trailingNewline || content == "") {
		out += "\n"
	}
	return out, nil
}

func findPatchLines(lines, want []string, from int, endOfFile bool) int {
	normalizers := []func(string) string{
		func(s string) string { return s },
		func(s string) string { return strings.TrimRight(s, " \t") },
		strings.TrimSpace,
	}
	for _, norm := range normalizers {
		if endOfFile {
			at := len(lines) - len(want)
			if at >= from && matchPatchLines(lines[at:], want, norm) {
				return at
			}
			continue
		}
		for at := from; at+len(want) <= len(lines); at++ {
			if matchPatchLines(lines[at:at+len(want)], want, norm) {
				return at
			}
		}
	}
	return -1
}

func matchPatchLines(got, want []string, norm func(string) string) bool {
	for i := range want {
		if norm(got[i]) != norm(want[i]) {
			return false
		}
	}
	return true
}
