package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// developerGitProject resolves ?project= (or the body's project) to an
// absolute folder inside the space. Git itself decides whether that folder is
// a repository; the handlers report its answer rather than guessing.
func developerGitProject(raw string) (string, string, error) {
	rel, err := service.CleanDeveloperPath(raw)
	if err != nil {
		return "", "", err
	}
	return rel, service.DeveloperAbsolutePath(rel), nil
}

type developerGitFile struct {
	Path     string `json:"path"`
	OrigPath string `json:"orig_path,omitempty"`
	Index    string `json:"index"`
	Worktree string `json:"worktree"`
}

type developerGitStatus struct {
	Repository bool               `json:"repository"`
	Branch     string             `json:"branch,omitempty"`
	Upstream   string             `json:"upstream,omitempty"`
	Ahead      int                `json:"ahead"`
	Behind     int                `json:"behind"`
	Staged     []developerGitFile `json:"staged"`
	Unstaged   []developerGitFile `json:"unstaged"`
	Untracked  []string           `json:"untracked"`
	Conflicted []string           `json:"conflicted"`
}

// parseDeveloperGitStatus reads `git status --porcelain=v2 --branch -z`.
// NUL separation is what makes paths with spaces, quotes or newlines safe;
// the line-based form quotes them in a way the UI would have to unescape.
func parseDeveloperGitStatus(out string) developerGitStatus {
	status := developerGitStatus{Repository: true, Staged: []developerGitFile{}, Unstaged: []developerGitFile{}, Untracked: []string{}, Conflicted: []string{}}
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		line := fields[i]
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			status.Branch = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# branch.upstream "):
			status.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
		case strings.HasPrefix(line, "# branch.ab "):
			var ahead, behind int
			_, _ = fmt.Sscanf(strings.TrimPrefix(line, "# branch.ab "), "+%d -%d", &ahead, &behind)
			status.Ahead, status.Behind = ahead, behind
		case strings.HasPrefix(line, "1 "), strings.HasPrefix(line, "2 "):
			// "1 XY sub mH mI mW hH hI path"; renames add "Xscore" before the
			// path and carry the original path in the next NUL field.
			count := 9
			if line[0] == '2' {
				count = 10
			}
			parts := strings.SplitN(line, " ", count)
			if len(parts) < count {
				continue
			}
			entry := developerGitFile{Path: parts[count-1]}
			if line[0] == '2' && i+1 < len(fields) {
				entry.OrigPath = fields[i+1]
				i++
			}
			xy := parts[1]
			entry.Index, entry.Worktree = string(xy[0]), string(xy[1])
			if entry.Index != "." {
				status.Staged = append(status.Staged, entry)
			}
			if entry.Worktree != "." {
				status.Unstaged = append(status.Unstaged, entry)
			}
		case strings.HasPrefix(line, "u "):
			parts := strings.SplitN(line, " ", 11)
			if len(parts) == 11 {
				status.Conflicted = append(status.Conflicted, parts[10])
			}
		case strings.HasPrefix(line, "? "):
			status.Untracked = append(status.Untracked, strings.TrimPrefix(line, "? "))
		}
	}
	return status
}

func (s *Server) DeveloperGitStatusAPI(w http.ResponseWriter, r *http.Request) {
	_, dir, err := developerGitProject(r.URL.Query().Get("project"))
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	h, ok := s.developerRuntime(w, r)
	if !ok {
		return
	}
	out, stderr, code, err := h.exec(r.Context(), s, dir, "git", "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all")
	if err != nil {
		httpResponse(w, developerCommandFailure("git status", stderr, err).Error(), http.StatusBadGateway)
		return
	}
	if code != 0 {
		if strings.Contains(stderr, "not a git repository") {
			httpResponseJSON(w, developerGitStatus{Staged: []developerGitFile{}, Unstaged: []developerGitFile{}, Untracked: []string{}, Conflicted: []string{}}, http.StatusOK)
			return
		}
		httpResponse(w, developerCommandFailure("git status", stderr, nil).Error(), http.StatusBadGateway)
		return
	}
	httpResponseJSON(w, parseDeveloperGitStatus(out), http.StatusOK)
}

// DeveloperGitDiffAPI returns the diff for one file or the whole project.
// Untracked files have no index entry, so they are diffed against /dev/null.
func (s *Server) DeveloperGitDiffAPI(w http.ResponseWriter, r *http.Request) {
	_, dir, err := developerGitProject(r.URL.Query().Get("project"))
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	file := r.URL.Query().Get("file")
	if file != "" {
		if file, err = service.CleanDeveloperPath(file); err != nil {
			httpResponse(w, "invalid file path", http.StatusBadRequest)
			return
		}
	}
	h, ok := s.developerRuntime(w, r)
	if !ok {
		return
	}
	var args []string
	switch {
	case r.URL.Query().Get("untracked") == "true" && file != "":
		args = []string{"diff", "--no-ext-diff", "--no-index", "--", "/dev/null", file}
	case r.URL.Query().Get("staged") == "true":
		args = []string{"diff", "--no-ext-diff", "--cached", "--"}
	default:
		args = []string{"diff", "--no-ext-diff", "--"}
	}
	if file != "" && r.URL.Query().Get("untracked") != "true" {
		args = append(args, file)
	}
	out, stderr, code, err := h.exec(r.Context(), s, dir, "git", args...)
	// --no-index exits 1 when the files differ, which is the expected case.
	if err != nil || (code != 0 && code != 1) {
		httpResponse(w, developerCommandFailure("git diff", stderr, err).Error(), http.StatusBadGateway)
		return
	}
	httpResponseJSON(w, map[string]any{"diff": out}, http.StatusOK)
}

