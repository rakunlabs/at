package rag

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/rakunlabs/at/internal/service"
)

const (
	defaultGitCacheDir = "/tmp/at-git-cache"
	defaultMaxFileSize = 1 << 20 // 1 MB
	syncTimeout        = 5 * time.Minute
)

// SyncDeps holds the external dependencies needed by the sync engine.
type SyncDeps struct {
	RAGService *Service
	PageStore  service.RAGPageStorer
	StateStore service.RAGStateStorer
	VarStore   service.VariableStorer
}

// SyncResult contains statistics about a sync operation.
type SyncResult struct {
	FilesProcessed int    `json:"files_processed"`
	FilesDeleted   int    `json:"files_deleted"`
	ChunksAdded    int    `json:"chunks_added"`
	CommitSHA      string `json:"commit_sha"`
	IsFullSync     bool   `json:"is_full_sync"`
}

// SyncCollection performs a git-based sync for a RAG collection.
// It clones/fetches the configured git repo, detects changed files,
// stores original content in rag_pages, and ingests chunks into the vector store.
func SyncCollection(ctx context.Context, deps SyncDeps, collection *service.RAGCollection) (*SyncResult, error) {
	if collection.Config.GitSource == nil {
		return nil, fmt.Errorf("collection %q has no git source configured", collection.Name)
	}

	gs := collection.Config.GitSource

	if gs.RepoURL == "" {
		return nil, fmt.Errorf("collection %q: git source repo_url is required", collection.Name)
	}

	// Apply timeout.
	ctx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()

	slog.Info("starting RAG collection sync",
		"collection", collection.Name,
		"collection_id", collection.ID,
		"repo_url", gs.RepoURL,
	)

	// Resolve authentication.
	var token, tokenUser, sshKeyPath string
	var envVars []string
	var cleanup func()

	if gs.TokenVariable != "" && deps.VarStore != nil {
		v, err := deps.VarStore.GetVariableByKey(ctx, gs.TokenVariable)
		if err != nil {
			slog.Warn("sync: failed to resolve token variable", "key", gs.TokenVariable, "error", err)
		} else if v != nil {
			token = v.Value
		}
	}
	tokenUser = gs.TokenUser
	if tokenUser == "" {
		tokenUser = "x-token-auth"
	}

	if gs.SSHKeyVariable != "" && deps.VarStore != nil {
		v, err := deps.VarStore.GetVariableByKey(ctx, gs.SSHKeyVariable)
		if err != nil {
			slog.Warn("sync: failed to resolve SSH key variable", "key", gs.SSHKeyVariable, "error", err)
		} else if v != nil {
			tmpFile, err := os.CreateTemp("", "at-ssh-key-*")
			if err != nil {
				return nil, fmt.Errorf("create temp SSH key file: %w", err)
			}
			if _, err := tmpFile.WriteString(v.Value); err != nil {
				tmpFile.Close()
				os.Remove(tmpFile.Name())
				return nil, fmt.Errorf("write temp SSH key file: %w", err)
			}
			tmpFile.Close()
			os.Chmod(tmpFile.Name(), 0o600)
			sshKeyPath = tmpFile.Name()
			envVars = append(envVars, fmt.Sprintf("GIT_SSH_COMMAND=ssh -i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null", sshKeyPath))
			cleanup = func() { os.Remove(sshKeyPath) }
		}
	}
	if cleanup != nil {
		defer cleanup()
	}

	// Build auth URL.
	authURL := gs.RepoURL
	if token != "" {
		authURL = syncInjectHTTPSToken(gs.RepoURL, token, tokenUser)
	}

	// Resolve branch.
	branch := gs.Branch
	if branch == "" {
		var err error
		branch, err = syncResolveDefaultBranch(ctx, authURL, envVars)
		if err != nil {
			slog.Warn("sync: failed to auto-detect branch, defaulting to main", "error", err)
			branch = "main"
		}
	}

	// Clone or fetch the repo.
	cacheDir := defaultGitCacheDir
	repoHash := syncHashKey(gs.RepoURL, branch)
	repoDir := filepath.Join(cacheDir, repoHash)

	isNewClone, err := syncEnsureRepo(ctx, repoDir, cacheDir, repoHash, authURL, branch, envVars)
	if err != nil {
		return nil, fmt.Errorf("ensure repo: %w", err)
	}

	// Get HEAD commit SHA.
	commitSHA, err := syncGitOutput(ctx, repoDir, envVars, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("get HEAD commit: %w", err)
	}
	commitSHA = strings.TrimSpace(commitSHA)

	// Look up last sync state.
	stateKey := fmt.Sprintf("rag_sync_%s", repoHash)
	var lastSyncSHA string
	if deps.StateStore != nil {
		state, err := deps.StateStore.GetRAGState(ctx, stateKey)
		if err != nil {
			slog.Warn("sync: failed to get last sync state", "key", stateKey, "error", err)
		} else if state != nil {
			lastSyncSHA = state.Value
		}
	}

	// Determine file pattern.
	filePattern := gs.FilePatterns
	if filePattern == "" {
		filePattern = "*.md,*.txt,*.go,*.py,*.js,*.ts,*.json,*.yaml,*.yml,*.rst,*.html"
	}

	maxFileSize := gs.MaxFileSize
	if maxFileSize <= 0 {
		maxFileSize = defaultMaxFileSize
	}

	// Compute diff.
	var changedFiles []syncFileInfo
	var deletedFiles []string
	isFullSync := false

	if isNewClone || lastSyncSHA == "" {
		// Full sync.
		isFullSync = true
		changedFiles, err = syncCollectAllFiles(repoDir, filePattern, maxFileSize)
		if err != nil {
			return nil, fmt.Errorf("collect all files: %w", err)
		}
	} else if lastSyncSHA != commitSHA {
		// Incremental diff.
		changedFiles, deletedFiles, err = syncDiffFiles(ctx, repoDir, envVars, lastSyncSHA, commitSHA, filePattern, maxFileSize)
		if err != nil {
			slog.Warn("sync: incremental diff failed, falling back to full sync", "error", err)
			isFullSync = true
			changedFiles, err = syncCollectAllFiles(repoDir, filePattern, maxFileSize)
			if err != nil {
				return nil, fmt.Errorf("collect all files (fallback): %w", err)
			}
		}
	} else {
		slog.Info("sync: no changes detected",
			"collection", collection.Name,
			"commit_sha", commitSHA,
		)
		return &SyncResult{CommitSHA: commitSHA}, nil
	}

	slog.Info("sync: processing changes",
		"collection", collection.Name,
		"changed", len(changedFiles),
		"deleted", len(deletedFiles),
		"full_sync", isFullSync,
	)

	result := &SyncResult{
		CommitSHA:  commitSHA,
		IsFullSync: isFullSync,
	}

	// Process deleted files.
	for _, path := range deletedFiles {
		source := gs.RepoURL + "/" + path
		// Delete from vector store.
		if err := deps.RAGService.DeleteDocumentsBySource(ctx, collection.ID, source); err != nil {
			slog.Warn("sync: failed to delete chunks", "source", source, "error", err)
		}
		// Delete from pages.
		if deps.PageStore != nil {
			if err := deps.PageStore.DeleteRAGPageBySource(ctx, collection.ID, source); err != nil {
				slog.Warn("sync: failed to delete page", "source", source, "error", err)
			}
		}
		result.FilesDeleted++
	}

	// Process changed/new files.
	for _, f := range changedFiles {
		source := gs.RepoURL + "/" + f.Path

		// Store original content in rag_pages.
		if deps.PageStore != nil {
			contentHash := sha256Hex(f.Content)
			_, err := deps.PageStore.UpsertRAGPage(ctx, service.RAGPage{
				CollectionID: collection.ID,
				Source:       source,
				Path:         f.Path,
				Content:      f.Content,
				ContentType:  DetectContentType(f.Path),
				Metadata: map[string]any{
					"repo_url":   gs.RepoURL,
					"branch":     branch,
					"commit_sha": commitSHA,
					"path":       f.Path,
				},
				ContentHash: contentHash,
			})
			if err != nil {
				slog.Warn("sync: failed to upsert page", "path", f.Path, "error", err)
			}
		}

		// Delete old chunks then re-ingest.
		if err := deps.RAGService.DeleteDocumentsBySource(ctx, collection.ID, source); err != nil {
			slog.Warn("sync: failed to delete old chunks", "source", source, "error", err)
		}

		contentType := DetectContentType(f.Path)
		if contentType == "" {
			contentType = "text/plain"
		}

		extraMetadata := map[string]any{
			"repo_url":   gs.RepoURL,
			"branch":     branch,
			"commit_sha": commitSHA,
			"path":       f.Path,
		}

		ingestResult, err := deps.RAGService.Ingest(ctx, collection.ID, strings.NewReader(f.Content), contentType, source, extraMetadata)
		if err != nil {
			slog.Warn("sync: failed to ingest file", "path", f.Path, "error", err)
			continue
		}

		result.ChunksAdded += ingestResult.ChunksStored
		result.FilesProcessed++
	}

	// Update sync state.
	if deps.StateStore != nil {
		if err := deps.StateStore.SetRAGState(ctx, stateKey, commitSHA); err != nil {
			slog.Error("sync: failed to save sync state", "key", stateKey, "error", err)
		}
	}

	slog.Info("sync: completed",
		"collection", collection.Name,
		"files_processed", result.FilesProcessed,
		"files_deleted", result.FilesDeleted,
		"chunks_added", result.ChunksAdded,
		"commit_sha", commitSHA,
	)

	return result, nil
}

