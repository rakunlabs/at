package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// ragSearchNode performs a similarity search against RAG collections.
// Optionally enriches results with full original file content by cloning
// the source repository at the exact commit recorded in chunk metadata.
//
// Configuration (node.Data):
//
//	collection_ids  []string  — collections to search (empty = all)
//	num_results     float64   — max results to return (default 5)
//	score_threshold float64   — minimum similarity score 0-1 (default 0)
//
// Git enrichment (optional — set repo_url or token/ssh_key to enable):
//
//	repo_url  string  — git repository URL override (if empty, uses metadata repo_url)
//	token     string  — HTTPS auth token (supports Go templates with {{ getVar "key" }})
//	ssh_key   string  — SSH private key content (supports Go templates)
//	cache_dir string  — root directory for cloned repos (default "/tmp/at-git-cache")
//	timeout   float64 — git operation timeout in seconds (default 120)
//
// When git enrichment is enabled, for each result that has repo_url,
// commit_sha, and path in its metadata the node will:
//  1. Clone the repo into <cache_dir>/<hash(repo_url, commit_sha)> (if not cached)
//  2. Checkout the exact commit (detached HEAD)
//  3. Read the full original file at the recorded path
//  4. Add "original_content" field to the result
//
// Inputs:
//
//	query  string — the search query text (required)
//
// Outputs:
//
//	results  []RAGSearchResult — the search hits (with optional original_content)
//	text     string            — concatenated result content (convenience)
type ragSearchNode struct {
	collectionIDs  []string
	numResults     int
	scoreThreshold float32

	// Git enrichment config.
	repoURL   string
	token     string
	tokenUser string
	sshKey    string
	cacheDir  string
	timeout   time.Duration
}

const (
	defaultSearchTimeout = 120 * time.Second
	maxSearchTimeout     = 600 * time.Second
)

func init() {
	workflow.RegisterNodeType("rag_search", newRAGSearchNode)
}

func newRAGSearchNode(node service.WorkflowNode) (workflow.Noder, error) {
	n := &ragSearchNode{
		numResults: 5,
		cacheDir:   defaultGitCacheDir,
		timeout:    defaultSearchTimeout,
	}

	// Parse collection_ids.
	if raw, ok := node.Data["collection_ids"]; ok {
		switch v := raw.(type) {
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok && s != "" {
					n.collectionIDs = append(n.collectionIDs, s)
				}
			}
		case []string:
			n.collectionIDs = v
		case string:
			// Try JSON array.
			if v != "" {
				var ids []string
				if err := json.Unmarshal([]byte(v), &ids); err == nil {
					n.collectionIDs = ids
				} else {
					n.collectionIDs = []string{v}
				}
			}
		}
	}

	// Parse num_results.
	if raw, ok := node.Data["num_results"]; ok {
		switch v := raw.(type) {
		case float64:
			if v > 0 {
				n.numResults = int(v)
			}
		case int:
			if v > 0 {
				n.numResults = v
			}
		case json.Number:
			if i, err := v.Int64(); err == nil && i > 0 {
				n.numResults = int(i)
			}
		}
	}

	// Parse score_threshold.
	if raw, ok := node.Data["score_threshold"]; ok {
		switch v := raw.(type) {
		case float64:
			n.scoreThreshold = float32(v)
		case json.Number:
			if f, err := v.Float64(); err == nil {
				n.scoreThreshold = float32(f)
			}
		}
	}

	// Parse git enrichment config.
	if v, ok := node.Data["repo_url"].(string); ok {
		n.repoURL = strings.TrimSpace(v)
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
		if n.timeout > maxSearchTimeout {
			n.timeout = maxSearchTimeout
		}
	}

	return n, nil
}

func (n *ragSearchNode) Type() string { return "rag_search" }

func (n *ragSearchNode) Meta() workflow.NodeMeta {
	return workflow.NodeMeta{
		Type:        "rag_search",
		Label:       "RAG Search",
		Category:    "knowledge",
		Description: "Search RAG collections for relevant documents",
		Inputs: []workflow.PortMeta{
			{Name: "query", Type: workflow.PortTypeText, Required: true, Accept: []workflow.PortType{workflow.PortTypeData}, Label: "Query", Position: "left"},
		},
		Outputs: []workflow.PortMeta{
			{Name: "results", Type: workflow.PortTypeData, Label: "Results", Position: "right"},
			{Name: "text", Type: workflow.PortTypeText, Label: "Text", Position: "right"},
		},
		Fields: []workflow.FieldMeta{
			{Name: "label", Type: "string", Required: true, Description: "Display name"},
			{Name: "collection_id", Type: "string", Required: true, Description: "RAG collection ID"},
			{Name: "top_k", Type: "number", Default: 5, Description: "Number of results"},
			{Name: "min_score", Type: "number", Default: 0.7, Description: "Minimum similarity score"},
		},
		Color: "orange",
	}
}

