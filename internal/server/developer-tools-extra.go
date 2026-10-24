package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

// ─── patch ───

const developerPatchDescription = "Apply a multi-file patch in one call (adds, updates, deletes and moves files). Format:\n" +
	"*** Begin Patch\n*** Add File: path/new.go\n+line\n*** Update File: path/old.go\n*** Move to: path/renamed.go   (optional)\n@@ optional line before the change\n context line\n-removed line\n+added line\n*** Delete File: path/gone.go\n*** End Patch\n" +
	"Paths are relative to the project. Hunks are found by their context and removed lines (no line numbers); keep 2-3 context lines so each hunk is unique. " +
	"Every operation is checked before any file is written."

type developerPatchWrite struct {
	rel, content, version string
}

// applyDeveloperPatch validates and reads every touched file first, then
// writes, so a hunk that does not apply leaves the project unchanged.
func (s *Server) applyDeveloperPatch(ctx context.Context, h *developerRuntimeHandle, project, text string) (string, []string, error) {
	ops, err := parsePatch(text)
	if err != nil {
		return "", nil, err
	}
	var writes []developerPatchWrite
	var deletes []string
	var moves [][2]string
	var summary []string
	for _, op := range ops {
		rel, err := developerProjectFilePath(op.path)
		if err != nil {
			return "", nil, fmt.Errorf("%s: %w", op.path, err)
		}
		switch op.kind {
		case patchAdd:
			if _, err := s.runDeveloperFS(ctx, h, nil, "read", project, rel); err == nil {
				return "", nil, fmt.Errorf("Add File %s: the file already exists; use Update File", rel) //nolint:staticcheck // mirrors the patch header
			}
			if len(op.content) > developerWriteMaxBytes {
				return "", nil, fmt.Errorf("%s exceeds 2 MiB", rel)
			}
			writes = append(writes, developerPatchWrite{rel: rel, content: op.content})
			summary = append(summary, "A "+rel)
		case patchDelete:
			if _, err := s.runDeveloperFS(ctx, h, nil, "read", project, rel); err != nil {
				return "", nil, fmt.Errorf("Delete File %s: %w", rel, err) //nolint:staticcheck // mirrors the patch header
			}
			deletes = append(deletes, rel)
			summary = append(summary, "D "+rel)
		case patchUpdate:
			raw, err := s.runDeveloperFS(ctx, h, nil, "read", project, rel)
			if err != nil {
				return "", nil, fmt.Errorf("Update File %s: %w", rel, err) //nolint:staticcheck // mirrors the patch header
			}
			var file struct {
				Content  string `json:"content"`
				Version  string `json:"version"`
				Binary   bool   `json:"binary"`
				TooLarge bool   `json:"too_large"`
			}
			if err := json.Unmarshal(raw, &file); err != nil || file.Binary || file.TooLarge {
				return "", nil, fmt.Errorf("%s is not an editable text file", rel)
			}
			updated := file.Content
			if len(op.hunks) > 0 {
				if updated, err = applyPatchHunks(rel, file.Content, op.hunks); err != nil {
					return "", nil, err
				}
			}
			if len(updated) > developerWriteMaxBytes {
				return "", nil, fmt.Errorf("%s would exceed 2 MiB", rel)
			}
			writes = append(writes, developerPatchWrite{rel: rel, content: updated, version: file.Version})
			if op.moveTo != "" {
				dest, err := developerProjectFilePath(op.moveTo)
				if err != nil {
					return "", nil, fmt.Errorf("Move to %s: %w", op.moveTo, err) //nolint:staticcheck // mirrors the patch header
				}
				moves = append(moves, [2]string{rel, dest})
				summary = append(summary, "R "+rel+" -> "+dest)
			} else {
				summary = append(summary, "M "+rel)
			}
		}
	}
	// changed lists the files that exist after the patch (for formatting and
	// editor reloads); moved-from and deleted paths are only in the summary.
	moved := map[string]string{}
	for _, m := range moves {
		moved[m[0]] = m[1]
	}
	var changed []string
	for _, w := range writes {
		if _, err := s.runDeveloperFS(ctx, h, strings.NewReader(w.content), "write", project, w.rel, w.version); err != nil {
			return "", changed, fmt.Errorf("write %s: %w", w.rel, err)
		}
		if _, isMoved := moved[w.rel]; !isMoved {
			changed = append(changed, w.rel)
		}
	}
	for _, m := range moves {
		if _, err := s.runDeveloperFS(ctx, h, nil, "rename", project, m[0], m[1]); err != nil {
			return "", changed, fmt.Errorf("move %s: %w", m[0], err)
		}
		changed = append(changed, m[1])
	}
	for _, rel := range deletes {
		if _, err := s.runDeveloperFS(ctx, h, nil, "delete", project, rel); err != nil {
			return "", changed, fmt.Errorf("delete %s: %w", rel, err)
		}
	}
	return "Applied patch:\n" + strings.Join(summary, "\n"), changed, nil
}