// ─── Internal helpers ───

type syncFileInfo struct {
	Path    string
	Content string
	Status  string // "added" or "modified"
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func syncHashKey(parts ...string) string {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(p))
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8])
}

func syncGitEnv(extra []string) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.TempDir(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_SSH_COMMAND=ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null",
	}
	return append(env, extra...)
}

func syncRunGit(ctx context.Context, dir string, extraEnv []string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = syncGitEnv(extraEnv)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func syncGitOutput(ctx context.Context, dir string, extraEnv []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = syncGitEnv(extraEnv)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func syncInjectHTTPSToken(repoURL, token, user string) string {
	// Convert SSH to HTTPS if needed.
	if strings.HasPrefix(repoURL, "git@") || strings.HasPrefix(repoURL, "ssh://") {
		repoURL = syncSSHToHTTPS(repoURL)
	}
	if !strings.HasPrefix(repoURL, "https://") && !strings.HasPrefix(repoURL, "http://") {
		return repoURL
	}
	prefix := "https://"
	if strings.HasPrefix(repoURL, "http://") {
		prefix = "http://"
	}
	rest := strings.TrimPrefix(repoURL, prefix)
	return fmt.Sprintf("%s%s:%s@%s", prefix, user, token, rest)
}

func syncSSHToHTTPS(repoURL string) string {
	if strings.HasPrefix(repoURL, "git@") {
		// git@github.com:org/repo.git -> https://github.com/org/repo.git
		rest := strings.TrimPrefix(repoURL, "git@")
		rest = strings.Replace(rest, ":", "/", 1)
		return "https://" + rest
	}
	if strings.HasPrefix(repoURL, "ssh://") {
		return "https://" + strings.TrimPrefix(repoURL, "ssh://")
	}
	return repoURL
}

func syncResolveDefaultBranch(ctx context.Context, repoURL string, envVars []string) (string, error) {
	output, err := syncGitOutput(ctx, "", envVars, "ls-remote", "--symref", repoURL, "HEAD")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "ref: refs/heads/") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				return strings.TrimPrefix(parts[0], "ref: refs/heads/"), nil
			}
		}
	}
	return "", fmt.Errorf("could not determine default branch from ls-remote output")
}

