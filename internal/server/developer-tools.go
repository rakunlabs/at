package server

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/devfs"
	"github.com/rakunlabs/at/internal/service"
)

const (
	developerReadDefaultLines = 2000
	developerReadMaxLineRunes = 2000

	developerBashDefaultTimeout = 2 * time.Minute
	developerBashMaxTimeout     = 10 * time.Minute
	developerBashOutputMaxBytes = 30000

	// developerInstructionMaxBytes bounds each instruction file pasted into
	// the system prompt, so a huge AGENTS.md cannot take the whole window.
	developerInstructionMaxBytes = 32 << 10
)

const developerBashDescription = "Run a shell command in the project folder with `bash -c` (falls back to `sh` when bash is missing). " +
	"Pipes, redirection, `&&`, environment variables and globbing work. Each call is a fresh shell: `cd` and exported variables do not carry over, so chain them in one command. " +
	"The default timeout is 2 minutes (pass timeout in milliseconds, up to 10 minutes); a command that runs longer is killed. stdout and stderr are combined, and long output is truncated in the middle. " +
	"Do not use it for reading, searching or editing files: use read_file, glob, search and edit_file instead. " +
	"For servers, watchers or anything that runs long, pass background: true: the command keeps running after the call, and bash_output / bash_kill read and stop it."

// developerNumberedLines renders content like `cat -n`: a right-aligned line
// number, a tab, then the line. offset is 1-based.
func developerNumberedLines(content string, offset, limit int) string {
	if content == "" {
		return "(empty file)"
	}
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	if offset < 1 {
		offset = 1
	}
	if limit <= 0 || limit > developerReadDefaultLines*5 {
		limit = developerReadDefaultLines
	}
	if offset > len(lines) {
		return fmt.Sprintf("(offset %d is past the end of the file, which has %d lines)", offset, len(lines))
	}
	end := min(len(lines), offset-1+limit)
	var b strings.Builder
	for i := offset - 1; i < end; i++ {
		line := strings.TrimSuffix(lines[i], "\r")
		if runes := []rune(line); len(runes) > developerReadMaxLineRunes {
			line = string(runes[:developerReadMaxLineRunes]) + "… [line truncated]"
		}
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, line)
	}
	if end < len(lines) {
		fmt.Fprintf(&b, "(showing lines %d-%d of %d; pass offset %d to continue)\n", offset, end, len(lines), end+1)
	}
	return b.String()
}

// developerGlobRegexp compiles a doublestar glob. `**` crosses directories,
// `*` and `?` stay within one segment, `{a,b}` is alternation and `[...]` a
// character class. A pattern without a slash matches the base name at any
// depth, as in ripgrep/OpenCode.
func developerGlobRegexp(pattern string) (*regexp.Regexp, error) {
	pattern = strings.TrimPrefix(strings.TrimSpace(pattern), "./")
	if pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}
	if !strings.Contains(pattern, "/") {
		pattern = "**/" + pattern
	}
	var b strings.Builder
	b.WriteString("^")
	depth := 0
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch {
		case c == '*' && i+1 < len(pattern) && pattern[i+1] == '*':
			i++
			if i+1 < len(pattern) && pattern[i+1] == '/' {
				i++
				b.WriteString("(?:.*/)?")
			} else {
				b.WriteString(".*")
			}
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '{':
			depth++
			b.WriteString("(?:")
		case c == '}' && depth > 0:
			depth--
			b.WriteString(")")
		case c == ',' && depth > 0:
			b.WriteString("|")
		case c == '[':
			end := strings.IndexByte(pattern[i+1:], ']')
			if end < 0 {
				return nil, fmt.Errorf("invalid glob: unclosed [")
			}
			class := pattern[i+1 : i+1+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + strings.ReplaceAll(class, `\`, `\\`) + "]")
			i += end + 1
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("invalid glob: unclosed {")
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

func (s *Server) readDeveloperFileNumbered(ctx context.Context, h *developerRuntimeHandle, project, rel string, offset, limit int) (string, error) {
	raw, err := s.runDeveloperFS(ctx, h, nil, "read", project, rel)
	if err != nil {
		return "", err
	}
	var file struct {
		Content  string `json:"content"`
		Binary   bool   `json:"binary"`
		TooLarge bool   `json:"too_large"`
		Size     int64  `json:"size"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return "", err
	}
	if file.Binary {
		return "", fmt.Errorf("%s is a binary file (%d bytes)", rel, file.Size)
	}
	if file.TooLarge {
		return "", fmt.Errorf("%s is %d bytes, above the 2 MiB read limit; use search or bash (sed -n, head, tail) to inspect it", rel, file.Size)
	}
	return developerNumberedLines(file.Content, offset, limit), nil
}

// runDeveloperBash runs one shell command. The timeout is the tool's own
// argument, not the profile's tool timeout, because builds and test suites
// routinely need minutes.
func (s *Server) runDeveloperBash(ctx context.Context, h *developerRuntimeHandle, project string, args map[string]any) (string, error) {
	command := stringArg(args, "command")
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("command is required")
	}
	timeout := developerBashDefaultTimeout
	if ms := intArg(args["timeout"], 0); ms > 0 {
		timeout = min(time.Duration(ms)*time.Millisecond, developerBashMaxTimeout)
	}
	// Leave the process a moment to be killed by `timeout` itself, so the
	// report says "timed out" instead of the exec failing on our deadline.
	ctx, cancel := context.WithTimeout(ctx, timeout+10*time.Second)
	defer cancel()
	seconds := max(1, int(timeout/time.Second))
	// `exec 2>&1` interleaves stderr with stdout in the order it was written.
	// timeout(1) is coreutils/busybox; without it the outer deadline applies.
	script := `if command -v bash >/dev/null 2>&1; then sh=bash; else sh=sh; fi
exec 2>&1
if command -v timeout >/dev/null 2>&1; then exec timeout -s KILL "$AT_TIMEOUT" "$sh" -c "$AT_COMMAND"; fi
exec "$sh" -c "$AT_COMMAND"`
	env := map[string]string{"AT_COMMAND": command, "AT_TIMEOUT": fmt.Sprint(seconds)}
	stdout, stderr, code, err := s.containerManager.ExecArgs(ctx, h.scope, h.cfg, service.DeveloperAbsolutePath(project), env, "sh", "-c", script)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("command timed out after %s", timeout)
		}
		return "", err
	}
	output := developerClipOutput(stdout + stderr)
	switch {
	case code == 137 && seconds > 0:
		return output, fmt.Errorf("command was killed after the %s timeout (pass a larger timeout if it needs longer)", timeout)
	case code != 0:
		return output, fmt.Errorf("command exited with code %d", code)
	}
	if strings.TrimSpace(output) == "" {
		return "(no output)", nil
	}
	return output, nil
}

