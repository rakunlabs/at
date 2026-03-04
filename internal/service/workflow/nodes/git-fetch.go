package nodes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rytsh/mugo/templatex"

	"github.com/rakunlabs/at/internal/render"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// gitFetchNode clones or pulls a git repository and outputs the local
// repository path along with the HEAD commit SHA. It is a pure git
// operation node — no file reading, pattern matching, or variable
// lookups. Connect its output to a git_diff node for change detection.
//
// All config fields (repo_url, branch, token, ssh_key) support Go
// templates with access to input data and variables via {{ getVar "key" }}.
//
// Configuration (node.Data):
//
//	repo_url  string  — git repository URL (HTTPS or SSH) (required)
//	branch    string  — branch to track (empty = auto-detect remote default)
//	token     string  — HTTPS auth token (optional; injected into URL)
//	ssh_key   string  — SSH private key content (optional; written to temp file)
//	cache_dir string  — root directory for cloned repos (default "/tmp/at-git-cache")
//	timeout   float64 — git operation timeout in seconds (default 120)
//
// Inputs:
//
//	repo_url string — runtime override for repo URL (optional)
//	branch   string — runtime override for branch (optional)
//
// Outputs:
//
//	repo_dir     string — local filesystem path of the cloned repository
//	commit_sha   string — HEAD commit SHA after fetch
//	repo_url     string — the repository URL
//	branch       string — the branch that was fetched
//	is_new_clone bool   — true if this was a fresh clone (no prior cache)
type gitFetchNode struct {
	repoURL   string
	branch    string
	token     string
	tokenUser string
	sshKey    string
	cacheDir  string
	timeout   time.Duration
}

const (
	defaultGitCacheDir = "/tmp/at-git-cache"
	defaultGitBranch   = "main"
	defaultGitTimeout  = 120 * time.Second
	maxGitTimeout      = 600 * time.Second
)

func init() {
	workflow.RegisterNodeType("git_fetch", newGitFetchNode)
}

func newGitFetchNode(node service.WorkflowNode) (workflow.Noder, error) {
	n := &gitFetchNode{
		cacheDir: defaultGitCacheDir,
		timeout:  defaultGitTimeout,
	}

	if v, ok := node.Data["repo_url"].(string); ok {
		n.repoURL = strings.TrimSpace(v)
	}
	if v, ok := node.Data["branch"].(string); ok && v != "" {
		n.branch = strings.TrimSpace(v)
	}
	if v, ok := node.Data["token"].(string); ok {
		n.token = strings.TrimSpace(v)
	}
	if v, ok := node.Data["token_user"].(string); ok {
		n.tokenUser = strings.TrimSpace(v)
	}
	if v, ok := node.Data["ssh_key"].(string); ok {
		n.sshKey = strings.TrimSpace(v)
	}
	if v, ok := node.Data["cache_dir"].(string); ok && v != "" {
		n.cacheDir = strings.TrimSpace(v)
	}
	if t, ok := node.Data["timeout"].(float64); ok && t > 0 {
		n.timeout = time.Duration(t) * time.Second
		if n.timeout > maxGitTimeout {
			n.timeout = maxGitTimeout
		}
	}

	return n, nil
}

func (n *gitFetchNode) Type() string { return "git_fetch" }

func (n *gitFetchNode) Validate(_ context.Context, _ *workflow.Registry) error {
	return nil
}