func developerGitPaths(paths []string) ([]string, bool) {
	if len(paths) == 0 || len(paths) > 500 {
		return nil, false
	}
	out := make([]string, 0, len(paths))
	for _, candidate := range paths {
		if candidate == "." {
			out = append(out, ".")
			continue
		}
		clean, err := service.CleanDeveloperPath(candidate)
		if err != nil || clean == "" {
			return nil, false
		}
		out = append(out, clean)
	}
	return out, true
}

type developerGitRequest struct {
	Project string   `json:"project"`
	Paths   []string `json:"paths"`
	Message string   `json:"message"`
	Branch  string   `json:"branch"`
	Create  bool     `json:"create"`
	Confirm bool     `json:"confirm"`
}

func (s *Server) developerGitCommand(w http.ResponseWriter, r *http.Request) (*developerRuntimeHandle, string, developerGitRequest, bool) {
	var req developerGitRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpResponse(w, "invalid request body", http.StatusBadRequest)
		return nil, "", req, false
	}
	_, dir, err := developerGitProject(req.Project)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return nil, "", req, false
	}
	h, ok := s.developerRuntime(w, r)
	return h, dir, req, ok
}

func (s *Server) developerGitRun(w http.ResponseWriter, r *http.Request, h *developerRuntimeHandle, dir, operation string, args ...string) bool {
	out, stderr, code, err := h.exec(r.Context(), s, dir, "git", args...)
	if err != nil || code != 0 {
		httpResponse(w, developerCommandFailure(operation, stderr+out, err).Error(), http.StatusBadGateway)
		return false
	}
	return true
}

func (s *Server) DeveloperGitStageAPI(w http.ResponseWriter, r *http.Request) {
	h, dir, req, ok := s.developerGitCommand(w, r)
	if !ok {
		return
	}
	paths, valid := developerGitPaths(req.Paths)
	if !valid {
		httpResponse(w, "paths must contain 1 to 500 relative paths", http.StatusBadRequest)
		return
	}
	if s.developerGitRun(w, r, h, dir, "git add", append([]string{"add", "--"}, paths...)...) {
		httpResponseJSON(w, map[string]any{"staged": paths}, http.StatusOK)
	}
}

func (s *Server) DeveloperGitUnstageAPI(w http.ResponseWriter, r *http.Request) {
	h, dir, req, ok := s.developerGitCommand(w, r)
	if !ok {
		return
	}
	paths, valid := developerGitPaths(req.Paths)
	if !valid {
		httpResponse(w, "paths must contain 1 to 500 relative paths", http.StatusBadRequest)
		return
	}
	if s.developerGitRun(w, r, h, dir, "git restore --staged", append([]string{"restore", "--staged", "--"}, paths...)...) {
		httpResponseJSON(w, map[string]any{"unstaged": paths}, http.StatusOK)
	}
}

// DeveloperGitDiscardAPI drops working-tree changes. It needs an explicit
// confirm because the edits are not recoverable from Git afterwards.
func (s *Server) DeveloperGitDiscardAPI(w http.ResponseWriter, r *http.Request) {
	h, dir, req, ok := s.developerGitCommand(w, r)
	if !ok {
		return
	}
	paths, valid := developerGitPaths(req.Paths)
	if !valid || !req.Confirm {
		httpResponse(w, "discarding changes requires paths and confirm: true", http.StatusBadRequest)
		return
	}
	if s.developerGitRun(w, r, h, dir, "git restore", append([]string{"restore", "--worktree", "--"}, paths...)...) {
		httpResponseJSON(w, map[string]any{"discarded": paths}, http.StatusOK)
	}
}

func (s *Server) DeveloperGitCommitAPI(w http.ResponseWriter, r *http.Request) {
	h, dir, req, ok := s.developerGitCommand(w, r)
	if !ok {
		return
	}
	if strings.TrimSpace(req.Message) == "" || len(req.Message) > 10000 {
		httpResponse(w, "message is required and must not exceed 10000 bytes", http.StatusBadRequest)
		return
	}
	if !s.developerGitRun(w, r, h, dir, "git commit", "commit", "-m", req.Message) {
		return
	}
	head, _, _, _ := h.exec(r.Context(), s, dir, "git", "rev-parse", "--short", "HEAD")
	httpResponseJSON(w, map[string]any{"head": strings.TrimSpace(head)}, http.StatusOK)
}