func syncEnsureRepo(ctx context.Context, repoDir, cacheDir, repoHash, authURL, branch string, envVars []string) (isNewClone bool, err error) {
	gitDir := filepath.Join(repoDir, ".git")

	if _, err := os.Stat(gitDir); err == nil {
		// Repo exists — try fetch + reset.
		if verifyErr := syncRunGit(ctx, repoDir, envVars, "rev-parse", "--git-dir"); verifyErr != nil {
			// Corrupted — remove and re-clone.
			slog.Warn("sync: repo appears corrupted, removing", "dir", repoDir)
			os.RemoveAll(repoDir)
		} else {
			// Fetch and reset.
			if fetchErr := syncRunGit(ctx, repoDir, envVars, "fetch", "origin", branch); fetchErr != nil {
				slog.Warn("sync: fetch failed, will re-clone", "error", fetchErr)
				os.RemoveAll(repoDir)
			} else {
				if resetErr := syncRunGit(ctx, repoDir, envVars, "reset", "--hard", "origin/"+branch); resetErr != nil {
					return false, fmt.Errorf("reset: %w", resetErr)
				}
				syncRunGit(ctx, repoDir, envVars, "clean", "-fd")
				return false, nil
			}
		}
	}

	// Clone fresh.
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return false, fmt.Errorf("create cache dir: %w", err)
	}

	if cloneErr := syncRunGit(ctx, cacheDir, envVars, "clone", "--branch", branch, "--single-branch", authURL, repoHash); cloneErr != nil {
		os.RemoveAll(repoDir) // Clean up partial clone.
		return false, fmt.Errorf("clone: %w", cloneErr)
	}

	return true, nil
}