func (n *ragSearchNode) Validate(_ context.Context, reg *workflow.Registry) error {
	if reg.RAGSearch == nil {
		return fmt.Errorf("rag_search: RAG is not configured")
	}
	return nil
}

// gitEnrichmentEnabled returns true if any git-related config is set,
// meaning the user wants original file content from the source repo.
func (n *ragSearchNode) gitEnrichmentEnabled() bool {
	return n.repoURL != "" || n.token != "" || n.sshKey != ""
}

// Run executes the RAG search. It reads the query from inputs and returns
// both structured results and a concatenated text output. When git
// enrichment is configured, results are enriched with original_content.
func (n *ragSearchNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	// Extract query from inputs.
	query, _ := inputs["query"].(string)
	if query == "" {
		// Try "input" port (common wiring).
		query, _ = inputs["input"].(string)
	}
	if query == "" {
		// Try nested data map.
		if data, ok := inputs["data"].(map[string]any); ok {
			query, _ = data["query"].(string)
		}
	}
	if query == "" {
		return nil, fmt.Errorf("rag_search: query is required (pass via 'query' or 'input' port)")
	}

	// Allow runtime override of collection_ids from inputs.
	collectionIDs := n.collectionIDs
	if raw, ok := inputs["collection_ids"]; ok {
		switch v := raw.(type) {
		case []any:
			override := make([]string, 0, len(v))
			for _, item := range v {
				if s, ok := item.(string); ok && s != "" {
					override = append(override, s)
				}
			}
			if len(override) > 0 {
				collectionIDs = override
			}
		case []string:
			if len(v) > 0 {
				collectionIDs = v
			}
		}
	}

	results, err := reg.RAGSearch(ctx, query, collectionIDs, n.numResults, n.scoreThreshold)
	if err != nil {
		return nil, fmt.Errorf("rag_search: %w", err)
	}

	// Convert results to []any for JSON-friendly output.
	resultsAny := make([]any, len(results))
	for i, r := range results {
		resultsAny[i] = map[string]any{
			"content":       r.Content,
			"metadata":      r.Metadata,
			"score":         r.Score,
			"collection_id": r.CollectionID,
		}
	}

	// Enrich results with original file content if git config is present.
	if n.gitEnrichmentEnabled() && len(resultsAny) > 0 {
		n.enrichWithOriginalContent(ctx, reg, resultsAny)
	}

	// Build concatenated text for easy downstream consumption.
	// If original_content is available, prefer it over the chunk content.
	var text string
	for i, r := range resultsAny {
		if i > 0 {
			text += "\n\n---\n\n"
		}
		m, _ := r.(map[string]any)
		if oc, ok := m["original_content"].(string); ok && oc != "" {
			text += oc
		} else if c, ok := m["content"].(string); ok {
			text += c
		}
	}

	return workflow.NewResult(map[string]any{
		"results": resultsAny,
		"text":    text,
	}), nil
}