func (s *Server) DeveloperGitBranchesAPI(w http.ResponseWriter, r *http.Request) {
	_, dir, err := developerGitProject(r.URL.Query().Get("project"))
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	h, ok := s.developerRuntime(w, r)
	if !ok {
		return
	}
	out, stderr, code, err := h.exec(r.Context(), s, dir, "git", "branch", "--all", "--format=%(refname:short)%00%(HEAD)")
	if err != nil || code != 0 {
		httpResponse(w, developerCommandFailure("git branch", stderr, err).Error(), http.StatusBadGateway)
		return
	}
	type branch struct {
		Name    string `json:"name"`
		Current bool   `json:"current"`
		Remote  bool   `json:"remote"`
	}
	branches := []branch{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		name, head, _ := strings.Cut(line, "\x00")
		if name == "" || strings.HasSuffix(name, "/HEAD") {
			continue
		}
		branches = append(branches, branch{Name: name, Current: head == "*", Remote: strings.HasPrefix(name, "origin/") || strings.Contains(name, "remotes/")})
	}
	httpResponseJSON(w, branches, http.StatusOK)
}

func (s *Server) DeveloperGitCheckoutAPI(w http.ResponseWriter, r *http.Request) {
	h, dir, req, ok := s.developerGitCommand(w, r)
	if !ok {
		return
	}
	if !service.ValidDeveloperBranch(req.Branch) {
		httpResponse(w, "branch is invalid", http.StatusBadRequest)
		return
	}
	args := []string{"switch", req.Branch}
	if req.Create {
		args = []string{"switch", "-c", req.Branch}
	}
	if s.developerGitRun(w, r, h, dir, "git switch", args...) {
		httpResponseJSON(w, map[string]any{"branch": req.Branch}, http.StatusOK)
	}
}

func (s *Server) DeveloperGitPullAPI(w http.ResponseWriter, r *http.Request) {
	h, dir, _, ok := s.developerGitCommand(w, r)
	if !ok {
		return
	}
	if !s.developerGitRemoteAllowed(w, r, h, dir) {
		return
	}
	out, stderr, code, err := h.exec(r.Context(), s, dir, "git", "pull", "--ff-only")
	if err != nil || code != 0 {
		httpResponse(w, developerCommandFailure("git pull", stderr+out, err).Error(), http.StatusBadGateway)
		return
	}
	httpResponseJSON(w, map[string]any{"output": strings.TrimSpace(out + stderr)}, http.StatusOK)
}

// DeveloperGitPushAPI pushes the current branch to origin. The caller must
// confirm the exact branch it saw, so a push never lands on a branch the page
// no longer shows (for example after a checkout in the terminal).
func (s *Server) DeveloperGitPushAPI(w http.ResponseWriter, r *http.Request) {
	h, dir, req, ok := s.developerGitCommand(w, r)
	if !ok {
		return
	}
	current, stderr, code, err := h.exec(r.Context(), s, dir, "git", "symbolic-ref", "--short", "HEAD")
	if err != nil || code != 0 {
		httpResponse(w, developerCommandFailure("read current branch", stderr, err).Error(), http.StatusBadGateway)
		return
	}
	current = strings.TrimSpace(current)
	if !req.Confirm || req.Branch != current || !service.ValidDeveloperBranch(current) {
		httpResponse(w, "push requires confirming the current branch ("+current+")", http.StatusConflict)
		return
	}
	if !s.developerGitRemoteAllowed(w, r, h, dir) {
		return
	}
	out, stderr, code, err := h.exec(r.Context(), s, dir, "git", "push", "--set-upstream", "origin", "HEAD:refs/heads/"+current)
	if err != nil || code != 0 {
		httpResponse(w, developerCommandFailure("git push", stderr+out, err).Error(), http.StatusBadGateway)
		return
	}
	httpResponseJSON(w, map[string]any{"branch": current, "output": strings.TrimSpace(out + stderr)}, http.StatusOK)
}

func (s *Server) developerGitRemoteAllowed(w http.ResponseWriter, r *http.Request, h *developerRuntimeHandle, dir string) bool {
	remote, stderr, code, err := h.exec(r.Context(), s, dir, "git", "remote", "get-url", "--push", "origin")
	if err != nil || code != 0 {
		httpResponse(w, developerCommandFailure("read origin remote", stderr, err).Error(), http.StatusBadGateway)
		return false
	}
	remote = strings.TrimSpace(remote)
	if err := service.ValidDeveloperRemote(remote); err != nil {
		httpResponse(w, "origin remote is not allowed: "+err.Error(), http.StatusForbidden)
		return false
	}
	if err := developerRemoteAllowed(r.Context(), remote); err != nil {
		httpResponse(w, err.Error(), http.StatusForbidden)
		return false
	}
	return true
}