// developerClipOutput keeps the start and end of long output, where a build or
// test runner prints its command line and its verdict.
func developerClipOutput(output string) string {
	if len(output) <= developerBashOutputMaxBytes {
		return output
	}
	head, tail := developerBashOutputMaxBytes/2, developerBashOutputMaxBytes/2
	omitted := len(output) - head - tail
	return strings.ToValidUTF8(output[:head], "") + fmt.Sprintf("\n\n… [%d bytes omitted] …\n\n", omitted) + strings.ToValidUTF8(output[len(output)-tail:], "")
}

func (s *Server) writeDeveloperTodos(ctx context.Context, h *developerRuntimeHandle, session *service.DeveloperSession, args map[string]any) (string, error) {
	raw, err := json.Marshal(args["todos"])
	if err != nil {
		return "", fmt.Errorf("todos must be an array")
	}
	var todos []service.DeveloperSessionTodo
	if err := json.Unmarshal(raw, &todos); err != nil {
		return "", fmt.Errorf("todos must be an array of {content, status, priority}")
	}
	updated, err := h.store.SetDeveloperSessionTodos(ctx, session.ID, todos)
	if err != nil {
		return "", err
	}
	return developerTodoText(updated.Todos), nil
}

func developerTodoText(todos []service.DeveloperSessionTodo) string {
	if len(todos) == 0 {
		return "The task list is empty."
	}
	marks := map[string]string{"pending": "[ ]", "in_progress": "[~]", "completed": "[x]", "cancelled": "[-]"}
	var b strings.Builder
	open := 0
	for _, todo := range todos {
		if todo.Status == "pending" || todo.Status == "in_progress" {
			open++
		}
		fmt.Fprintf(&b, "%s %s", marks[todo.Status], todo.Content)
		if todo.Priority != "" {
			fmt.Fprintf(&b, " (%s)", todo.Priority)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "%d of %d open.", open, len(todos))
	return b.String()
}

// developerInstructionFiles are read from the project root, in this order,
// like OpenCode's AGENTS.md / CLAUDE.md rules. CLAUDE.md is a fallback for
// repositories prepared for Claude Code, so it is skipped when AGENTS.md
// exists.
var developerInstructionFiles = []string{"AGENTS.md", "CLAUDE.md", "CONTEXT.md"}

// developerInstructions loads the project's instruction files for the system
// prompt. Missing files are normal; a failure to read is not fatal either,
// because a run must not depend on optional context.
func (s *Server) developerInstructions(ctx context.Context, h *developerRuntimeHandle, project string) string {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var parts []string
	for _, name := range developerInstructionFiles {
		if name == "CLAUDE.md" && len(parts) > 0 {
			continue
		}
		raw, err := s.runDeveloperFS(ctx, h, nil, "read", project, name)
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
		parts = append(parts, fmt.Sprintf("Instructions from %s:\n%s", name, content))
	}
	return strings.Join(parts, "\n\n")
}

// ─── Undo snapshots ───
//
// Undo needs the files as they were before a prompt ran, including files the
// project's own Git does not track. Like OpenCode, a separate "shadow" Git
// repository records them: its git-dir lives in the space volume under
// .at-snapshots/<project>, its work tree is the project, and the project's
// own .git (index, refs, history) is never touched. A snapshot is the tree
// hash of `git add -A && git write-tree`; reverting checks that tree out and
// removes files it does not contain. The project's .gitignore applies, so
// node_modules and build output are neither recorded nor deleted.

func developerSnapshotGitDir(project string) string {
	name := project
	if name == "" {
		name = "_root"
	}
	return service.DeveloperWorkspaceRoot + "/" + devfs.SnapshotDir + "/" + strings.ReplaceAll(name, "/", "__") + ".git"
}

func (s *Server) developerSnapshotGit(ctx context.Context, h *developerRuntimeHandle, project string, args ...string) (string, string, int, error) {
	workTree := service.DeveloperAbsolutePath(project)
	env := map[string]string{"GIT_DIR": developerSnapshotGitDir(project), "GIT_WORK_TREE": workTree}
	base := []string{"-c", "core.autocrlf=false", "-c", "core.longpaths=true", "-c", "core.symlinks=true", "-c", "core.fsmonitor=false", "-c", "safe.directory=*"}
	return s.containerManager.ExecArgs(ctx, h.scope, h.cfg, workTree, env, "git", append(base, args...)...)
}

// takeDeveloperSnapshot records the project's current files and returns the
// tree hash, or "" when the space has no git (undo is then unavailable).
func (s *Server) takeDeveloperSnapshot(ctx context.Context, h *developerRuntimeHandle, project string) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	gitDir := developerSnapshotGitDir(project)
	if _, _, code, err := s.developerSnapshotGit(ctx, h, project, "rev-parse", "--git-dir"); err != nil || code != 0 {
		if _, _, code, err := s.containerManager.ExecArgs(ctx, h.scope, h.cfg, service.DeveloperWorkspaceRoot, nil, "git", "init", "--quiet", "--bare", gitDir); err != nil || code != 0 {
			return ""
		}
		// The project's own excludes and .gitignore files apply automatically;
		// the snapshot store itself must never be recorded.
		_, _, _, _ = s.containerManager.ExecArgs(ctx, h.scope, h.cfg, service.DeveloperWorkspaceRoot, nil, "sh", "-c", `mkdir -p "$1/info" && printf '%s\n' /.git/ '/'"$2"/ >> "$1/info/exclude"`, "sh", gitDir, devfs.SnapshotDir)
	}
	if _, _, code, err := s.developerSnapshotGit(ctx, h, project, "add", "--all", "--", "."); err != nil || (code != 0 && code != 1) {
		return ""
	}
	tree, _, code, err := s.developerSnapshotGit(ctx, h, project, "write-tree")
	if err != nil || code != 0 {
		return ""
	}
	return strings.TrimSpace(tree)
}

