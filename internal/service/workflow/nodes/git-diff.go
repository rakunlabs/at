package nodes

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// gitDiffNode compares the current HEAD of a git repository against the last
// known sync SHA, identifies changed/added/deleted files matching a pattern,
// reads their content, and outputs the result for downstream processing
// (typically a rag_ingest node).
//
// It expects to receive its inputs from an upstream git_fetch node.
//
// Configuration (node.Data):
//
//	file_pattern        string — glob pattern for files to include (default "*.md")
//	variable_key_prefix string — prefix for variable store keys (default "rag_sync")
//
// Inputs (from git_fetch):
//
//	repo_dir     string — local filesystem path of the repository
//	commit_sha   string — HEAD commit SHA
//	repo_url     string — the repository URL
//	branch       string — the branch that was fetched
//	is_new_clone bool   — true if this was a fresh clone
//
// Outputs:
//
//	files         []map[string]any — changed/added files [{path, content, status}]
//	deleted_files []string         — files removed since last sync
//	commit_sha    string           — HEAD commit SHA
//	repo_url      string           — the repository URL
//	variable_key  string           — the variable key used to track sync state
type gitDiffNode struct {
	filePattern       string
	variableKeyPrefix string
}

const (
	defaultFilePattern  = "*.md"
	defaultVarKeyPrefix = "rag_sync"
)

func init() {
	workflow.RegisterNodeType("git_diff", newGitDiffNode)
}

func newGitDiffNode(node service.WorkflowNode) (workflow.Noder, error) {
	n := &gitDiffNode{
		filePattern:       defaultFilePattern,
		variableKeyPrefix: defaultVarKeyPrefix,
	}

	if v, ok := node.Data["file_pattern"].(string); ok && v != "" {
		n.filePattern = strings.TrimSpace(v)
	}
	if v, ok := node.Data["variable_key_prefix"].(string); ok && v != "" {
		n.variableKeyPrefix = strings.TrimSpace(v)
	}

	return n, nil
}

func (n *gitDiffNode) Type() string { return "git_diff" }

func (n *gitDiffNode) Validate(_ context.Context, _ *workflow.Registry) error {
	return nil
}

func (n *gitDiffNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	// Parse required inputs from upstream git_fetch node.
	repoDir, _ := inputs["repo_dir"].(string)
	if repoDir == "" {
		return nil, fmt.Errorf("git_diff: 'repo_dir' input is required (connect to a git_fetch node)")
	}

	commitSHA, _ := inputs["commit_sha"].(string)
	if commitSHA == "" {
		return nil, fmt.Errorf("git_diff: 'commit_sha' input is required")
	}

	repoURL, _ := inputs["repo_url"].(string)
	branch, _ := inputs["branch"].(string)
	isNewClone, _ := inputs["is_new_clone"].(bool)

	// Compute variable key for sync state tracking.
	repoHash := hashRepoKey(repoURL, branch)
	variableKey := fmt.Sprintf("%s_%s", n.variableKeyPrefix, repoHash)

	// Lookup last synced commit SHA from the RAG state store.
	var lastSyncSHA string
	if reg.RAGStateLookup != nil {
		state, err := reg.RAGStateLookup(ctx, variableKey)
		if err == nil && state != nil {
			lastSyncSHA = state.Value
		}
		// Not found is fine — means first sync.
	}

	// Determine changed, added, and deleted files.
	var changedFiles []map[string]any
	var deletedFiles []string
	var err error

	if isNewClone || lastSyncSHA == "" {
		// First sync: all matching files are "added".
		changedFiles, err = collectAllFiles(repoDir, n.filePattern)
		if err != nil {
			return nil, fmt.Errorf("git_diff: collect files: %w", err)
		}
	} else if lastSyncSHA != commitSHA {
		// Diff since last sync.
		changedFiles, deletedFiles, err = diffFiles(ctx, repoDir, nil, lastSyncSHA, commitSHA, n.filePattern)
		if err != nil {
			// If diff fails (e.g. SHA was garbage-collected), fall back to full sync.
			slog.Warn("git_diff: diff failed, falling back to full sync", "error", err)
			changedFiles, err = collectAllFiles(repoDir, n.filePattern)
			if err != nil {
				return nil, fmt.Errorf("git_diff: collect files (fallback): %w", err)
			}
		}
	}
	// If lastSyncSHA == commitSHA, no changes — changedFiles and deletedFiles remain empty.

	// Build output slices as []any for JSON output.
	deletedAny := make([]any, len(deletedFiles))
	for i, f := range deletedFiles {
		deletedAny[i] = f
	}

	filesAny := make([]any, len(changedFiles))
	for i, f := range changedFiles {
		filesAny[i] = f
	}

	return workflow.NewResult(map[string]any{
		"files":         filesAny,
		"deleted_files": deletedAny,
		"commit_sha":    commitSHA,
		"repo_url":      repoURL,
		"variable_key":  variableKey,
	}), nil
}

// ─── File Helpers ───

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
				slog.Warn("git_diff: cannot read changed file", "path", filePath, "error", readErr)
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
