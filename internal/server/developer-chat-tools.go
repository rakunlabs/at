package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// Developer Space tools run inside the caller's own space container. They are
// the coding agent's hands, offered to Chats as one tool source: the space is
// resolved from the run's identity (never an argument), every path is kept
// inside /workspace by at-devfs, and the selected project scopes relative
// paths. Developer Spaces itself keeps no chat; a turn that edits a project is
// an ordinary Chats turn with these tools selected.

const (
	developerToolTimeout = 60 * time.Second
	// developerDiffMaxBytes bounds the diff a write reports back to the page,
	// so one large edit cannot fill the transcript.
	developerDiffMaxBytes = 32 << 10
)

// developerToolShell names the tools that run arbitrary commands. Chats asks
// before each call unless the reader allowed that command for the
// conversation; background runs cannot ask, so they do not offer them.
var developerToolShell = map[string]bool{"dev_bash": true, "dev_bash_kill": true}

// developerToolEdits are the tools that change files. They run without asking
// and report a diff, which is what the reader reviews instead.
var developerToolEdits = map[string]bool{"dev_write_file": true, "dev_edit_file": true, "dev_patch": true}

func developerProjectProperty() map[string]any {
	return map[string]any{"type": "string", "description": "Project folder in /workspace (a top-level folder name). Omit to use the conversation's project; empty string addresses /workspace itself."}
}

func developerToolSchema(properties map[string]any, required ...string) map[string]any {
	properties["project"] = developerProjectProperty()
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// developerToolDefs are advertised to Chats when the conversation enables the
// Developer Space source. Names carry a dev_ prefix so they never collide with
// built-ins (file_read, bash_execute) that run on the AT host.
var developerToolDefs = []service.Tool{
	{Name: "dev_projects", Description: "List the projects (top-level folders) in your Developer Space with their Git branch and status. Use it to find the project to work on.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
	{Name: "dev_clone", Description: "Clone a Git repository (https, ssh, git or git@host:path, no credentials in the URL) into a new project folder in your Developer Space. SSH keys and Git credentials come from the space's persistent home. Commits made later use your account's name and verified email unless the repository configures its own.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{
		"remote": map[string]any{"type": "string"},
		"name":   map[string]any{"type": "string", "description": "Folder name; defaults to the repository name"},
		"branch": map[string]any{"type": "string"},
	}, "required": []string{"remote"}}},
	{Name: "dev_read_file", Description: "Read a UTF-8 file in the project. Output is numbered like `cat -n` (line number, tab, text); the numbers are not part of the file. Reads up to 2000 lines from offset.", InputSchema: developerToolSchema(map[string]any{
		"path":   map[string]any{"type": "string", "description": "Path relative to the project"},
		"offset": map[string]any{"type": "integer", "description": "1-based line to start at (default 1)"},
		"limit":  map[string]any{"type": "integer", "description": "Number of lines (default 2000)"},
	}, "path")},
	{Name: "dev_glob", Description: "Find files by name pattern, e.g. `**/*.md` or `docs/*.go`. A pattern without a slash matches the file name at any depth. .git and node_modules are skipped.", InputSchema: developerToolSchema(map[string]any{
		"pattern": map[string]any{"type": "string"},
		"path":    map[string]any{"type": "string", "description": "Optional sub-folder"},
	}, "pattern")},
	{Name: "dev_search", Description: "Search file contents with a regular expression (RE2). Returns path:line:text. Use it to look things up across a knowledge base or code base.", InputSchema: developerToolSchema(map[string]any{
		"pattern": map[string]any{"type": "string"},
		"path":    map[string]any{"type": "string", "description": "Optional sub-folder or file"},
		"include": map[string]any{"type": "string", "description": "Optional file-name pattern(s), comma separated, e.g. `*.md`"},
	}, "pattern")},
	{Name: "dev_write_file", Description: "Create or replace a UTF-8 file in the project with the full new content.", InputSchema: developerToolSchema(map[string]any{
		"path":    map[string]any{"type": "string"},
		"content": map[string]any{"type": "string"},
	}, "path", "content")},
	{Name: "dev_edit_file", Description: "Replace old_text with new_text in a project file. old_text must match exactly (never include dev_read_file's line-number prefix) and be unique unless replace_all is true.", InputSchema: developerToolSchema(map[string]any{
		"path":        map[string]any{"type": "string"},
		"old_text":    map[string]any{"type": "string"},
		"new_text":    map[string]any{"type": "string"},
		"replace_all": map[string]any{"type": "boolean"},
	}, "path", "old_text", "new_text")},
	{Name: "dev_patch", Description: developerPatchDescription, InputSchema: developerToolSchema(map[string]any{
		"patch": map[string]any{"type": "string", "description": "The full patch text, from *** Begin Patch to *** End Patch"},
	}, "patch")},
	{Name: "dev_git_status", Description: "Read the project's Git status (porcelain v2 with branch).", InputSchema: developerToolSchema(map[string]any{})},
	{Name: "dev_git_diff", Description: "Read the project's Git diff.", InputSchema: developerToolSchema(map[string]any{
		"staged": map[string]any{"type": "boolean"},
		"path":   map[string]any{"type": "string", "description": "Optional file"},
	})},
	{Name: "dev_git_commit", Description: "Stage the given paths (or everything with all: true) and commit them in the project. The author is your account's name and verified email unless the repository sets user.name/user.email. Pushing is not a tool: ask the user to push from Developer Spaces.", InputSchema: developerToolSchema(map[string]any{
		"message": map[string]any{"type": "string"},
		"paths":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"all":     map[string]any{"type": "boolean"},
	}, "message")},
	{Name: "dev_bash", Description: developerBashDescription + " The user approves each command before it runs.", InputSchema: developerToolSchema(map[string]any{
		"command":     map[string]any{"type": "string"},
		"timeout":     map[string]any{"type": "integer", "description": "Optional timeout in milliseconds (default 120000, max 600000)"},
		"background":  map[string]any{"type": "boolean"},
		"description": map[string]any{"type": "string", "description": "Five to ten words saying what the command does"},
	}, "command")},
	{Name: "dev_bash_output", Description: "Read the output of a background dev_bash job and whether it is still running. Without job_id, list the jobs.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{
		"job_id": map[string]any{"type": "string"},
		"offset": map[string]any{"type": "integer"},
	}}},
	{Name: "dev_bash_kill", Description: "Stop a background dev_bash job.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"job_id": map[string]any{"type": "string"}}, "required": []string{"job_id"}}},
}