// enrichWithOriginalContent reads the full original files for each search
// result using go-git's in-process object store to read at the exact commit
// recorded in chunk metadata. No git checkout — reads directly from the
// object database of the branch-based cache maintained by git_fetch.
func (n *ragSearchNode) enrichWithOriginalContent(ctx context.Context, reg *workflow.Registry, results []any) {
	// Render template fields for auth.
	funcs := varFuncMap(reg)

	token, _ := renderField(n.token, nil, funcs)
	tokenUser, _ := renderField(n.tokenUser, nil, funcs)
	sshKey, _ := renderField(n.sshKey, nil, funcs)

	// Set up SSH environment if needed.
	var envVars []string
	var sshCleanup func()
	if sshKey != "" {
		tmpFile, err := os.CreateTemp("", "at-git-ssh-*")
		if err != nil {
			slog.Warn("rag_search: failed to create ssh key temp file", "error", err)
			return
		}
		sshCleanup = func() { os.Remove(tmpFile.Name()) }
		if _, err := tmpFile.WriteString(sshKey + "\n"); err != nil {
			tmpFile.Close()
			sshCleanup()
			slog.Warn("rag_search: failed to write ssh key", "error", err)
			return
		}
		tmpFile.Close()
		if err := os.Chmod(tmpFile.Name(), 0o600); err != nil {
			sshCleanup()
			slog.Warn("rag_search: failed to chmod ssh key", "error", err)
			return
		}
		envVars = append(envVars, fmt.Sprintf("GIT_SSH_COMMAND=ssh -i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null", tmpFile.Name()))
	}
	if sshCleanup != nil {
		defer sshCleanup()
	}

	execCtx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()

	for _, r := range results {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}

		meta, _ := m["metadata"].(map[string]any)
		if meta == nil {
			continue
		}

		repoURL, _ := meta["repo_url"].(string)
		commitSHA, _ := meta["commit_sha"].(string)
		filePath, _ := meta["path"].(string)
		branch, _ := meta["branch"].(string)

		// Use config repo_url as override if set.
		if n.repoURL != "" {
			repoURL = n.repoURL
		}

		if repoURL == "" || commitSHA == "" || filePath == "" {
			continue
		}

		// Build auth URL for fallback clone.
		authURL := repoURL
		if token != "" {
			if !strings.HasPrefix(repoURL, "https://") {
				authURL = sshToHTTPS(repoURL)
			}
			if strings.HasPrefix(authURL, "https://") {
				authURL = injectHTTPSToken(authURL, token, tokenUser)
			}
		}

		// Read file at exact commit using branch-based cache + go-git.
		content, err := n.readFileFromCache(execCtx, repoURL, authURL, commitSHA, filePath, branch, envVars)
		if err != nil {
			slog.Warn("rag_search: failed to read original file",
				"repo_url", repoURL,
				"commit_sha", commitSHA,
				"path", filePath,
				"error", err,
			)
			continue
		}

		m["original_content"] = content
	}
}

// readFileFromCache reads a file at a specific commit from the local git cache.
// It scans branch-based cache directories and uses go-git to read from the
// object store — no checkout, no working tree modification.
//
// Strategy:
//  1. Try the known branch cache dir (if branch is available)
//  2. Try common branches (main, master, develop)
//  3. Scan all cache dirs
//  4. Fallback: clone at branch HEAD, then read via go-git
func (n *ragSearchNode) readFileFromCache(ctx context.Context, repoURL, authURL, commitSHA, filePath, branch string, envVars []string) (string, error) {
	// tryDir attempts gitReadFileAtCommit on a cache directory.
	tryDir := func(dir string) (string, bool) {
		repoDir := filepath.Join(n.cacheDir, dir)
		if _, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil {
			return "", false
		}
		content, err := gitReadFileAtCommit(repoDir, commitSHA, filePath)
		if err == nil {
			return content, true
		}
		return "", false
	}

	// Strategy 1: Try the exact branch cache dir.
	if branch != "" {
		dirName := hashRepoKey(repoURL, branch)
		if content, ok := tryDir(dirName); ok {
			return content, nil
		}
	}

	// Strategy 2: Try common branches.
	for _, b := range []string{"main", "master", "develop"} {
		if b == branch {
			continue // already tried above
		}
		dirName := hashRepoKey(repoURL, b)
		if content, ok := tryDir(dirName); ok {
			return content, nil
		}
	}

	// Strategy 3: Scan all cache directories.
	entries, err := os.ReadDir(n.cacheDir)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if content, ok := tryDir(entry.Name()); ok {
				return content, nil
			}
		}
	}

	// Strategy 4: Clone at branch HEAD and read via go-git.
	cloneBranch := branch
	if cloneBranch == "" {
		cloneBranch = "main"
	}

	dirName := hashRepoKey(repoURL, cloneBranch)
	repoDir := filepath.Join(n.cacheDir, dirName)

	// Only clone if the directory doesn't exist yet.
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil {
		if err := os.MkdirAll(n.cacheDir, 0o755); err != nil {
			return "", fmt.Errorf("create cache dir: %w", err)
		}

		if err := runGit(ctx, n.cacheDir, envVars, "clone", "--single-branch", "--branch", cloneBranch, authURL, dirName); err != nil {
			_ = os.RemoveAll(repoDir)
			return "", fmt.Errorf("clone %s branch %s: %w", repoURL, cloneBranch, err)
		}
	}

	content, err := gitReadFileAtCommit(repoDir, commitSHA, filePath)
	if err != nil {
		return "", fmt.Errorf("read file at commit: %w", err)
	}

	return content, nil
}
