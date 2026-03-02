package nodes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// gitFetchNode clones or pulls a git repository, detects changed files since
// the last sync, and outputs file paths and contents for downstream processing
// (typically a rag_ingest node).
//
// Configuration (node.Data):
//
//	repo_url            string  — git repository URL (HTTPS or SSH) (required)
//	branch              string  — branch to track (default "main")
//	token               string  — HTTPS auth token (optional; injected into URL)
//	ssh_key             string  — SSH private key content (optional; written to temp file)
//	file_pattern        string  — glob pattern for files to include (default "*.md")
//	cache_dir           string  — root directory for cloned repos (default "/tmp/at-git-cache")
//	variable_key_prefix string  — prefix for variable store keys (default "rag_sync")
//	timeout             float64 — git operation timeout in seconds (default 120)
//
// Inputs:
//
//	(none required — all config is static)
//
// Outputs:
//
//	files         []map[string]any — changed/added files [{path, content, status}]
//	deleted_files []string         — files removed since last sync
//	commit_sha    string           — HEAD commit SHA after fetch
//	repo_url      string           — the repository URL
//	variable_key  string           — the variable key used to track sync state
type gitFetchNode struct {
	repoURL           string
	branch            string
	token             string
	sshKey            string
	filePattern       string
	cacheDir          string
	variableKeyPrefix string
	timeout           time.Duration
}

const (
	defaultGitCacheDir  = "/tmp/at-git-cache"
	defaultGitBranch    = "main"
	defaultFilePattern  = "*.md"
	defaultVarKeyPrefix = "rag_sync"
	defaultGitTimeout   = 120 * time.Second
	maxGitTimeout       = 600 * time.Second
)

func init() {
	workflow.RegisterNodeType("git_fetch", newGitFetchNode)
}