// ─── Background shell jobs ───
//
// `bash` with background: true starts the command detached inside the
// container, writing combined output to a log under /tmp/at-jobs. The job
// survives the tool call (and the turn); bash_output reads it and bash_kill
// stops it. Jobs live as long as the container: a stopped space ends them.

const developerJobDir = "/tmp/at-jobs"

var developerJobIDPattern = regexp.MustCompile(`^[0-9a-z]{26}$`)

func (s *Server) startDeveloperBackgroundJob(ctx context.Context, h *developerRuntimeHandle, project, command string) (string, error) {
	id := strings.ToLower(ulid.Make().String())
	script := `set -e
mkdir -p "$AT_JOB_DIR"
if command -v bash >/dev/null 2>&1; then sh=bash; else sh=sh; fi
log="$AT_JOB_DIR/$AT_JOB_ID.log"
: > "$log"
if command -v setsid >/dev/null 2>&1; then
  setsid "$sh" -c "$AT_COMMAND; echo \"[exit \$?]\" >> \"$log\"" >> "$log" 2>&1 < /dev/null &
else
  nohup "$sh" -c "$AT_COMMAND; echo \"[exit \$?]\" >> \"$log\"" >> "$log" 2>&1 < /dev/null &
fi
echo $! > "$AT_JOB_DIR/$AT_JOB_ID.pid"
printf '%s\n' "$PWD" > "$AT_JOB_DIR/$AT_JOB_ID.cwd"
printf '%s' "$AT_COMMAND" > "$AT_JOB_DIR/$AT_JOB_ID.cmd"`
	env := map[string]string{"AT_COMMAND": command, "AT_JOB_DIR": developerJobDir, "AT_JOB_ID": id}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, stderr, code, err := s.containerManager.ExecArgs(ctx, h.scope, h.cfg, service.DeveloperAbsolutePath(project), env, "sh", "-c", script)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", developerCommandFailure("start background job", stderr, nil)
	}
	return fmt.Sprintf("Started background job %s. Use bash_output with job_id %q to read its output and see whether it finished, and bash_kill to stop it.", id, id), nil
}

func (s *Server) developerJobOutput(ctx context.Context, h *developerRuntimeHandle, args map[string]any) (string, error) {
	id := strings.TrimSpace(stringArg(args, "job_id"))
	if id == "" {
		return s.listDeveloperJobs(ctx, h)
	}
	if !developerJobIDPattern.MatchString(id) {
		return "", fmt.Errorf("invalid job_id")
	}
	offset := intArg(args["offset"], 0)
	if offset < 0 {
		offset = 0
	}
	script := `log="$AT_JOB_DIR/$AT_JOB_ID.log"
[ -f "$log" ] || { echo "no such job" >&2; exit 4; }
if tail -n 1 "$log" | grep -q '^\[exit [0-9]*\]$'; then echo finished; else
  pid=$(cat "$AT_JOB_DIR/$AT_JOB_ID.pid" 2>/dev/null || true)
  # A reaped-late child can linger as a zombie; only a live, non-zombie pid is running.
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null && ! grep -q '^State:.*Z' "/proc/$pid/status" 2>/dev/null; then echo running; else echo finished; fi
fi
wc -c < "$log"
tail -c +$((AT_OFFSET + 1)) "$log" | head -c "$AT_MAX"`
	env := map[string]string{"AT_JOB_DIR": developerJobDir, "AT_JOB_ID": id, "AT_OFFSET": fmt.Sprint(offset), "AT_MAX": fmt.Sprint(developerBashOutputMaxBytes * 4)}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stdout, stderr, code, err := s.containerManager.ExecArgs(ctx, h.scope, h.cfg, service.DeveloperWorkspaceRoot, env, "sh", "-c", script)
	if err != nil {
		return "", err
	}
	if code == 4 {
		return "", fmt.Errorf("no background job %q (jobs end when the space stops)", id)
	}
	if code != 0 {
		return "", developerCommandFailure("read job output", stderr, nil)
	}
	state, rest, _ := strings.Cut(stdout, "\n")
	sizeLine, output, _ := strings.Cut(rest, "\n")
	var size int
	_, _ = fmt.Sscan(strings.TrimSpace(sizeLine), &size)
	output = developerClipOutput(output)
	next := offset + len(output)
	if size > next {
		next = min(size, offset+developerBashOutputMaxBytes*4)
	}
	header := fmt.Sprintf("job %s: %s; %d bytes of output", id, strings.TrimSpace(state), size)
	if size > next {
		header += fmt.Sprintf("; pass offset %d for more", next)
	} else if strings.TrimSpace(state) == "running" {
		header += fmt.Sprintf("; pass offset %d next time to read only new output", size)
	}
	if strings.TrimSpace(output) == "" {
		return header + "\n(no new output)", nil
	}
	return header + "\n" + output, nil
}

