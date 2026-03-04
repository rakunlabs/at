package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/rag"
)

// ─── RAG Chat UI Endpoints ───
//
// These endpoints expose RAG tools directly to the Chat UI without requiring
// the full MCP JSON-RPC protocol.

// ragToolDef describes a RAG tool for the Chat UI.
type ragToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// RAGToolListAPI handles GET /api/v1/mcp/rag-tools.
// Returns the list of available RAG tool definitions.
func (s *Server) RAGToolListAPI(w http.ResponseWriter, r *http.Request) {
	available := s.ragService != nil

	// Return the same tool definitions as the MCP handler, but in
	// the simpler format used by the Chat UI.
	tools := []ragToolDef{
		{
			Name:        "rag_search",
			Description: "Search documents in the RAG knowledge base by semantic similarity. Returns relevant document chunks with full metadata (source, path, repo_url, commit_sha, branch, content_type, score).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "The natural language search query",
					},
					"collection_ids": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Optional list of collection IDs to search. If empty, searches all collections.",
					},
					"num_results": map[string]any{
						"type":        "integer",
						"description": "Maximum number of results to return (default: 5)",
					},
					"score_threshold": map[string]any{
						"type":        "number",
						"description": "Minimum similarity score threshold (0-1). Results below this are filtered out.",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "rag_list_collections",
			Description: "List all available RAG document collections.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "rag_fetch_source",
			Description: "Fetch the original full content of a document by its source URL or path. Use this after rag_search to retrieve the complete original file when chunks are insufficient. Supports HTTP/HTTPS URLs and SSH git sources (resolved from local git cache). Pass commit_sha and repo_url from search result metadata for exact version fetching.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"source": map[string]any{
						"type":        "string",
						"description": "The source URL or path from the rag_search result metadata",
					},
					"max_size": map[string]any{
						"type":        "integer",
						"description": "Maximum content size in bytes to return (default: 102400, max: 1048576). Content is truncated if larger.",
					},
					"commit_sha": map[string]any{
						"type":        "string",
						"description": "The commit SHA from search result metadata. When provided with repo_url, fetches the file at this exact commit.",
					},
					"branch": map[string]any{
						"type":        "string",
						"description": "The branch name from search result metadata. Used for cache lookup when commit_sha is not available.",
					},
					"repo_url": map[string]any{
						"type":        "string",
						"description": "The repository URL from search result metadata. Required for git-based fetching when commit_sha is provided.",
					},
				},
				"required": []string{"source"},
			},
		},
		{
			Name:        "rag_search_and_fetch",
			Description: "Search the RAG knowledge base and automatically fetch the full source files for the top results. Combines rag_search + rag_fetch_source into a single call — returns both search result chunks with metadata and the complete original file contents, deduplicated by source.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "The natural language search query",
					},
					"collection_ids": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Optional list of collection IDs to search. If empty, searches all collections.",
					},
					"num_results": map[string]any{
						"type":        "integer",
						"description": "Maximum number of search results to return (default: 5)",
					},
					"score_threshold": map[string]any{
						"type":        "number",
						"description": "Minimum similarity score threshold (0-1). Results below this are filtered out.",
					},
					"max_sources": map[string]any{
						"type":        "integer",
						"description": "Maximum number of unique source files to fetch (default: 3, max: 5). Sources are deduplicated and fetched in order of best search score.",
					},
					"max_source_size": map[string]any{
						"type":        "integer",
						"description": "Maximum size in bytes per fetched source file (default: 102400). Content is truncated if larger.",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "rag_search_and_fetch_org",
			Description: "Search the RAG knowledge base and return only the full original source files without chunks. Uses semantic search internally to identify relevant files, then fetches and returns the complete original content of each unique source file, deduplicated by source.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "The natural language search query",
					},
					"collection_ids": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Optional list of collection IDs to search. If empty, searches all collections.",
					},
					"num_results": map[string]any{
						"type":        "integer",
						"description": "Maximum number of search results to examine for unique sources (default: 5)",
					},
					"score_threshold": map[string]any{
						"type":        "number",
						"description": "Minimum similarity score threshold (0-1). Results below this are filtered out.",
					},
					"max_sources": map[string]any{
						"type":        "integer",
						"description": "Maximum number of unique source files to fetch (default: 3, max: 5). Sources are deduplicated and fetched in order of best search score.",
					},
					"max_source_size": map[string]any{
						"type":        "integer",
						"description": "Maximum size in bytes per fetched source file (default: 102400). Content is truncated if larger.",
					},
				},
				"required": []string{"query"},
			},
		},
	}

	httpResponseJSON(w, map[string]any{
		"tools":     tools,
		"available": available,
	}, http.StatusOK)
}