func isDeveloperTool(name string) bool {
	for _, tool := range developerToolDefs {
		if tool.Name == name {
			return true
		}
	}
	return false
}

// developerToolResult is what one call reports. Output goes to the model;
// Changed and Diff are for the page, which shows the diff under the call and
// links the files to the Developer Spaces editor.
type developerToolResult struct {
	Output  string   `json:"output"`
	Error   string   `json:"error,omitempty"`
	Project string   `json:"project,omitempty"`
	Changed []string `json:"changed,omitempty"`
	Diff    string   `json:"diff,omitempty"`
	// DiffTruncated is set when Diff was clipped at developerDiffMaxBytes.
	DiffTruncated bool `json:"diff_truncated,omitempty"`
}

// developerToolProject resolves the project argument: an explicit value wins,
// otherwise the conversation's project. Projects are top-level folders.
func developerToolProject(args map[string]any, fallback string) (string, error) {
	raw, explicit := args["project"].(string)
	if !explicit {
		raw = fallback
	}
	clean, err := service.CleanDeveloperPath(raw)
	if err != nil {
		return "", fmt.Errorf("project: %w", err)
	}
	if strings.Contains(clean, "/") {
		return "", errors.New("project must be a top-level folder of /workspace")
	}
	return clean, nil
}

// executeDeveloperTool runs one Developer Space tool for the bound account.
// The space must already be running: tools never start it, matching the page.
func (s *Server) executeDeveloperTool(ctx context.Context, name string, args map[string]any, defaultProject string) (developerToolResult, error) {
	if !isDeveloperTool(name) {
		return developerToolResult{}, fmt.Errorf("unknown Developer Space tool %q", name)
	}
	if enabled, err := s.isFeatureEnabled(ctx, service.FeatureDeveloperSpaces); err != nil {
		return developerToolResult{}, err
	} else if !enabled {
		return developerToolResult{}, errors.New("the Developer Spaces feature is disabled in this installation")
	}
	h, ctx, err := s.openDeveloperRuntime(ctx, false)
	if err != nil {
		if errors.Is(err, errDeveloperSpaceStopped) {
			return developerToolResult{}, errors.New("your Developer Space is stopped; ask the user to start it on the Developer Spaces page")
		}
		return developerToolResult{}, err
	}
	if h.space.Status != service.DeveloperSpaceReady {
		return developerToolResult{}, errors.New("your Developer Space is not running; ask the user to press Start on the Developer Spaces page")
	}
	project, err := developerToolProject(args, defaultProject)
	if err != nil {
		return developerToolResult{}, err
	}
	result := developerToolResult{Project: project}
	switch name {
	case "dev_projects":
		result.Project = ""
		result.Output, err = s.developerProjects(ctx, h)
	case "dev_clone":
		result.Output, result.Project, err = s.developerClone(ctx, h, args)
	case "dev_patch":
		pctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		var changed []string
		result.Output, changed, err = s.applyDeveloperPatch(pctx, h, project, stringArg(args, "patch"))
		cancel()
		result.Changed = changed
	case "dev_git_commit":
		result.Output, err = s.developerCommit(ctx, h, project, args)
	default:
		result.Output, err = s.runDeveloperContainerTool(ctx, h, project, name, args)
	}
	if err != nil {
		result.Error = err.Error()
	}
	if err == nil && (name == "dev_write_file" || name == "dev_edit_file") {
		if rel, relErr := developerProjectFilePath(stringArg(args, "path")); relErr == nil {
			result.Changed = []string{rel}
		}
	}
	if len(result.Changed) > 0 {
		if developerFormatterEnabled(h.space) {
			if formatted := s.formatDeveloperFiles(ctx, h, project, result.Changed); len(formatted) > 0 {
				result.Output += "\n(formatted: " + strings.Join(formatted, ", ") + "; read the file again before editing it further)"
			}
		}
		result.Diff, result.DiffTruncated = s.developerChangedDiff(ctx, h, project, result.Changed)
	}
	return result, nil
}