func (s *Server) listDeveloperJobs(ctx context.Context, h *developerRuntimeHandle) (string, error) {
	script := `cd "$AT_JOB_DIR" 2>/dev/null || exit 0
for f in *.pid; do [ -e "$f" ] || continue; id=${f%.pid}; pid=$(cat "$f"); s=finished
  if ! tail -n 1 "$id.log" 2>/dev/null | grep -q '^\[exit [0-9]*\]$' && kill -0 "$pid" 2>/dev/null && ! grep -q '^State:.*Z' "/proc/$pid/status" 2>/dev/null; then s=running; fi
  printf '%s\t%s\t%s\n' "$id" "$s" "$(head -c 200 "$id.cmd" 2>/dev/null)"; done`
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stdout, _, _, err := s.containerManager.ExecArgs(ctx, h.scope, h.cfg, service.DeveloperWorkspaceRoot, map[string]string{"AT_JOB_DIR": developerJobDir}, "sh", "-c", script)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(stdout) == "" {
		return "No background jobs.", nil
	}
	return "job_id\tstate\tcommand\n" + stdout, nil
}

func (s *Server) killDeveloperJob(ctx context.Context, h *developerRuntimeHandle, args map[string]any) (string, error) {
	id := strings.TrimSpace(stringArg(args, "job_id"))
	if !developerJobIDPattern.MatchString(id) {
		return "", fmt.Errorf("job_id is required")
	}
	script := `pid=$(cat "$AT_JOB_DIR/$AT_JOB_ID.pid" 2>/dev/null) || { echo "no such job" >&2; exit 4; }
if ! kill -0 "$pid" 2>/dev/null || grep -q '^State:.*Z' "/proc/$pid/status" 2>/dev/null; then echo "already finished"; exit 0; fi
kill -TERM -"$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
sleep 1
kill -KILL -"$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true
echo stopped`
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stdout, stderr, code, err := s.containerManager.ExecArgs(ctx, h.scope, h.cfg, service.DeveloperWorkspaceRoot, map[string]string{"AT_JOB_DIR": developerJobDir, "AT_JOB_ID": id}, "sh", "-c", script)
	if err != nil {
		return "", err
	}
	if code == 4 {
		return "", fmt.Errorf("no background job %q", id)
	}
	if code != 0 {
		return "", developerCommandFailure("stop job", stderr, nil)
	}
	return fmt.Sprintf("job %s: %s", id, strings.TrimSpace(stdout)), nil
}

// ─── Formatters ───
//
// After write_file / edit_file / patch the changed file is formatted with the
// first matching formatter that exists in the container (OpenCode's built-in
// list, trimmed to the common ones). A formatter that is missing, or fails, is
// skipped silently: formatting must never fail an edit.

type developerFormatter struct {
	extensions []string
	// command is run with $FILE set to the absolute path. It exits 127 when
	// the formatter is not installed.
	command string
	// requires is a file that must exist in the project root (config based).
	requires []string
}