func (n *gitFetchNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	// Render config fields as Go templates with input data + variables.
	funcs := varFuncMap(reg)
	tmplData := inputs

	repoURL, err := renderField(n.repoURL, tmplData, funcs)
	if err != nil {
		return nil, fmt.Errorf("git_fetch: render repo_url: %w", err)
	}
	branch, err := renderField(n.branch, tmplData, funcs)
	if err != nil {
		return nil, fmt.Errorf("git_fetch: render branch: %w", err)
	}
	token, err := renderField(n.token, tmplData, funcs)
	if err != nil {
		return nil, fmt.Errorf("git_fetch: render token: %w", err)
	}
	tokenUser, err := renderField(n.tokenUser, tmplData, funcs)
	if err != nil {
		return nil, fmt.Errorf("git_fetch: render token_user: %w", err)
	}
	sshKey, err := renderField(n.sshKey, tmplData, funcs)
	if err != nil {
		return nil, fmt.Errorf("git_fetch: render ssh_key: %w", err)
	}

	// Allow runtime override from inputs (takes precedence over rendered config).
	if v, ok := inputs["repo_url"].(string); ok && v != "" {
		repoURL = v
	}
	if v, ok := inputs["branch"].(string); ok && v != "" {
		branch = v
	}

	if repoURL == "" {
		return nil, fmt.Errorf("git_fetch: 'repo_url' is empty after rendering")
	}

	execCtx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()

	// Prepare auth for HTTPS token.
	authURL := repoURL
	if token != "" && strings.HasPrefix(repoURL, "https://") {
		authURL = injectHTTPSToken(repoURL, token, tokenUser)
	}

	// Prepare SSH environment if SSH key is provided.
	var envVars []string
	if sshKey != "" {
		tmpFile, err := os.CreateTemp("", "at-git-ssh-*")
		if err != nil {
			return nil, fmt.Errorf("git_fetch: create ssh key temp file: %w", err)
		}
		defer os.Remove(tmpFile.Name())
		if _, err := tmpFile.WriteString(sshKey + "\n"); err != nil {
			tmpFile.Close()
			return nil, fmt.Errorf("git_fetch: write ssh key: %w", err)
		}
		tmpFile.Close()
		if err := os.Chmod(tmpFile.Name(), 0o600); err != nil {
			return nil, fmt.Errorf("git_fetch: chmod ssh key: %w", err)
		}
		envVars = append(envVars, fmt.Sprintf("GIT_SSH_COMMAND=ssh -i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null", tmpFile.Name()))
	}

	// Auto-detect remote default branch when not explicitly set.
	if branch == "" {
		detected, err := resolveDefaultBranch(execCtx, authURL, envVars)
		if err != nil {
			// Fall back to "main" if detection fails.
			branch = defaultGitBranch
		} else {
			branch = detected
		}
	}

	// Compute a stable hash for the repo directory name.
	repoHash := hashRepoKey(repoURL, branch)
	repoDir := filepath.Join(n.cacheDir, repoHash)

	// Clone or fetch+reset. Handle corrupted/partial clones gracefully.
	isNewClone, err := n.ensureRepo(execCtx, repoDir, repoHash, authURL, branch, envVars)
	if err != nil {
		return nil, err
	}

	// Get HEAD SHA.
	headSHA, err := gitOutput(execCtx, repoDir, envVars, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("git_fetch: get HEAD: %w", err)
	}
	headSHA = strings.TrimSpace(headSHA)

	return workflow.NewResult(map[string]any{
		"repo_dir":     repoDir,
		"commit_sha":   headSHA,
		"repo_url":     repoURL,
		"branch":       branch,
		"is_new_clone": isNewClone,
	}), nil
}

// ensureRepo clones the repo if it doesn't exist, or fetches+resets if it does.
// If the existing clone is corrupted (e.g. from an interrupted previous run),
// it removes the directory and re-clones.
func (n *gitFetchNode) ensureRepo(ctx context.Context, repoDir, repoHash, authURL, branch string, envVars []string) (isNewClone bool, err error) {
	gitDir := filepath.Join(repoDir, ".git")

	if _, statErr := os.Stat(gitDir); os.IsNotExist(statErr) {
		// No .git directory — need a fresh clone.
		// But the directory itself might exist (leftover from a failed clone).
		if err := os.RemoveAll(repoDir); err != nil {
			return false, fmt.Errorf("git_fetch: clean stale dir: %w", err)
		}
		if err := n.cloneRepo(ctx, repoDir, repoHash, authURL, branch, envVars); err != nil {
			return false, err
		}
		return true, nil
	}

	// .git exists — verify the repo is healthy before fetching.
	if err := runGit(ctx, repoDir, envVars, "rev-parse", "--git-dir"); err != nil {
		// Repository is corrupted. Remove and re-clone.
		if rmErr := os.RemoveAll(repoDir); rmErr != nil {
			return false, fmt.Errorf("git_fetch: remove corrupted repo: %w", rmErr)
		}
		if err := n.cloneRepo(ctx, repoDir, repoHash, authURL, branch, envVars); err != nil {
			return false, err
		}
		return true, nil
	}

	// Healthy repo — fetch and reset.
	if err := runGit(ctx, repoDir, envVars, "fetch", "origin", branch); err != nil {
		return false, fmt.Errorf("git_fetch: fetch: %w", err)
	}
	if err := runGit(ctx, repoDir, envVars, "reset", "--hard", "origin/"+branch); err != nil {
		return false, fmt.Errorf("git_fetch: reset: %w", err)
	}
	// Clean any leftover untracked files from previous operations.
	_ = runGit(ctx, repoDir, envVars, "clean", "-fd")

	return false, nil
}