// runDeveloperContainerTool covers the file, search, Git read and shell tools.
func (s *Server) runDeveloperContainerTool(ctx context.Context, h *developerRuntimeHandle, project, name string, args map[string]any) (string, error) {
	switch name {
	case "dev_bash":
		if boolArg(args, "background") {
			command := stringArg(args, "command")
			if strings.TrimSpace(command) == "" {
				return "", errors.New("command is required")
			}
			return s.startDeveloperBackgroundJob(ctx, h, project, command)
		}
		return s.runDeveloperBash(ctx, h, project, args)
	case "dev_bash_output":
		return s.developerJobOutput(ctx, h, args)
	case "dev_bash_kill":
		return s.killDeveloperJob(ctx, h, args)
	}
	ctx, cancel := context.WithTimeout(ctx, developerToolTimeout)
	defer cancel()
	switch name {
	case "dev_read_file":
		rel, err := developerProjectFilePath(stringArg(args, "path"))
		if err != nil {
			return "", err
		}
		return s.readDeveloperFileNumbered(ctx, h, project, rel, intArg(args["offset"], 1), intArg(args["limit"], developerReadDefaultLines))
	case "dev_glob":
		return s.developerGlob(ctx, h, project, args)
	case "dev_search":
		return s.developerSearch(ctx, h, project, args)
	case "dev_write_file":
		rel, err := developerProjectFilePath(stringArg(args, "path"))
		if err != nil {
			return "", err
		}
		content := stringArg(args, "content")
		if len(content) > developerWriteMaxBytes {
			return "", errors.New("content exceeds 2 MiB")
		}
		if _, err := s.runDeveloperFS(ctx, h, strings.NewReader(content), "write", project, rel, ""); err != nil {
			return "", err
		}
		return fmt.Sprintf("wrote %s (%d bytes)", rel, len(content)), nil
	case "dev_edit_file":
		return s.developerEdit(ctx, h, project, args)
	case "dev_git_status":
		return s.developerGit(ctx, h, project, "status", "--porcelain=v2", "--branch")
	case "dev_git_diff":
		gitArgs := []string{"diff", "--no-ext-diff"}
		if boolArg(args, "staged") {
			gitArgs = append(gitArgs, "--cached")
		}
		gitArgs = append(gitArgs, "--")
		if file := stringArg(args, "path"); file != "" {
			rel, err := developerProjectFilePath(file)
			if err != nil {
				return "", err
			}
			gitArgs = append(gitArgs, rel)
		}
		out, err := s.developerGit(ctx, h, project, gitArgs...)
		if err == nil && strings.TrimSpace(out) == "" {
			return "no changes", nil
		}
		return out, err
	}
	return "", fmt.Errorf("unknown Developer Space tool %q", name)
}

func (s *Server) developerGit(ctx context.Context, h *developerRuntimeHandle, project string, args ...string) (string, error) {
	stdout, stderr, code, err := h.exec(ctx, s, service.DeveloperAbsolutePath(project), "git", args...)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return stdout, developerCommandFailure("git "+args[0], stderr, nil)
	}
	return stdout, nil
}