func newGitFetchNode(node service.WorkflowNode) (workflow.Noder, error) {
	n := &gitFetchNode{
		branch:            defaultGitBranch,
		filePattern:       defaultFilePattern,
		cacheDir:          defaultGitCacheDir,
		variableKeyPrefix: defaultVarKeyPrefix,
		timeout:           defaultGitTimeout,
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
	if v, ok := node.Data["ssh_key"].(string); ok {
		n.sshKey = strings.TrimSpace(v)
	}
	if v, ok := node.Data["file_pattern"].(string); ok && v != "" {
		n.filePattern = strings.TrimSpace(v)
	}
	if v, ok := node.Data["cache_dir"].(string); ok && v != "" {
		n.cacheDir = strings.TrimSpace(v)
	}
	if v, ok := node.Data["variable_key_prefix"].(string); ok && v != "" {
		n.variableKeyPrefix = strings.TrimSpace(v)
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
	if n.repoURL == "" {
		return fmt.Errorf("git_fetch: 'repo_url' is required")
	}
	return nil
}

func (n *gitFetchNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	// Allow runtime override from inputs.
	repoURL := n.repoURL
	if v, ok := inputs["repo_url"].(string); ok && v != "" {
		repoURL = v
	}
	branch := n.branch
	if v, ok := inputs["branch"].(string); ok && v != "" {
		branch = v
	}

	// Compute a stable hash for the repo directory name.
	repoHash := hashRepoKey(repoURL, branch)
	repoDir := filepath.Join(n.cacheDir, repoHash)

	// Variable key for tracking last synced commit.
	variableKey := fmt.Sprintf("%s_%s", n.variableKeyPrefix, repoHash)

	// Lookup last synced commit SHA.
	var lastSyncSHA string
	if reg.VarLookup != nil {
		val, err := reg.VarLookup(variableKey)
		if err == nil {
			lastSyncSHA = val
		}
		// Not found is fine — means first sync.
	}

	execCtx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()

	// Prepare auth for HTTPS token.
	authURL := repoURL
	if n.token != "" && strings.HasPrefix(repoURL, "https://") {
		authURL = injectHTTPSToken(repoURL, n.token)
	}

	// Prepare SSH environment if SSH key is provided.
	var sshKeyFile string
	var envVars []string
	if n.sshKey != "" {
		tmpFile, err := os.CreateTemp("", "at-git-ssh-*")
		if err != nil {
			return nil, fmt.Errorf("git_fetch: create ssh key temp file: %w", err)
		}
		defer os.Remove(tmpFile.Name())
		if _, err := tmpFile.WriteString(n.sshKey + "\n"); err != nil {
			tmpFile.Close()
			return nil, fmt.Errorf("git_fetch: write ssh key: %w", err)
		}
		tmpFile.Close()
		if err := os.Chmod(tmpFile.Name(), 0o600); err != nil {
			return nil, fmt.Errorf("git_fetch: chmod ssh key: %w", err)
		}
		sshKeyFile = tmpFile.Name()
		envVars = append(envVars, fmt.Sprintf("GIT_SSH_COMMAND=ssh -i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null", sshKeyFile))
	}

	// Clone or pull.
	isNew := false
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); os.IsNotExist(err) {
		// Clone.
		isNew = true
		if err := os.MkdirAll(n.cacheDir, 0o755); err != nil {
			return nil, fmt.Errorf("git_fetch: create cache dir: %w", err)
		}
		if err := runGit(execCtx, n.cacheDir, envVars, "clone", "--branch", branch, "--single-branch", "--depth", "0", authURL, repoHash); err != nil {
			return nil, fmt.Errorf("git_fetch: clone: %w", err)
		}
	} else {
		// Pull.
		if err := runGit(execCtx, repoDir, envVars, "fetch", "origin", branch); err != nil {
			return nil, fmt.Errorf("git_fetch: fetch: %w", err)
		}
		if err := runGit(execCtx, repoDir, envVars, "reset", "--hard", "origin/"+branch); err != nil {
			return nil, fmt.Errorf("git_fetch: reset: %w", err)
		}
	}

	// Get HEAD SHA.
	headSHA, err := gitOutput(execCtx, repoDir, envVars, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("git_fetch: get HEAD: %w", err)
	}
	headSHA = strings.TrimSpace(headSHA)

	// Determine changed, added, and deleted files.
	var changedFiles []map[string]any
	var deletedFiles []string

	if isNew || lastSyncSHA == "" {
		// First sync: all matching files are "added".
		changedFiles, err = collectAllFiles(repoDir, n.filePattern)
		if err != nil {
			return nil, fmt.Errorf("git_fetch: collect files: %w", err)
		}
	} else if lastSyncSHA != headSHA {
		// Diff since last sync.
		changedFiles, deletedFiles, err = diffFiles(execCtx, repoDir, envVars, lastSyncSHA, headSHA, n.filePattern)
		if err != nil {
			// If diff fails (e.g. SHA was garbage-collected due to shallow clone),
			// fall back to full re-sync.
			slog.Warn("git_fetch: diff failed, falling back to full sync", "error", err)
			changedFiles, err = collectAllFiles(repoDir, n.filePattern)
			if err != nil {
				return nil, fmt.Errorf("git_fetch: collect files (fallback): %w", err)
			}
		}
	}
	// If lastSyncSHA == headSHA, no changes — changedFiles and deletedFiles remain empty.

	// Build deleted files list as []any for JSON output.
	deletedAny := make([]any, len(deletedFiles))
	for i, f := range deletedFiles {
		deletedAny[i] = f
	}

	// Build files list as []any for JSON output.
	filesAny := make([]any, len(changedFiles))
	for i, f := range changedFiles {
		filesAny[i] = f
	}

	return workflow.NewResult(map[string]any{
		"files":         filesAny,
		"deleted_files": deletedAny,
		"commit_sha":    headSHA,
		"repo_url":      repoURL,
		"variable_key":  variableKey,
	}), nil
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

// injectHTTPSToken injects a token into an HTTPS URL for git auth.
// "https://github.com/foo/bar.git" → "https://x-token-auth:{token}@github.com/foo/bar.git"
func injectHTTPSToken(repoURL, token string) string {
	// Replace "https://" with "https://x-token-auth:{token}@"
	return strings.Replace(repoURL, "https://", "https://x-token-auth:"+token+"@", 1)
}

// collectAllFiles finds all files matching the pattern in a repo and reads them.
func collectAllFiles(repoDir, pattern string) ([]map[string]any, error) {
	var files []map[string]any

	err := filepath.Walk(repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Skip .git directory.
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, err := filepath.Rel(repoDir, path)
		if err != nil {
			return err
		}

		// Check against pattern.
		matched, err := matchFilePattern(relPath, pattern)
		if err != nil || !matched {
			return err
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", relPath, err)
		}

		files = append(files, map[string]any{
			"path":    relPath,
			"content": string(content),
			"status":  "added",
		})

		return nil
	})

	return files, err
}

// diffFiles computes changed and deleted files between two commits matching a pattern.
func diffFiles(ctx context.Context, repoDir string, envVars []string, fromSHA, toSHA, pattern string) (changed []map[string]any, deleted []string, err error) {
	// Get diff --name-status between the two commits.
	output, err := gitOutput(ctx, repoDir, envVars, "diff", "--name-status", fromSHA, toSHA)
	if err != nil {
		return nil, nil, err
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "\t", 2)
		if len(parts) < 2 {
			continue
		}

		status := parts[0]
		filePath := parts[1]

		// Handle renames (R100 old\tnew).
		if strings.HasPrefix(status, "R") {
			renameParts := strings.SplitN(filePath, "\t", 2)
			if len(renameParts) == 2 {
				oldPath := renameParts[0]
				filePath = renameParts[1]
				// Treat old path as deleted.
				matched, _ := matchFilePattern(oldPath, pattern)
				if matched {
					deleted = append(deleted, oldPath)
				}
			}
		}

		matched, _ := matchFilePattern(filePath, pattern)
		if !matched {
			continue
		}

		switch {
		case status == "D":
			deleted = append(deleted, filePath)
		case status == "A" || status == "M" || strings.HasPrefix(status, "R"):
			fullPath := filepath.Join(repoDir, filePath)
			content, readErr := os.ReadFile(fullPath)
			if readErr != nil {
				slog.Warn("git_fetch: cannot read changed file", "path", filePath, "error", readErr)
				continue
			}

			fileStatus := "modified"
			if status == "A" {
				fileStatus = "added"
			}

			changed = append(changed, map[string]any{
				"path":    filePath,
				"content": string(content),
				"status":  fileStatus,
			})
		}
	}

	return changed, deleted, nil
}

// matchFilePattern checks if a file path matches a glob pattern.
// Supports comma-separated patterns like "*.md,*.txt,docs/**/*.rst".
func matchFilePattern(filePath, pattern string) (bool, error) {
	patterns := strings.Split(pattern, ",")
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		// filepath.Match only matches the basename against simple patterns.
		// For patterns without path separators, match against basename.
		// For patterns with path separators, match against the full relative path.
		if strings.ContainsRune(p, '/') || strings.ContainsRune(p, filepath.Separator) {
			matched, err := filepath.Match(p, filePath)
			if err != nil {
				return false, err
			}
			if matched {
				return true, nil
			}
		} else {
			matched, err := filepath.Match(p, filepath.Base(filePath))
			if err != nil {
				return false, err
			}
			if matched {
				return true, nil
			}
		}
	}
	return false, nil
}
