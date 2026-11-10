package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

const (
	developerReadDefaultLines = 2000
	developerReadMaxLineRunes = 2000

	developerBashDefaultTimeout = 2 * time.Minute
	developerBashMaxTimeout     = 10 * time.Minute
	developerBashOutputMaxBytes = 30000
)

const developerBashDescription = "Run a shell command in the project folder with `bash -c` (falls back to `sh` when bash is missing). " +
	"Pipes, redirection, `&&`, environment variables and globbing work. Each call is a fresh shell: `cd` and exported variables do not carry over, so chain them in one command. " +
	"The default timeout is 2 minutes (pass timeout in milliseconds, up to 10 minutes); a command that runs longer is killed. stdout and stderr are combined, and long output is truncated in the middle. " +
	"Do not use it for reading, searching or editing files: use dev_read_file, dev_glob, dev_search and dev_edit_file instead. " +
	"For servers, watchers or anything that runs long, pass background: true: the command keeps running after the call, and dev_bash_output / dev_bash_kill read and stop it."

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
// argument because builds and test suites routinely need minutes.
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

// developerProjectFilePath validates a tool path relative to the project.
func developerProjectFilePath(raw string) (string, error) {
	clean, err := service.CleanDeveloperPath(raw)
	if err != nil {
		return "", err
	}
	if clean == "" {
		return "", errors.New("path is required")
	}
	return clean, nil
}

func developerSliceArg(args map[string]any, key string) []any {
	value, _ := args[key].([]any)
	return value
}

func developerJoin(project, rel string) string {
	if project == "" {
		return rel
	}
	return project + "/" + rel
}