func (s *Server) developerProjects(ctx context.Context, h *developerRuntimeHandle) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, developerToolTimeout)
	defer cancel()
	raw, err := s.runDeveloperFS(ctx, h, nil, "list", "", "")
	if err != nil {
		return "", err
	}
	var listing struct {
		Entries []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &listing); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, entry := range listing.Entries {
		if entry.Type != "dir" || strings.HasPrefix(entry.Name, ".") {
			continue
		}
		dir := service.DeveloperAbsolutePath(entry.Name)
		branch, _, code, _ := h.exec(ctx, s, dir, "git", "rev-parse", "--abbrev-ref", "HEAD")
		if code != 0 {
			fmt.Fprintf(&b, "%s\t(not a Git repository)\n", entry.Name)
			continue
		}
		status, _, _, _ := h.exec(ctx, s, dir, "git", "status", "--porcelain")
		changes := 0
		if trimmed := strings.TrimSpace(status); trimmed != "" {
			changes = len(strings.Split(trimmed, "\n"))
		}
		fmt.Fprintf(&b, "%s\tbranch %s, %d changed files\n", entry.Name, strings.TrimSpace(branch), changes)
	}
	if b.Len() == 0 {
		return "The space has no projects yet. Use dev_clone to clone a repository.", nil
	}
	return b.String(), nil
}

func (s *Server) developerClone(ctx context.Context, h *developerRuntimeHandle, args map[string]any) (string, string, error) {
	remote := strings.TrimSpace(stringArg(args, "remote"))
	if err := service.ValidDeveloperRemote(remote); err != nil {
		return "", "", err
	}
	name := strings.TrimSpace(stringArg(args, "name"))
	if name == "" {
		name = strings.TrimSuffix(path.Base(strings.TrimSuffix(strings.ReplaceAll(remote, ":", "/"), "/")), ".git")
	}
	if clean, err := service.CleanDeveloperPath(name); err != nil || clean == "" || strings.Contains(clean, "/") || strings.HasPrefix(clean, "-") || strings.HasPrefix(clean, ".") {
		return "", "", errors.New("project name must be a single folder name")
	}
	branch := strings.TrimSpace(stringArg(args, "branch"))
	if branch != "" && !service.ValidDeveloperBranch(branch) {
		return "", "", errors.New("branch is invalid")
	}
	if err := developerRemoteAllowed(ctx, remote); err != nil {
		return "", "", err
	}
	gitArgs := []string{"clone", "--origin", "origin"}
	if branch != "" {
		gitArgs = append(gitArgs, "--branch", branch)
	}
	gitArgs = append(gitArgs, "--", remote, service.DeveloperAbsolutePath(name))
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if _, stderr, code, err := h.exec(ctx, s, service.DeveloperWorkspaceRoot, "git", gitArgs...); err != nil || code != 0 {
		return "", "", developerCommandFailure("git clone", stderr, err)
	}
	return fmt.Sprintf("Cloned %s into project %q. Pass project %q to the other dev_ tools.", remote, name, name), name, nil
}

func (s *Server) developerGlob(ctx context.Context, h *developerRuntimeHandle, project string, args map[string]any) (string, error) {
	start := ""
	if candidate := stringArg(args, "path"); candidate != "" {
		clean, err := service.CleanDeveloperPath(candidate)
		if err != nil {
			return "", err
		}
		start = clean
	}
	pattern := strings.TrimSpace(stringArg(args, "pattern"))
	if pattern == "" {
		return "", errors.New("pattern is required")
	}
	rx, err := developerGlobRegexp(pattern)
	if err != nil {
		return "", err
	}
	raw, err := s.runDeveloperFS(ctx, h, nil, "walk", project, start)
	if err != nil {
		return "", err
	}
	var listing struct {
		Files     []string `json:"files"`
		Truncated bool     `json:"truncated"`
	}
	if err := json.Unmarshal(raw, &listing); err != nil {
		return "", err
	}
	var files []string
	for _, file := range listing.Files {
		rel := file
		if start != "" {
			rel = strings.TrimPrefix(strings.TrimPrefix(file, start), "/")
		}
		if rx.MatchString(rel) {
			files = append(files, file)
		}
	}
	if len(files) == 0 {
		return "no files found", nil
	}
	const shown = 1000
	out := strings.Join(files[:min(len(files), shown)], "\n")
	if len(files) > shown {
		out += fmt.Sprintf("\n[%d more files; narrow the pattern or path]", len(files)-shown)
	}
	if listing.Truncated {
		out += "\n[the walk stopped at 10000 files; narrow the path]"
	}
	return out, nil
}