var developerFormatters = []developerFormatter{
	{extensions: []string{".go"}, command: `command -v gofmt >/dev/null 2>&1 || exit 127; gofmt -w "$FILE"`},
	{extensions: []string{".rs"}, command: `command -v rustfmt >/dev/null 2>&1 || exit 127; rustfmt --edition 2021 "$FILE"`},
	{extensions: []string{".py", ".pyi"}, command: `command -v ruff >/dev/null 2>&1 || exit 127; ruff format -q "$FILE"`},
	{extensions: []string{".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".json", ".css", ".scss", ".md", ".html", ".vue", ".svelte", ".yaml", ".yml"},
		command:  `[ -x node_modules/.bin/prettier ] || exit 127; node_modules/.bin/prettier --log-level silent --write "$FILE"`,
		requires: []string{"node_modules/.bin/prettier"}},
	{extensions: []string{".js", ".jsx", ".ts", ".tsx", ".json"},
		command:  `[ -x node_modules/.bin/biome ] || exit 127; node_modules/.bin/biome format --write "$FILE" >/dev/null`,
		requires: []string{"node_modules/.bin/biome"}},
	{extensions: []string{".sh", ".bash"}, command: `command -v shfmt >/dev/null 2>&1 || exit 127; shfmt -w "$FILE"`},
	{extensions: []string{".tf", ".tfvars"}, command: `command -v terraform >/dev/null 2>&1 || exit 127; terraform fmt "$FILE" >/dev/null`},
	{extensions: []string{".c", ".cc", ".cpp", ".h", ".hpp"}, command: `[ -f .clang-format ] && command -v clang-format >/dev/null 2>&1 || exit 127; clang-format -i "$FILE"`},
}

// formatDeveloperFiles formats each changed file and returns the ones a
// formatter rewrote.
func (s *Server) formatDeveloperFiles(ctx context.Context, h *developerRuntimeHandle, project string, rels []string) []string {
	var formatted []string
	for _, rel := range rels {
		ext := strings.ToLower(path.Ext(rel))
		for _, f := range developerFormatters {
			if !containsString(f.extensions, ext) {
				continue
			}
			fctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			script := `before=$(cksum < "$FILE" 2>/dev/null); ` + f.command + `; rc=$?; [ $rc -eq 0 ] || exit $rc; after=$(cksum < "$FILE" 2>/dev/null); [ "$before" = "$after" ] && exit 0 || exit 10`
			_, _, code, err := s.containerManager.ExecArgs(fctx, h.scope, h.cfg, service.DeveloperAbsolutePath(project), map[string]string{"FILE": service.DeveloperAbsolutePath(developerJoin(project, rel))}, "sh", "-c", script)
			cancel()
			if err != nil || code == 127 {
				continue // not installed: try the next formatter for this type
			}
			if code == 10 {
				formatted = append(formatted, rel)
			}
			break
		}
	}
	return formatted
}

func containsString(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// developerFormatterEnabled reports the space's formatter switch (on unless
// explicitly disabled).
func developerFormatterEnabled(space *service.DeveloperSpace) bool {
	return space == nil || !space.Config.DisableFormatter
}

// ─── Nested instruction files ───
//
// The project-root AGENTS.md goes into the system prompt. Like OpenCode, an
// AGENTS.md (or CLAUDE.md) in a sub-folder is added the first time the agent
// reads or lists inside that folder, nearest first, once per session run.

type developerNestedInstructions struct {
	seen map[string]bool
}

// collect returns the instruction files between rel's folder and the project
// root (exclusive) that were not added yet, nearest first.
func (s *Server) nestedDeveloperInstructions(ctx context.Context, h *developerRuntimeHandle, project, rel string, tracker *developerNestedInstructions, isDir bool) string {
	if tracker == nil {
		return ""
	}
	dir := rel
	if !isDir {
		dir = path.Dir(rel)
	}
	var parts []string
	for dir != "." && dir != "" && dir != "/" {
		if !tracker.seen[dir] {
			tracker.seen[dir] = true
			for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
				raw, err := s.runDeveloperFS(ctx, h, nil, "read", project, dir+"/"+name)
				if err != nil {
					continue
				}
				var file struct {
					Content  string `json:"content"`
					Binary   bool   `json:"binary"`
					TooLarge bool   `json:"too_large"`
				}
				if json.Unmarshal(raw, &file) != nil || file.Binary || file.TooLarge || strings.TrimSpace(file.Content) == "" {
					continue
				}
				content := file.Content
				if len(content) > developerInstructionMaxBytes {
					content = strings.ToValidUTF8(content[:developerInstructionMaxBytes], "") + "\n… [truncated; read the file for the rest]"
				}
				parts = append(parts, fmt.Sprintf("Instructions from %s/%s (they apply to files under %s/):\n%s", dir, name, dir, content))
				break
			}
		}
		dir = path.Dir(dir)
	}
	if len(parts) == 0 {
		return ""
	}
	return "\n\n<system-reminder>\n" + strings.Join(parts, "\n\n") + "\n</system-reminder>"
}