func syncMatchFilePattern(filePath, pattern string) (bool, error) {
	patterns := strings.Split(pattern, ",")
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		var matched bool
		var err error
		if strings.ContainsRune(p, '/') || strings.ContainsRune(p, filepath.Separator) {
			matched, err = filepath.Match(p, filePath)
		} else {
			matched, err = filepath.Match(p, filepath.Base(filePath))
		}
		if err != nil {
			return false, fmt.Errorf("match pattern %q: %w", p, err)
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

func syncCollectAllFiles(repoDir, pattern string, maxFileSize int) ([]syncFileInfo, error) {
	var files []syncFileInfo

	err := filepath.Walk(repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip unreadable.
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, err := filepath.Rel(repoDir, path)
		if err != nil {
			return nil
		}

		matched, err := syncMatchFilePattern(relPath, pattern)
		if err != nil || !matched {
			return nil
		}

		if info.Size() > int64(maxFileSize) {
			slog.Debug("sync: skipping large file", "path", relPath, "size", info.Size())
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("sync: failed to read file", "path", relPath, "error", err)
			return nil
		}

		files = append(files, syncFileInfo{
			Path:    relPath,
			Content: string(data),
			Status:  "added",
		})

		return nil
	})

	return files, err
}

func syncDiffFiles(ctx context.Context, repoDir string, envVars []string, fromSHA, toSHA, pattern string, maxFileSize int) (changed []syncFileInfo, deleted []string, err error) {
	output, err := syncGitOutput(ctx, repoDir, envVars, "diff", "--name-status", fromSHA, toSHA)
	if err != nil {
		return nil, nil, err
	}

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}

		status := parts[0]
		filePath := parts[1]

		// Handle renames (R100, etc.).
		if strings.HasPrefix(status, "R") && len(parts) >= 3 {
			oldPath := parts[1]
			newPath := parts[2]
			// Old file is deleted.
			matched, _ := syncMatchFilePattern(oldPath, pattern)
			if matched {
				deleted = append(deleted, oldPath)
			}
			filePath = newPath
			status = "M" // Treat as modified.
		}

		matched, mErr := syncMatchFilePattern(filePath, pattern)
		if mErr != nil || !matched {
			continue
		}

		if status == "D" {
			deleted = append(deleted, filePath)
			continue
		}

		// Read content from working tree.
		fullPath := filepath.Join(repoDir, filePath)
		info, statErr := os.Stat(fullPath)
		if statErr != nil {
			slog.Warn("sync: cannot stat changed file", "path", filePath, "error", statErr)
			continue
		}

		if info.Size() > int64(maxFileSize) {
			slog.Debug("sync: skipping large file", "path", filePath, "size", info.Size())
			continue
		}

		data, readErr := os.ReadFile(fullPath)
		if readErr != nil {
			slog.Warn("sync: cannot read changed file", "path", filePath, "error", readErr)
			continue
		}

		st := "modified"
		if status == "A" {
			st = "added"
		}

		changed = append(changed, syncFileInfo{
			Path:    filePath,
			Content: string(data),
			Status:  st,
		})
	}

	return changed, deleted, nil
}

// gitReadFileAtCommitSync reads a file from a git repo at a specific commit
// using go-git's in-process object store (no working tree modification).
func gitReadFileAtCommitSync(repoDir, commitSHA, filePath string) (string, error) {
	repo, err := git.PlainOpen(repoDir)
	if err != nil {
		return "", fmt.Errorf("open repo: %w", err)
	}

	hash := plumbing.NewHash(commitSHA)
	commit, err := repo.CommitObject(hash)
	if err != nil {
		return "", fmt.Errorf("get commit %s: %w", commitSHA, err)
	}

	tree, err := commit.Tree()
	if err != nil {
		return "", fmt.Errorf("get tree: %w", err)
	}

	f, err := tree.File(filePath)
	if err != nil {
		return "", fmt.Errorf("get file %s: %w", filePath, err)
	}

	r, err := f.Reader()
	if err != nil {
		return "", fmt.Errorf("read file %s: %w", filePath, err)
	}
	defer r.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		return "", fmt.Errorf("copy file %s: %w", filePath, err)
	}

	return buf.String(), nil
}