// cloneRepo performs the initial clone into the cache directory.
func (n *gitFetchNode) cloneRepo(ctx context.Context, repoDir, repoHash, authURL, branch string, envVars []string) error {
	if err := os.MkdirAll(n.cacheDir, 0o755); err != nil {
		return fmt.Errorf("git_fetch: create cache dir: %w", err)
	}

	err := runGit(ctx, n.cacheDir, envVars, "clone", "--branch", branch, "--single-branch", authURL, repoHash)
	if err != nil {
		// Clean up partial clone on failure.
		_ = os.RemoveAll(repoDir)
		return fmt.Errorf("git_fetch: clone: %w", err)
	}

	return nil
}

// ─── Template Helpers ───

// renderField renders a config string as a Go template. If the string
// contains no template directives it is returned as-is with no overhead.
func renderField(tmpl string, data any, funcs map[string]any) (string, error) {
	if tmpl == "" {
		return "", nil
	}

	result, err := render.ExecuteWithData(tmpl, data, templatex.WithExecFuncMap(funcs))
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(result)), nil
}

// ─── Git Helpers ───

// runGit executes a git command in the given directory.
func runGit(ctx context.Context, dir string, extraEnv []string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv(extraEnv)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w: %s", args[0], err, stderr.String())
	}
	return nil
}

// gitOutput executes a git command and returns stdout.
func gitOutput(ctx context.Context, dir string, extraEnv []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv(extraEnv)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, stderr.String())
	}
	return stdout.String(), nil
}

// gitEnv builds the environment for git commands.
func gitEnv(extra []string) []string {
	env := []string{
		"PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
		"HOME=" + os.TempDir(),
		"GIT_TERMINAL_PROMPT=0",
	}
	env = append(env, extra...)
	return env
}

// hashRepoKey creates a short deterministic hash from repo URL + branch.
func hashRepoKey(repoURL, branch string) string {
	h := sha256.Sum256([]byte(repoURL + "\x00" + branch))
	return hex.EncodeToString(h[:8])
}

// defaultTokenUser is the fallback username for HTTPS token auth.
// "x-token-auth" works with GitHub, GitLab and Bitbucket.
// Users can override per-node via token_user (e.g. "oauth2" for GitLab OAuth tokens).
const defaultTokenUser = "x-token-auth"

// injectHTTPSToken injects a token into an HTTPS URL for git auth.
// user controls the username portion; pass "" to use the default.
// "https://github.com/foo/bar.git" → "https://{user}:{token}@github.com/foo/bar.git"
func injectHTTPSToken(repoURL, token, user string) string {
	if user == "" {
		user = defaultTokenUser
	}
	return strings.Replace(repoURL, "https://", "https://"+user+":"+token+"@", 1)
}

// resolveDefaultBranch queries the remote to determine its default branch
// using "git ls-remote --symref <url> HEAD". Returns the branch name
// (e.g. "main", "master") or an error if detection fails.
func resolveDefaultBranch(ctx context.Context, repoURL string, envVars []string) (string, error) {
	// git ls-remote --symref outputs something like:
	//   ref: refs/heads/main	HEAD
	//   <sha>	HEAD
	output, err := gitOutput(ctx, os.TempDir(), envVars, "ls-remote", "--symref", repoURL, "HEAD")
	if err != nil {
		return "", fmt.Errorf("ls-remote: %w", err)
	}

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ref: refs/heads/") {
			// "ref: refs/heads/main\tHEAD" → "main"
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				branch := strings.TrimPrefix(parts[0], "ref: refs/heads/")
				if branch != "" {
					return branch, nil
				}
			}
		}
	}

	return "", fmt.Errorf("could not detect default branch from ls-remote output")
}