var developerTreeHash = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// restoreDeveloperSnapshot returns the project's files to tree: files in the
// tree are written back and files created since are removed. Ignored files
// are left alone.
func (s *Server) restoreDeveloperSnapshot(ctx context.Context, h *developerRuntimeHandle, project, tree string) ([]string, error) {
	if !developerTreeHash.MatchString(tree) {
		return nil, fmt.Errorf("invalid snapshot")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	current := s.takeDeveloperSnapshot(ctx, h, project)
	if current == "" {
		return nil, fmt.Errorf("could not record the current files; is git installed in the space?")
	}
	changed, _, _, _ := s.developerSnapshotGit(ctx, h, project, "diff", "--name-only", "-z", current, tree)
	if _, stderr, code, err := s.developerSnapshotGit(ctx, h, project, "read-tree", "--reset", "-u", tree); err != nil || code != 0 {
		return nil, developerCommandFailure("restore snapshot", stderr, err)
	}
	// read-tree -u removes files that were in the index but are not in the
	// target tree, and the index held `current`, so new files are gone too.
	var paths []string
	for _, name := range strings.Split(strings.TrimSuffix(changed, "\x00"), "\x00") {
		if name != "" {
			paths = append(paths, developerJoin(project, name))
		}
	}
	return paths, nil
}
