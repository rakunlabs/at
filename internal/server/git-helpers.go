package server

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

	"github.com/rakunlabs/at/internal/service"
)

// ─── Git Helpers for MCP Tools ───
//
// These mirror the helpers in internal/service/workflow/nodes/git-fetch.go
// and internal/service/workflow/nodes/rag-search.go, but live in the server
// package to avoid import cycles. They are used by gateway-rag-mcp.go and
// rag-mcp.go for commit-specific checkout and auth.

// mcpGitEnv builds the environment for git commands.
func mcpGitEnv(extra []string) []string {
	env := []string{
		"PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
		"HOME=" + os.TempDir(),
		"GIT_TERMINAL_PROMPT=0",
	}
	env = append(env, extra...)
	return env
}

// mcpRunGit executes a git command in the given directory.
func mcpRunGit(ctx context.Context, dir string, extraEnv []string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = mcpGitEnv(extraEnv)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w: %s", args[0], err, stderr.String())
	}
	return nil
}

// defaultTokenUser is the fallback username for HTTPS token auth.
// "x-token-auth" works with GitHub, GitLab and Bitbucket out of the box.
// Users can override per-server via token_user (e.g. "oauth2" for GitLab OAuth tokens).
const defaultTokenUser = "x-token-auth"

// mcpInjectHTTPSToken injects a token into an HTTPS URL for git auth.
// user controls the username portion; pass "" to use the default.
// "https://github.com/foo/bar.git" -> "https://{user}:{token}@github.com/foo/bar.git"
func mcpInjectHTTPSToken(repoURL, token, user string) string {
	if user == "" {
		user = defaultTokenUser
	}
	return strings.Replace(repoURL, "https://", "https://"+user+":"+token+"@", 1)
}

// hashRepoCommitKey creates a short deterministic hash from repo URL + commit SHA.
// Uses the same SHA-256 scheme as hashCacheKey and hashRepoKey in the workflow nodes,
// but keyed on commit SHA for commit-specific cache directories.
func hashRepoCommitKey(repoURL, commitSHA string) string {
	h := sha256.Sum256([]byte(repoURL + "\x00" + commitSHA))
	return hex.EncodeToString(h[:8])
}

// ensureRepoAtCommitMCP clones a repo and checks out a specific commit.
// The clone directory is <cacheDir>/<hash(repoURL, commitSHA)>.
// If the directory already exists with a valid .git, it is reused.
// authURL should have credentials embedded (use mcpInjectHTTPSToken for HTTPS);
// repoURL is the canonical URL without credentials (used for hashing).
func ensureRepoAtCommitMCP(ctx context.Context, authURL, repoURL, commitSHA, cacheDir string, envVars []string) (string, error) {
	dirName := hashRepoCommitKey(repoURL, commitSHA)
	repoDir := filepath.Join(cacheDir, dirName)

	// If directory exists and has a .git, it is already at the right commit.
	gitDir := filepath.Join(repoDir, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		// Verify health with a quick rev-parse.
		if err := mcpRunGit(ctx, repoDir, envVars, "rev-parse", "--git-dir"); err == nil {
			return repoDir, nil
		}
		// Corrupted — remove and re-clone.
		_ = os.RemoveAll(repoDir)
	}

	// Create cache dir.
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}

	// Clone without specifying a branch — we'll checkout the commit directly.
	if err := mcpRunGit(ctx, cacheDir, envVars, "clone", "--no-checkout", authURL, dirName); err != nil {
		_ = os.RemoveAll(repoDir)
		return "", fmt.Errorf("clone: %w", err)
	}

	// Checkout the specific commit (detached HEAD).
	if err := mcpRunGit(ctx, repoDir, envVars, "checkout", commitSHA); err != nil {
		_ = os.RemoveAll(repoDir)
		return "", fmt.Errorf("checkout %s: %w", commitSHA, err)
	}

	return repoDir, nil
}

// gitAuthResult holds the resolved git authentication state.
type gitAuthResult struct {
	// envVars contains environment variables for git commands (e.g. GIT_SSH_COMMAND).
	envVars []string

	// token is the resolved HTTPS auth token (empty if not configured).
	token string

	// tokenUser is the username for HTTPS token auth (empty = defaultTokenUser).
	tokenUser string

	// cleanup should be called (if non-nil) to remove temporary files (e.g. SSH key).
	cleanup func()
}

// authURL returns the repo URL with credentials embedded, if applicable.
func (a *gitAuthResult) authURL(repoURL string) string {
	if a.token != "" && strings.HasPrefix(repoURL, "https://") {
		return mcpInjectHTTPSToken(repoURL, a.token, a.tokenUser)
	}
	return repoURL
}

// resolveGitAuth resolves git authentication from a RAGMCPServer's config
// by looking up TokenVariable and SSHKeyVariable in the variable store.
// Returns a gitAuthResult that the caller must clean up via result.cleanup().
// If the server config has no auth variables, returns a zero-value result (no auth).
func resolveGitAuth(ctx context.Context, variableStore service.VariableStorer, srv *service.RAGMCPServer) (*gitAuthResult, error) {
	result := &gitAuthResult{}

	if srv == nil || variableStore == nil {
		return result, nil
	}

	// Carry token user from config (empty = default).
	result.tokenUser = srv.Config.TokenUser

	// Resolve HTTPS token.
	if srv.Config.TokenVariable != "" {
		v, err := variableStore.GetVariableByKey(ctx, srv.Config.TokenVariable)
		if err != nil {
			return result, fmt.Errorf("resolve token variable %q: %w", srv.Config.TokenVariable, err)
		}
		if v != nil {
			result.token = v.Value
		} else {
			slog.Warn("git auth: token variable not found", "key", srv.Config.TokenVariable)
		}
	}

	// Resolve SSH key.
	if srv.Config.SSHKeyVariable != "" {
		v, err := variableStore.GetVariableByKey(ctx, srv.Config.SSHKeyVariable)
		if err != nil {
			return result, fmt.Errorf("resolve ssh key variable %q: %w", srv.Config.SSHKeyVariable, err)
		}
		if v != nil && v.Value != "" {
			tmpFile, err := os.CreateTemp("", "at-mcp-ssh-*")
			if err != nil {
				return result, fmt.Errorf("create ssh key temp file: %w", err)
			}
			if _, err := tmpFile.WriteString(v.Value + "\n"); err != nil {
				tmpFile.Close()
				os.Remove(tmpFile.Name())
				return result, fmt.Errorf("write ssh key: %w", err)
			}
			tmpFile.Close()
			if err := os.Chmod(tmpFile.Name(), 0o600); err != nil {
				os.Remove(tmpFile.Name())
				return result, fmt.Errorf("chmod ssh key: %w", err)
			}
			result.envVars = append(result.envVars,
				fmt.Sprintf("GIT_SSH_COMMAND=ssh -i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null", tmpFile.Name()),
			)
			result.cleanup = func() { os.Remove(tmpFile.Name()) }
		} else {
			slog.Warn("git auth: ssh key variable not found or empty", "key", srv.Config.SSHKeyVariable)
		}
	}

	return result, nil
}