// ragCallRequest is the request body for RAGToolCallAPI.
type ragCallRequest struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`

	// Optional auth fields for git operations (fetch_source, search_and_fetch).
	// These reference variable keys, not raw secrets.
	TokenVariable  string `json:"token_variable,omitempty"`
	TokenUser      string `json:"token_user,omitempty"`
	SSHKeyVariable string `json:"ssh_key_variable,omitempty"`
}

// ragCallResponse is the response body for RAGToolCallAPI.
type ragCallResponse struct {
	Result string `json:"result"`
	Error  string `json:"error,omitempty"`
}

// RAGToolCallAPI handles POST /api/v1/mcp/call-rag-tool.
// Dispatches to the appropriate RAG tool executor by name.
func (s *Server) RAGToolCallAPI(w http.ResponseWriter, r *http.Request) {
	if s.ragService == nil {
		httpResponseJSON(w, ragCallResponse{
			Error: "RAG service not configured",
		}, http.StatusOK)
		return
	}

	var req ragCallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	if req.Arguments == nil {
		req.Arguments = make(map[string]any)
	}

	// Resolve git auth from request fields when present.
	var auth *gitAuthResult
	if req.TokenVariable != "" || req.SSHKeyVariable != "" {
		srv := &service.RAGMCPServer{
			Config: service.RAGMCPServerConfig{
				TokenVariable:  req.TokenVariable,
				TokenUser:      req.TokenUser,
				SSHKeyVariable: req.SSHKeyVariable,
			},
		}
		resolved, err := resolveGitAuth(r.Context(), s.variableStore, srv)
		if err != nil {
			slog.Warn("rag tool: failed to resolve git auth", "error", err)
		} else {
			auth = resolved
			if auth.cleanup != nil {
				defer auth.cleanup()
			}
		}
	}

	var result string
	var execErr error

	switch req.Name {
	case "rag_search":
		result, execErr = s.execRAGSearch(r, req.Arguments)
	case "rag_list_collections":
		result, execErr = s.execRAGListCollections(r)
	case "rag_fetch_source":
		result, execErr = s.execRAGFetchSource(r, req.Arguments, auth)
	case "rag_search_and_fetch":
		result, execErr = s.execRAGSearchAndFetch(r, req.Arguments, auth)
	case "rag_search_and_fetch_org":
		result, execErr = s.execRAGFetchSourcesOrg(r, req.Arguments, auth)
	default:
		httpResponse(w, fmt.Sprintf("unknown RAG tool: %q", req.Name), http.StatusBadRequest)
		return
	}

	resp := ragCallResponse{Result: result}
	if execErr != nil {
		resp.Error = execErr.Error()
		slog.Warn("rag tool: execution failed", "tool", req.Name, "error", execErr)
	}

	httpResponseJSON(w, resp, http.StatusOK)
}

// execRAGSearch executes the rag_search tool directly (without MCP protocol).
func (s *Server) execRAGSearch(r *http.Request, args map[string]any) (string, error) {
	var searchReq rag.SearchRequest

	if q, ok := args["query"].(string); ok {
		searchReq.Query = q
	}
	if searchReq.Query == "" {
		return "", fmt.Errorf("query argument is required")
	}

	if ids, ok := args["collection_ids"].([]any); ok {
		for _, v := range ids {
			if s, ok := v.(string); ok {
				searchReq.CollectionIDs = append(searchReq.CollectionIDs, s)
			}
		}
	}

	if n, ok := args["num_results"].(float64); ok {
		searchReq.NumResults = int(n)
	}

	if t, ok := args["score_threshold"].(float64); ok {
		searchReq.ScoreThreshold = float32(t)
	}

	results, err := s.ragService.Search(r.Context(), searchReq)
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}

	if len(results) == 0 {
		return "No results found.", nil
	}

	var text string
	for i, res := range results {
		source, _ := res.Metadata["source"].(string)
		path, _ := res.Metadata["path"].(string)
		repoURL, _ := res.Metadata["repo_url"].(string)
		commitSHA, _ := res.Metadata["commit_sha"].(string)
		branch, _ := res.Metadata["branch"].(string)
		contentType, _ := res.Metadata["content_type"].(string)

		text += fmt.Sprintf("--- Result %d (score: %.4f) ---\n", i+1, res.Score)
		if source != "" {
			text += fmt.Sprintf("source: %s\n", source)
		}
		if path != "" {
			text += fmt.Sprintf("path: %s\n", path)
		}
		if repoURL != "" {
			text += fmt.Sprintf("repo_url: %s\n", repoURL)
		}
		if commitSHA != "" {
			text += fmt.Sprintf("commit_sha: %s\n", commitSHA)
		}
		if branch != "" {
			text += fmt.Sprintf("branch: %s\n", branch)
		}
		if contentType != "" {
			text += fmt.Sprintf("content_type: %s\n", contentType)
		}
		text += fmt.Sprintf("\n%s\n\n", res.Content)
	}

	return text, nil
}

// execRAGListCollections executes the rag_list_collections tool directly.
func (s *Server) execRAGListCollections(r *http.Request) (string, error) {
	collectionsResult, err := s.ragCollectionStore.ListRAGCollections(r.Context(), nil)
	if err != nil {
		return "", fmt.Errorf("list collections failed: %w", err)
	}

	collections := collectionsResult.Data
	if collections == nil {
		collections = []service.RAGCollection{}
	}

	data, err := json.MarshalIndent(collections, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal collections: %w", err)
	}

	return string(data), nil
}

// execRAGFetchSource executes the rag_fetch_source tool directly.
func (s *Server) execRAGFetchSource(r *http.Request, args map[string]any, auth *gitAuthResult) (string, error) {
	source, _ := args["source"].(string)
	if source == "" {
		return "", fmt.Errorf("source argument is required")
	}

	maxSize := 102400
	if n, ok := args["max_size"].(float64); ok && int(n) > 0 {
		maxSize = int(n)
	}
	if maxSize > 1048576 {
		maxSize = 1048576
	}

	// Optional git metadata for precise fetching.
	commitSHA, _ := args["commit_sha"].(string)
	branch, _ := args["branch"].(string)
	repoURL, _ := args["repo_url"].(string)

	// If commitSHA and repoURL are provided, try commit-specific checkout first.
	if commitSHA != "" && repoURL != "" {
		authURL := repoURL
		var envVars []string
		if auth != nil {
			authURL = auth.authURL(repoURL)
			envVars = auth.envVars
		}
		repoDir, err := ensureRepoAtCommitMCP(r.Context(), authURL, repoURL, commitSHA, defaultRAGGitCacheDir, envVars)
		if err == nil {
			_, filePath := splitSourceToRepoAndPath(source)
			if filePath != "" {
				content, readErr := readFileWithLimit(filepath.Join(repoDir, filePath), maxSize)
				if readErr == nil {
					return content, nil
				}
			}
		} else {
			slog.Warn("exec rag_fetch_source: commit-specific checkout failed, falling back",
				"repo_url", repoURL, "commit_sha", commitSHA, "error", err)
		}
	}

	// SSH sources are resolved from the local git cache (populated by git_fetch workflow).
	if isSSHSource(source) {
		content, found := tryLocalGitCache(source, defaultRAGGitCacheDir, maxSize, commitSHA, branch)
		if !found {
			// Fallback: try a shallow clone if the cache doesn't have it yet.
			sshRepoURL, filePath := splitSourceToRepoAndPath(source)
			var envVars []string
			if auth != nil {
				envVars = auth.envVars
			}
			content, found = fallbackCloneAndRead(r.Context(), sshRepoURL, branch, filePath, defaultRAGGitCacheDir, maxSize, commitSHA, envVars)
		}
		if found {
			return content, nil
		}
		return "", fmt.Errorf("source not found in local git cache (SSH sources are resolved from the git cache maintained by the git_fetch workflow)")
	}

	sourceLower := strings.ToLower(source)
	if !strings.HasPrefix(sourceLower, "http://") && !strings.HasPrefix(sourceLower, "https://") {
		return "", fmt.Errorf("only HTTP/HTTPS URLs are supported, got: %s", source)
	}

	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, source, nil)
	if err != nil {
		return "", fmt.Errorf("invalid source URL: %w", err)
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("failed to fetch source: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("source returned HTTP %d", resp.StatusCode)
	}

	limitReader := io.LimitReader(resp.Body, int64(maxSize+1))
	body, err := io.ReadAll(limitReader)
	if err != nil {
		return "", fmt.Errorf("failed to read source content: %w", err)
	}

	truncated := false
	if len(body) > maxSize {
		body = body[:maxSize]
		truncated = true
	}

	text := string(body)
	if truncated {
		text += fmt.Sprintf("\n\n[Content truncated at %d bytes]", maxSize)
	}

	return text, nil
}

// execRAGSearchAndFetch executes the rag_search_and_fetch tool directly (without MCP protocol).
// It searches, then fetches the top unique source files using git metadata for precise fetching,
// falling back to HTTP. Returns combined results.
func (s *Server) execRAGSearchAndFetch(r *http.Request, args map[string]any, auth *gitAuthResult) (string, error) {
	// ── Parse arguments ──
	var searchReq rag.SearchRequest

	if q, ok := args["query"].(string); ok {
		searchReq.Query = q
	}
	if searchReq.Query == "" {
		return "", fmt.Errorf("query argument is required")
	}

	if ids, ok := args["collection_ids"].([]any); ok {
		for _, v := range ids {
			if s, ok := v.(string); ok {
				searchReq.CollectionIDs = append(searchReq.CollectionIDs, s)
			}
		}
	}

	if n, ok := args["num_results"].(float64); ok {
		searchReq.NumResults = int(n)
	}

	if t, ok := args["score_threshold"].(float64); ok {
		searchReq.ScoreThreshold = float32(t)
	}

	maxSources := 3
	if n, ok := args["max_sources"].(float64); ok && int(n) > 0 {
		maxSources = int(n)
	}
	if maxSources > 5 {
		maxSources = 5
	}

	maxSourceSize := 102400 // 100KB default per source file
	if n, ok := args["max_source_size"].(float64); ok && int(n) > 0 {
		maxSourceSize = int(n)
	}
	if maxSourceSize > 1048576 {
		maxSourceSize = 1048576
	}

	// ── Search ──
	results, err := s.ragService.Search(r.Context(), searchReq)
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}

	if len(results) == 0 {
		return "No results found.", nil
	}

	// ── Format search results with full metadata ──
	var text strings.Builder
	text.WriteString("## Search Results\n\n")

	// Collect unique sources in order of best score, along with their git metadata.
	type sourceInfo struct {
		source    string
		repoURL   string
		commitSHA string
		branch    string
		path      string
	}
	seen := make(map[string]bool)
	var uniqueSources []sourceInfo

	for i, res := range results {
		source, _ := res.Metadata["source"].(string)
		path, _ := res.Metadata["path"].(string)
		repoURL, _ := res.Metadata["repo_url"].(string)
		commitSHA, _ := res.Metadata["commit_sha"].(string)
		branch, _ := res.Metadata["branch"].(string)
		contentType, _ := res.Metadata["content_type"].(string)

		fmt.Fprintf(&text, "--- Result %d (score: %.4f) ---\n", i+1, res.Score)
		if source != "" {
			fmt.Fprintf(&text, "source: %s\n", source)
		}
		if path != "" {
			fmt.Fprintf(&text, "path: %s\n", path)
		}
		if repoURL != "" {
			fmt.Fprintf(&text, "repo_url: %s\n", repoURL)
		}
		if commitSHA != "" {
			fmt.Fprintf(&text, "commit_sha: %s\n", commitSHA)
		}
		if branch != "" {
			fmt.Fprintf(&text, "branch: %s\n", branch)
		}
		if contentType != "" {
			fmt.Fprintf(&text, "content_type: %s\n", contentType)
		}
		fmt.Fprintf(&text, "\n%s\n\n", res.Content)

		// Track unique sources for fetching.
		if source != "" && !seen[source] {
			seen[source] = true
			uniqueSources = append(uniqueSources, sourceInfo{
				source:    source,
				repoURL:   repoURL,
				commitSHA: commitSHA,
				branch:    branch,
				path:      path,
			})
		}
	}

	// ── Fetch source files ──
	if len(uniqueSources) > maxSources {
		uniqueSources = uniqueSources[:maxSources]
	}

	if len(uniqueSources) > 0 {
		text.WriteString("## Fetched Sources\n\n")

		for _, si := range uniqueSources {
			// Derive a display label — use the path if available.
			label := si.source
			if si.path != "" {
				label = si.path
			} else if _, filePath := splitSourceToRepoAndPath(si.source); filePath != "" {
				label = filePath
			}

			// Try commit-specific checkout first when metadata is available.
			if si.commitSHA != "" && si.repoURL != "" && si.path != "" {
				authURL := si.repoURL
				var envVars []string
				if auth != nil {
					authURL = auth.authURL(si.repoURL)
					envVars = auth.envVars
				}
				repoDir, err := ensureRepoAtCommitMCP(r.Context(), authURL, si.repoURL, si.commitSHA, defaultRAGGitCacheDir, envVars)
				if err == nil {
					content, readErr := readFileWithLimit(filepath.Join(repoDir, si.path), maxSourceSize)
					if readErr == nil {
						fmt.Fprintf(&text, "=== %s ===\n%s\n\n", label, content)
						continue
					}
				} else {
					slog.Warn("exec rag_search_and_fetch: commit-specific checkout failed, falling back",
						"source", si.source, "commit_sha", si.commitSHA, "error", err)
				}
			}

			// SSH sources are resolved from the local git cache.
			if isSSHSource(si.source) {
				sshRepoURL, filePath := splitSourceToRepoAndPath(si.source)

				content, found := tryLocalGitCache(si.source, defaultRAGGitCacheDir, maxSourceSize, si.commitSHA, si.branch)
				if !found {
					// Fallback: try a shallow clone if the cache doesn't have it yet.
					var envVars []string
					if auth != nil {
						envVars = auth.envVars
					}
					content, found = fallbackCloneAndRead(r.Context(), sshRepoURL, si.branch, filePath, defaultRAGGitCacheDir, maxSourceSize, si.commitSHA, envVars)
				}
				if !found {
					fmt.Fprintf(&text, "=== %s (not found in git cache) ===\n\n", label)
					continue
				}
				fmt.Fprintf(&text, "=== %s ===\n%s\n\n", label, content)
				continue
			}

			sourceLower := strings.ToLower(si.source)
			if !strings.HasPrefix(sourceLower, "http://") && !strings.HasPrefix(sourceLower, "https://") {
				fmt.Fprintf(&text, "=== %s (skipped: not an HTTP URL) ===\n\n", si.source)
				continue
			}

			httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, si.source, nil)
			if err != nil {
				fmt.Fprintf(&text, "=== %s (fetch failed: %s) ===\n\n", label, err.Error())
				continue
			}

			resp, err := http.DefaultClient.Do(httpReq)
			if err != nil {
				fmt.Fprintf(&text, "=== %s (fetch failed: %s) ===\n\n", label, err.Error())
				continue
			}

			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				fmt.Fprintf(&text, "=== %s (fetch failed: HTTP %d) ===\n\n", label, resp.StatusCode)
				continue
			}

			limitReader := io.LimitReader(resp.Body, int64(maxSourceSize+1))
			body, err := io.ReadAll(limitReader)
			resp.Body.Close()
			if err != nil {
				fmt.Fprintf(&text, "=== %s (read failed: %s) ===\n\n", label, err.Error())
				continue
			}

			content := string(body)
			if len(body) > maxSourceSize {
				content = string(body[:maxSourceSize])
				content += fmt.Sprintf("\n\n[Content truncated at %d bytes]", maxSourceSize)
			}

			fmt.Fprintf(&text, "=== %s ===\n%s\n\n", label, content)
		}
	}

	return text.String(), nil
}

// execRAGFetchSourcesOrg executes the rag_search_and_fetch_org tool directly (without MCP protocol).
// It searches for relevant chunks, then returns only the full original source files — no chunk content.
func (s *Server) execRAGFetchSourcesOrg(r *http.Request, args map[string]any, auth *gitAuthResult) (string, error) {
	// ── Parse arguments ──
	var searchReq rag.SearchRequest

	if q, ok := args["query"].(string); ok {
		searchReq.Query = q
	}
	if searchReq.Query == "" {
		return "", fmt.Errorf("query argument is required")
	}

	if ids, ok := args["collection_ids"].([]any); ok {
		for _, v := range ids {
			if s, ok := v.(string); ok {
				searchReq.CollectionIDs = append(searchReq.CollectionIDs, s)
			}
		}
	}

	if n, ok := args["num_results"].(float64); ok {
		searchReq.NumResults = int(n)
	}

	if t, ok := args["score_threshold"].(float64); ok {
		searchReq.ScoreThreshold = float32(t)
	}

	maxSources := 3
	if n, ok := args["max_sources"].(float64); ok && int(n) > 0 {
		maxSources = int(n)
	}
	if maxSources > 5 {
		maxSources = 5
	}

	maxSourceSize := 102400 // 100KB default per source file
	if n, ok := args["max_source_size"].(float64); ok && int(n) > 0 {
		maxSourceSize = int(n)
	}
	if maxSourceSize > 1048576 {
		maxSourceSize = 1048576
	}

	// ── Search ──
	results, err := s.ragService.Search(r.Context(), searchReq)
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}

	if len(results) == 0 {
		return "No results found.", nil
	}

	// ── Collect unique sources (no chunk output) ──
	type sourceInfo struct {
		source    string
		repoURL   string
		commitSHA string
		branch    string
		path      string
	}
	seen := make(map[string]bool)
	var uniqueSources []sourceInfo

	for _, res := range results {
		source, _ := res.Metadata["source"].(string)
		if source == "" || seen[source] {
			continue
		}
		seen[source] = true

		path, _ := res.Metadata["path"].(string)
		repoURL, _ := res.Metadata["repo_url"].(string)
		commitSHA, _ := res.Metadata["commit_sha"].(string)
		branch, _ := res.Metadata["branch"].(string)

		uniqueSources = append(uniqueSources, sourceInfo{
			source:    source,
			repoURL:   repoURL,
			commitSHA: commitSHA,
			branch:    branch,
			path:      path,
		})
	}

	if len(uniqueSources) > maxSources {
		uniqueSources = uniqueSources[:maxSources]
	}

	// ── Fetch source files ──
	var text strings.Builder

	for _, si := range uniqueSources {
		label := si.source
		if si.path != "" {
			label = si.path
		} else if _, filePath := splitSourceToRepoAndPath(si.source); filePath != "" {
			label = filePath
		}

		// Try commit-specific checkout first when metadata is available.
		if si.commitSHA != "" && si.repoURL != "" && si.path != "" {
			authURL := si.repoURL
			var envVars []string
			if auth != nil {
				authURL = auth.authURL(si.repoURL)
				envVars = auth.envVars
			}
			repoDir, err := ensureRepoAtCommitMCP(r.Context(), authURL, si.repoURL, si.commitSHA, defaultRAGGitCacheDir, envVars)
			if err == nil {
				content, readErr := readFileWithLimit(filepath.Join(repoDir, si.path), maxSourceSize)
				if readErr == nil {
					fmt.Fprintf(&text, "=== %s ===\n%s\n\n", label, content)
					continue
				}
			} else {
				slog.Warn("exec rag_search_and_fetch_org: commit-specific checkout failed, falling back",
					"source", si.source, "commit_sha", si.commitSHA, "error", err)
			}
		}

		// SSH sources are resolved from the local git cache.
		if isSSHSource(si.source) {
			sshRepoURL, filePath := splitSourceToRepoAndPath(si.source)

			content, found := tryLocalGitCache(si.source, defaultRAGGitCacheDir, maxSourceSize, si.commitSHA, si.branch)
			if !found {
				var envVars []string
				if auth != nil {
					envVars = auth.envVars
				}
				content, found = fallbackCloneAndRead(r.Context(), sshRepoURL, si.branch, filePath, defaultRAGGitCacheDir, maxSourceSize, si.commitSHA, envVars)
			}
			if !found {
				fmt.Fprintf(&text, "=== %s (not found in git cache) ===\n\n", label)
				continue
			}
			fmt.Fprintf(&text, "=== %s ===\n%s\n\n", label, content)
			continue
		}

		sourceLower := strings.ToLower(si.source)
		if !strings.HasPrefix(sourceLower, "http://") && !strings.HasPrefix(sourceLower, "https://") {
			fmt.Fprintf(&text, "=== %s (skipped: not an HTTP URL) ===\n\n", si.source)
			continue
		}

		httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, si.source, nil)
		if err != nil {
			fmt.Fprintf(&text, "=== %s (fetch failed: %s) ===\n\n", label, err.Error())
			continue
		}

		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			fmt.Fprintf(&text, "=== %s (fetch failed: %s) ===\n\n", label, err.Error())
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			fmt.Fprintf(&text, "=== %s (fetch failed: HTTP %d) ===\n\n", label, resp.StatusCode)
			continue
		}

		limitReader := io.LimitReader(resp.Body, int64(maxSourceSize+1))
		body, err := io.ReadAll(limitReader)
		resp.Body.Close()
		if err != nil {
			fmt.Fprintf(&text, "=== %s (read failed: %s) ===\n\n", label, err.Error())
			continue
		}

		content := string(body)
		if len(body) > maxSourceSize {
			content = string(body[:maxSourceSize])
			content += fmt.Sprintf("\n\n[Content truncated at %d bytes]", maxSourceSize)
		}

		fmt.Fprintf(&text, "=== %s ===\n%s\n\n", label, content)
	}

	result := text.String()
	if result == "" {
		return "No source files could be fetched.", nil
	}

	return result, nil
}

// ─── MCP Response Helpers ───

func mcpResult(w http.ResponseWriter, id int, result any) {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		mcpError(w, id, -32603, fmt.Sprintf("marshal result: %v", err))
		return
	}

	resp := service.MCPResponse{
		Jsonrpc: "2.0",
		ID:      id,
		Result:  resultJSON,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

func mcpError(w http.ResponseWriter, id int, code int, message string) {
	resp := service.MCPResponse{
		Jsonrpc: "2.0",
		ID:      id,
		Error: &service.MCPError{
			Code:    code,
			Message: message,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}