func (s *Server) developerSearch(ctx context.Context, h *developerRuntimeHandle, project string, args map[string]any) (string, error) {
	pattern := stringArg(args, "pattern")
	if pattern == "" {
		return "", errors.New("pattern is required")
	}
	start := ""
	if candidate := stringArg(args, "path"); candidate != "" {
		clean, err := developerProjectFilePath(candidate)
		if err != nil {
			return "", err
		}
		start = clean
	}
	var includes []string
	for _, item := range strings.Split(stringArg(args, "include"), ",") {
		if item = strings.TrimSpace(item); item != "" {
			// The helper matches base names; `**/*.go` means the same here.
			includes = append(includes, item[strings.LastIndex(item, "/")+1:])
		}
	}
	raw, err := s.runDeveloperFS(ctx, h, nil, "search", project, pattern, start, strings.Join(includes, "\n"))
	if err != nil {
		return "", err
	}
	var result struct {
		Matches []struct {
			Path string `json:"path"`
			Line int    `json:"line"`
			Text string `json:"text"`
		} `json:"matches"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, match := range result.Matches {
		fmt.Fprintf(&b, "%s:%d:%s\n", match.Path, match.Line, match.Text)
	}
	if result.Truncated {
		b.WriteString("[results truncated at 2000 matches]\n")
	}
	if b.Len() == 0 {
		return "no matches", nil
	}
	return b.String(), nil
}

func (s *Server) developerEdit(ctx context.Context, h *developerRuntimeHandle, project string, args map[string]any) (string, error) {
	rel, err := developerProjectFilePath(stringArg(args, "path"))
	if err != nil {
		return "", err
	}
	oldText, newText := stringArg(args, "old_text"), stringArg(args, "new_text")
	if oldText == "" {
		return "", errors.New("old_text is required; use dev_write_file to create a file")
	}
	if oldText == newText {
		return "", errors.New("old_text and new_text are identical")
	}
	raw, err := s.runDeveloperFS(ctx, h, nil, "read", project, rel)
	if err != nil {
		return "", err
	}
	var file struct {
		Content string `json:"content"`
		Version string `json:"version"`
		Binary  bool   `json:"binary"`
	}
	if err := json.Unmarshal(raw, &file); err != nil || file.Binary {
		return "", fmt.Errorf("%s is not an editable text file", rel)
	}
	count := strings.Count(file.Content, oldText)
	replaceAll := boolArg(args, "replace_all")
	switch {
	case count == 0:
		return "", fmt.Errorf("old_text was not found in %s; read the file again and copy the exact text without line-number prefixes", rel)
	case count > 1 && !replaceAll:
		return "", fmt.Errorf("old_text occurs %d times in %s; include more surrounding lines so it is unique, or set replace_all", count, rel)
	}
	n := 1
	if replaceAll {
		n = -1
	}
	updated := strings.Replace(file.Content, oldText, newText, n)
	if len(updated) > developerWriteMaxBytes {
		return "", errors.New("result exceeds 2 MiB")
	}
	if _, err := s.runDeveloperFS(ctx, h, strings.NewReader(updated), "write", project, rel, file.Version); err != nil {
		return "", err
	}
	if replaceAll && count > 1 {
		return fmt.Sprintf("edited %s (%d replacements)", rel, count), nil
	}
	return "edited " + rel, nil
}

// developerCommit stages and commits. A repository without user.name /
// user.email commits as the run's account: the identity comes from the bound
// execution identity and the stored account, never from arguments, so a
// conversation cannot make a commit claim to be somebody else.
func (s *Server) developerCommit(ctx context.Context, h *developerRuntimeHandle, project string, args map[string]any) (string, error) {
	message := strings.TrimSpace(stringArg(args, "message"))
	if message == "" || len(message) > 10000 {
		return "", errors.New("message is required and must not exceed 10000 bytes")
	}
	var paths []string
	if boolArg(args, "all") {
		paths = []string{"."}
	} else {
		for _, item := range developerSliceArg(args, "paths") {
			value, _ := item.(string)
			rel, err := developerProjectFilePath(value)
			if err != nil {
				return "", fmt.Errorf("paths: %w", err)
			}
			paths = append(paths, rel)
		}
	}
	if len(paths) == 0 || len(paths) > 500 {
		return "", errors.New("pass 1 to 500 paths, or all: true")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	dir := service.DeveloperAbsolutePath(project)
	if _, err := s.developerGit(ctx, h, project, append([]string{"add", "--"}, paths...)...); err != nil {
		return "", err
	}
	env := s.developerCommitIdentity(ctx, h, dir)
	stdout, stderr, code, err := s.containerManager.ExecArgs(ctx, h.scope, h.cfg, dir, env, "git", "commit", "-m", message)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return stdout, developerCommandFailure("git commit", stderr+stdout, nil)
	}
	head, _, _, _ := h.exec(ctx, s, dir, "git", "log", "-1", "--format=%h %an <%ae>")
	return "Committed " + strings.TrimSpace(head) + ". Ask the user to push it from the Developer Spaces page.", nil
}

// developerCommitIdentity returns GIT_AUTHOR_* / GIT_COMMITTER_* for the run's
// account when the repository (or the space's .gitconfig) names nobody.
func (s *Server) developerCommitIdentity(ctx context.Context, h *developerRuntimeHandle, dir string) map[string]string {
	configured := func(key string) bool {
		out, _, code, _ := h.exec(ctx, s, dir, "git", "config", "--get", key)
		return code == 0 && strings.TrimSpace(out) != ""
	}
	if configured("user.name") && configured("user.email") {
		return nil
	}
	raw, err := s.execWhoami(ctx, nil)
	if err != nil {
		return nil
	}
	var who whoamiResult
	if json.Unmarshal([]byte(raw), &who) != nil {
		return nil
	}
	name := strings.TrimSpace(who.Name)
	if name == "" {
		name = who.Username
	}
	email := strings.TrimSpace(who.Email)
	if name == "" || email == "" {
		return nil
	}
	return map[string]string{
		"GIT_AUTHOR_NAME": name, "GIT_AUTHOR_EMAIL": email,
		"GIT_COMMITTER_NAME": name, "GIT_COMMITTER_EMAIL": email,
	}
}

// developerChangedDiff reports the edit against the project's HEAD, or the
// whole new file when the project is not a repository or the file is new.
func (s *Server) developerChangedDiff(ctx context.Context, h *developerRuntimeHandle, project string, files []string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	dir := service.DeveloperAbsolutePath(project)
	var b strings.Builder
	for _, rel := range files {
		out, _, code, err := h.exec(ctx, s, dir, "git", "diff", "--no-ext-diff", "HEAD", "--", rel)
		if err != nil || code != 0 || strings.TrimSpace(out) == "" {
			// Untracked or no repository: show the file as added.
			out, _, code, err = h.exec(ctx, s, dir, "git", "diff", "--no-ext-diff", "--no-index", "--", "/dev/null", rel)
			if err != nil || (code != 0 && code != 1) {
				continue
			}
		}
		b.WriteString(out)
		if b.Len() > developerDiffMaxBytes {
			return strings.ToValidUTF8(b.String()[:developerDiffMaxBytes], ""), true
		}
	}
	return b.String(), false
}

// developerToolResultText is the tool result Chats shows the model. Errors
// read as errors to the page's renderer and to the model.
func developerToolResultText(result developerToolResult) string {
	if result.Error != "" {
		text := "Error: " + result.Error
		if strings.TrimSpace(result.Output) != "" {
			text += "\n" + result.Output
		}
		return text
	}
	if strings.TrimSpace(result.Output) == "" {
		return "(no output)"
	}
	return result.Output
}

// developerToolsPrompt tells the model where the dev_* tools work. The Chats
// page sends the same text (helper/chat-developer.ts); background runs, which
// cannot ask the reader, say that commands are unavailable.
func developerToolsPrompt(project string, shell bool) string {
	where := "the whole space (/workspace)"
	if project != "" {
		where = fmt.Sprintf("the project %q (/workspace/%s)", project, project)
	}
	commands := "The user approves each dev_bash command."
	if !shell {
		commands = "Shell commands are not available while the conversation runs in the background."
	}
	return "You have Developer Space tools (dev_*). They work in the user's own persistent Linux container, currently on " + where +
		"; pass project to address another one, and use dev_projects to list them. Use dev_clone to bring a Git repository in, " +
		"dev_glob/dev_search/dev_read_file to read it, dev_edit_file/dev_write_file/dev_patch to change it, and dev_git_commit to commit. " +
		"Commits are made as the user. " + commands +
		" Pushing is not a tool: when work is ready, tell the user to push it from the Developer Spaces page."
}
