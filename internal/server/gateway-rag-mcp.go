package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/rag"
)

// GatewayRAGMCPHandler handles MCP protocol requests at /gateway/v1/mcp/rag/{name}.
// Each named endpoint is configured with specific collections, tools, and fetch options.
// Auth uses the same Bearer token mechanism as the gateway chat completions endpoint.
func (s *Server) GatewayRAGMCPHandler(w http.ResponseWriter, r *http.Request) {
	if s.ragService == nil {
		httpResponse(w, "rag service not configured", http.StatusServiceUnavailable)
		return
	}

	if s.ragMCPServerStore == nil {
		httpResponse(w, "rag mcp server store not configured", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodPost {
		httpResponse(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Authenticate.
	auth, errMsg := s.authenticateRequest(r)
	if auth == nil {
		httpResponse(w, errMsg, http.StatusUnauthorized)
		return
	}

	// Look up the named MCP server config.
	name := r.PathValue("name")
	if name == "" {
		httpResponse(w, "mcp server name is required", http.StatusBadRequest)
		return
	}

	// Check token scoping for RAG MCP servers.
	if auth.token != nil && len(auth.token.AllowedRAGMCPs) > 0 {
		if !slices.Contains(auth.token.AllowedRAGMCPs, name) {
			httpResponse(w, fmt.Sprintf("token does not have access to RAG MCP server %q", name), http.StatusForbidden)
			return
		}
	}

	mcpServer, err := s.ragMCPServerStore.GetRAGMCPServerByName(r.Context(), name)
	if err != nil {
		slog.Error("get rag mcp server failed", "name", name, "error", err)
		httpResponse(w, "internal error looking up MCP server", http.StatusInternalServerError)
		return
	}
	if mcpServer == nil {
		httpResponse(w, fmt.Sprintf("RAG MCP server %q not found", name), http.StatusNotFound)
		return
	}

	// Parse the JSON-RPC request.
	var req service.MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	// Route by method.
	switch req.Method {
	case "initialize":
		s.gwMCPInitialize(w, req, mcpServer)
	case "notifications/initialized":
		w.WriteHeader(http.StatusOK)
	case "tools/list":
		s.gwMCPListTools(w, req, mcpServer)
	case "tools/call":
		s.gwMCPCallTool(w, r, req, mcpServer)
	default:
		mcpError(w, req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

// ─── Gateway MCP Handlers ───

func (s *Server) gwMCPInitialize(w http.ResponseWriter, req service.MCPRequest, srv *service.RAGMCPServer) {
	description := srv.Config.Description
	if description == "" {
		description = fmt.Sprintf("RAG MCP server: %s", srv.Name)
	}

	result := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]string{
			"name":    fmt.Sprintf("at-rag-%s", srv.Name),
			"version": "1.0.0",
		},
	}

	mcpResult(w, req.ID, result)
}

func (s *Server) gwMCPListTools(w http.ResponseWriter, req service.MCPRequest, srv *service.RAGMCPServer) {
	enabledTools := srv.Config.EnabledTools
	if len(enabledTools) == 0 {
		// Default: enable all tools.
		enabledTools = []string{"rag_search", "rag_list_collections", "rag_fetch_source", "rag_search_and_fetch"}
	}

	var tools []service.Tool

	for _, toolName := range enabledTools {
		switch toolName {
		case "rag_search":
			tools = append(tools, service.Tool{
				Name:        "rag_search",
				Description: "Search documents in the RAG knowledge base by semantic similarity. Returns relevant document chunks with full metadata (source, path, repo_url, commit_sha, content_type, score).",
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
							"description": "Optional list of collection IDs to search. If empty, searches all collections configured for this MCP server.",
						},
						"num_results": map[string]any{
							"type":        "integer",
							"description": fmt.Sprintf("Maximum number of results to return (default: %d)", gwDefaultNumResults(srv.Config.DefaultNumResults)),
						},
						"score_threshold": map[string]any{
							"type":        "number",
							"description": "Minimum similarity score threshold (0-1). Results below this are filtered out.",
						},
					},
					"required": []string{"query"},
				},
			})
		case "rag_list_collections":
			tools = append(tools, service.Tool{
				Name:        "rag_list_collections",
				Description: "List available RAG document collections that this MCP server can search.",
				InputSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			})
		case "rag_fetch_source":
			tools = append(tools, service.Tool{
				Name:        "rag_fetch_source",
				Description: "Fetch the original full content of a document by its source URL or path. Supports HTTP/HTTPS URLs and local git cache. Use this after rag_search to retrieve the complete original file when chunks are insufficient.",
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
					},
					"required": []string{"source"},
				},
			})
		case "rag_search_and_fetch":
			tools = append(tools, service.Tool{
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
							"description": "Optional list of collection IDs to search. If empty, searches all collections configured for this MCP server.",
						},
						"num_results": map[string]any{
							"type":        "integer",
							"description": fmt.Sprintf("Maximum number of search results to return (default: %d)", gwDefaultNumResults(srv.Config.DefaultNumResults)),
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
			})
		}
	}

	mcpResult(w, req.ID, service.ListToolsResult{Tools: tools})
}

func (s *Server) gwMCPCallTool(w http.ResponseWriter, r *http.Request, req service.MCPRequest, srv *service.RAGMCPServer) {
	paramsJSON, err := json.Marshal(req.Params)
	if err != nil {
		mcpError(w, req.ID, -32602, fmt.Sprintf("invalid params: %v", err))
		return
	}

	var params service.CallToolParams
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		mcpError(w, req.ID, -32602, fmt.Sprintf("invalid params: %v", err))
		return
	}

	// Check if the tool is enabled for this MCP server.
	enabledTools := srv.Config.EnabledTools
	if len(enabledTools) == 0 {
		enabledTools = []string{"rag_search", "rag_list_collections", "rag_fetch_source", "rag_search_and_fetch"}
	}
	if !slices.Contains(enabledTools, params.Name) {
		mcpError(w, req.ID, -32602, fmt.Sprintf("tool %q is not enabled for this MCP server", params.Name))
		return
	}

	switch params.Name {
	case "rag_search":
		s.gwMCPSearch(w, r, req.ID, params.Arguments, srv)
	case "rag_list_collections":
		s.gwMCPListCollections(w, r, req.ID, srv)
	case "rag_fetch_source":
		s.gwMCPFetchSource(w, r, req.ID, params.Arguments, srv)
	case "rag_search_and_fetch":
		s.gwMCPSearchAndFetch(w, r, req.ID, params.Arguments, srv)
	default:
		mcpError(w, req.ID, -32602, fmt.Sprintf("unknown tool: %s", params.Name))
	}
}

// ─── Tool Implementations ───

func (s *Server) gwMCPSearch(w http.ResponseWriter, r *http.Request, id int, args map[string]any, srv *service.RAGMCPServer) {
	var searchReq rag.SearchRequest

	if q, ok := args["query"].(string); ok {
		searchReq.Query = q
	}
	if searchReq.Query == "" {
		mcpError(w, id, -32602, "query argument is required")
		return
	}

	// Parse collection_ids from args, but scope to server's configured collections.
	if ids, ok := args["collection_ids"].([]any); ok {
		for _, v := range ids {
			if s, ok := v.(string); ok {
				searchReq.CollectionIDs = append(searchReq.CollectionIDs, s)
			}
		}
	}

	// If no collection_ids provided by the caller, default to the MCP server's configured collections.
	if len(searchReq.CollectionIDs) == 0 && len(srv.Config.CollectionIDs) > 0 {
		searchReq.CollectionIDs = srv.Config.CollectionIDs
	}

	// If caller provided collection_ids, scope them to the server's allowed set (if configured).
	if len(searchReq.CollectionIDs) > 0 && len(srv.Config.CollectionIDs) > 0 {
		var scoped []string
		for _, id := range searchReq.CollectionIDs {
			if slices.Contains(srv.Config.CollectionIDs, id) {
				scoped = append(scoped, id)
			}
		}
		searchReq.CollectionIDs = scoped
		if len(scoped) == 0 {
			mcpError(w, id, -32602, "none of the requested collection_ids are available on this MCP server")
			return
		}
	}

	if n, ok := args["num_results"].(float64); ok {
		searchReq.NumResults = int(n)
	}
	if searchReq.NumResults <= 0 {
		searchReq.NumResults = gwDefaultNumResults(srv.Config.DefaultNumResults)
	}

	if t, ok := args["score_threshold"].(float64); ok {
		searchReq.ScoreThreshold = float32(t)
	}

	results, err := s.ragService.Search(r.Context(), searchReq)
	if err != nil {
		slog.Error("gateway mcp rag_search failed", "mcp_server", srv.Name, "error", err)
		mcpError(w, id, -32000, fmt.Sprintf("search failed: %v", err))
		return
	}

	// Format results with full metadata for agents.
	var text string
	if len(results) == 0 {
		text = "No results found."
	} else {
		for i, res := range results {
			source, _ := res.Metadata["source"].(string)
			path, _ := res.Metadata["path"].(string)
			repoURL, _ := res.Metadata["repo_url"].(string)
			commitSHA, _ := res.Metadata["commit_sha"].(string)
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
			if contentType != "" {
				text += fmt.Sprintf("content_type: %s\n", contentType)
			}
			text += fmt.Sprintf("\n%s\n\n", res.Content)
		}
	}

	mcpResult(w, id, service.CallToolResult{
		Content: []service.ToolContent{
			{Type: "text", Text: text},
		},
	})
}

func (s *Server) gwMCPListCollections(w http.ResponseWriter, r *http.Request, id int, srv *service.RAGMCPServer) {
	collectionsResult, err := s.ragCollectionStore.ListRAGCollections(r.Context(), nil)
	if err != nil {
		slog.Error("gateway mcp rag_list_collections failed", "mcp_server", srv.Name, "error", err)
		mcpError(w, id, -32000, fmt.Sprintf("list collections failed: %v", err))
		return
	}

	collections := collectionsResult.Data
	if collections == nil {
		collections = []service.RAGCollection{}
	}

	// Scope to the MCP server's configured collections if any.
	if len(srv.Config.CollectionIDs) > 0 {
		var scoped []service.RAGCollection
		for _, c := range collections {
			if slices.Contains(srv.Config.CollectionIDs, c.ID) {
				scoped = append(scoped, c)
			}
		}
		collections = scoped
	}

	// Return a simplified view for agents.
	type collectionInfo struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
	}

	var infos []collectionInfo
	for _, c := range collections {
		infos = append(infos, collectionInfo{
			ID:          c.ID,
			Name:        c.Name,
			Description: c.Config.Description,
		})
	}

	data, _ := json.MarshalIndent(infos, "", "  ")

	mcpResult(w, id, service.CallToolResult{
		Content: []service.ToolContent{
			{Type: "text", Text: string(data)},
		},
	})
}

func (s *Server) gwMCPFetchSource(w http.ResponseWriter, r *http.Request, id int, args map[string]any, srv *service.RAGMCPServer) {
	source, _ := args["source"].(string)
	if source == "" {
		mcpError(w, id, -32602, "source argument is required")
		return
	}

	maxSize := 102400 // 100KB default
	if n, ok := args["max_size"].(float64); ok && int(n) > 0 {
		maxSize = int(n)
	}
	if maxSize > 1048576 { // 1MB hard cap
		maxSize = 1048576
	}

	content, err := fetchSourceContent(r.Context(), source, srv, maxSize)
	if err != nil {
		slog.Error("gateway mcp rag_fetch_source failed", "source", source, "mcp_server", srv.Name, "error", err)
		mcpError(w, id, -32000, err.Error())
		return
	}

	mcpResult(w, id, service.CallToolResult{
		Content: []service.ToolContent{
			{Type: "text", Text: content},
		},
	})
}

func (s *Server) gwMCPSearchAndFetch(w http.ResponseWriter, r *http.Request, id int, args map[string]any, srv *service.RAGMCPServer) {
	// ── Parse arguments ──
	var searchReq rag.SearchRequest

	if q, ok := args["query"].(string); ok {
		searchReq.Query = q
	}
	if searchReq.Query == "" {
		mcpError(w, id, -32602, "query argument is required")
		return
	}

	if ids, ok := args["collection_ids"].([]any); ok {
		for _, v := range ids {
			if s, ok := v.(string); ok {
				searchReq.CollectionIDs = append(searchReq.CollectionIDs, s)
			}
		}
	}

	// Scope collections to server config.
	if len(searchReq.CollectionIDs) == 0 && len(srv.Config.CollectionIDs) > 0 {
		searchReq.CollectionIDs = srv.Config.CollectionIDs
	}
	if len(searchReq.CollectionIDs) > 0 && len(srv.Config.CollectionIDs) > 0 {
		var scoped []string
		for _, cid := range searchReq.CollectionIDs {
			if slices.Contains(srv.Config.CollectionIDs, cid) {
				scoped = append(scoped, cid)
			}
		}
		searchReq.CollectionIDs = scoped
		if len(scoped) == 0 {
			mcpError(w, id, -32602, "none of the requested collection_ids are available on this MCP server")
			return
		}
	}

	if n, ok := args["num_results"].(float64); ok {
		searchReq.NumResults = int(n)
	}
	if searchReq.NumResults <= 0 {
		searchReq.NumResults = gwDefaultNumResults(srv.Config.DefaultNumResults)
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
		slog.Error("gateway mcp rag_search_and_fetch: search failed", "mcp_server", srv.Name, "error", err)
		mcpError(w, id, -32000, fmt.Sprintf("search failed: %v", err))
		return
	}

	if len(results) == 0 {
		mcpResult(w, id, service.CallToolResult{
			Content: []service.ToolContent{
				{Type: "text", Text: "No results found."},
			},
		})
		return
	}

	// ── Format search results ──
	var text strings.Builder
	text.WriteString("## Search Results\n\n")

	// Collect unique sources in order of best score.
	seen := make(map[string]bool)
	var uniqueSources []string

	for i, res := range results {
		source, _ := res.Metadata["source"].(string)
		path, _ := res.Metadata["path"].(string)
		repoURL, _ := res.Metadata["repo_url"].(string)
		commitSHA, _ := res.Metadata["commit_sha"].(string)
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
		if contentType != "" {
			fmt.Fprintf(&text, "content_type: %s\n", contentType)
		}
		fmt.Fprintf(&text, "\n%s\n\n", res.Content)

		// Track unique sources for fetching.
		if source != "" && !seen[source] {
			seen[source] = true
			uniqueSources = append(uniqueSources, source)
		}
	}

	// ── Fetch source files ──
	if len(uniqueSources) > maxSources {
		uniqueSources = uniqueSources[:maxSources]
	}

	if len(uniqueSources) > 0 {
		text.WriteString("## Fetched Sources\n\n")

		for _, source := range uniqueSources {
			// Derive a display label — use the path portion if available.
			label := source
			if _, filePath := splitSourceToRepoAndPath(source); filePath != "" {
				label = filePath
			}

			content, err := fetchSourceContent(r.Context(), source, srv, maxSourceSize)
			if err != nil {
				fmt.Fprintf(&text, "=== %s (fetch failed: %s) ===\n\n", label, err.Error())
				continue
			}

			fmt.Fprintf(&text, "=== %s ===\n%s\n\n", label, content)
		}
	}

	mcpResult(w, id, service.CallToolResult{
		Content: []service.ToolContent{
			{Type: "text", Text: text.String()},
		},
	})
}

// ─── Helpers ───

// fetchSourceContent fetches the full content of a source file, respecting the server's fetch mode.
// It tries the local git cache first (for "auto"/"local" modes), then falls back to HTTP (for "auto"/"remote" modes).
func fetchSourceContent(ctx context.Context, source string, srv *service.RAGMCPServer, maxSize int) (string, error) {
	fetchMode := srv.Config.FetchMode
	if fetchMode == "" {
		fetchMode = "auto"
	}

	gitCacheDir := srv.Config.GitCacheDir
	if gitCacheDir == "" {
		gitCacheDir = "/tmp/at-git-cache"
	}

	// Try local git cache first (for "auto" or "local" modes).
	if fetchMode == "auto" || fetchMode == "local" {
		content, found := tryLocalGitCache(source, gitCacheDir, maxSize)
		if found {
			return content, nil
		}

		if fetchMode == "local" {
			return "", fmt.Errorf("source not found in local git cache and fetch mode is 'local'")
		}
	}

	// Fall back to HTTP fetch (for "auto" or "remote" modes).
	sourceLower := strings.ToLower(source)
	if !strings.HasPrefix(sourceLower, "http://") && !strings.HasPrefix(sourceLower, "https://") {
		return "", fmt.Errorf("source must be an HTTP/HTTPS URL for remote fetch, got: %s", source)
	}

	// Convert GitHub blob URLs to raw content URLs.
	fetchURL := convertToRawURL(source)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
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

func gwDefaultNumResults(configured int) int {
	if configured > 0 {
		return configured
	}
	return 10
}

// convertToRawURL converts GitHub blob URLs to raw.githubusercontent.com URLs.
// e.g. https://github.com/user/repo/blob/main/file.go → https://raw.githubusercontent.com/user/repo/main/file.go
var githubBlobRe = regexp.MustCompile(`^https?://github\.com/([^/]+/[^/]+)/blob/(.+)$`)

func convertToRawURL(source string) string {
	matches := githubBlobRe.FindStringSubmatch(source)
	if matches != nil {
		return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s", matches[1], matches[2])
	}
	return source
}

// tryLocalGitCache attempts to read a file from the local git cache.
// The source from RAG metadata is typically "repo_url/path" — we try to
// find it by scanning the git cache directory for matching repos.
func tryLocalGitCache(source, gitCacheDir string, maxSize int) (string, bool) {
	// Stat the cache dir — if it doesn't exist, return early.
	if _, err := os.Stat(gitCacheDir); os.IsNotExist(err) {
		return "", false
	}

	// Strategy 1: If the source looks like a GitHub-style URL with a path,
	// try to decompose it into repo + path and scan cache directories.
	repoURL, filePath := splitSourceToRepoAndPath(source)
	if repoURL != "" && filePath != "" {
		// Try common branches.
		for _, branch := range []string{"main", "master", "develop"} {
			cacheKey := repoURL + "\x00" + branch
			hash := sha256.Sum256([]byte(cacheKey))
			dirName := fmt.Sprintf("%x", hash[:])[:8]
			fullPath := filepath.Join(gitCacheDir, dirName, filePath)

			content, err := readFileWithLimit(fullPath, maxSize)
			if err == nil {
				return content, true
			}
		}

		// Strategy 2: Scan all cache directories for the file path.
		entries, err := os.ReadDir(gitCacheDir)
		if err != nil {
			return "", false
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			fullPath := filepath.Join(gitCacheDir, entry.Name(), filePath)
			content, err := readFileWithLimit(fullPath, maxSize)
			if err == nil {
				return content, true
			}
		}
	}

	return "", false
}

// splitSourceToRepoAndPath tries to split a source string into repo URL and file path.
// Handles formats like:
//   - https://github.com/user/repo/path/to/file.go → (https://github.com/user/repo, path/to/file.go)
//   - https://github.com/user/repo/blob/main/path/to/file.go → (https://github.com/user/repo, path/to/file.go)
func splitSourceToRepoAndPath(source string) (string, string) {
	// Try GitHub blob URL pattern first.
	matches := githubBlobRe.FindStringSubmatch(source)
	if matches != nil {
		repo := "https://github.com/" + matches[1]
		// matches[2] is "branch/path/to/file" — strip the branch part.
		branchAndPath := matches[2]
		if idx := strings.Index(branchAndPath, "/"); idx >= 0 {
			return repo, branchAndPath[idx+1:]
		}
		return repo, branchAndPath
	}

	// Try plain GitHub URL: https://github.com/user/repo/path/to/file.go
	if strings.HasPrefix(source, "https://github.com/") || strings.HasPrefix(source, "http://github.com/") {
		// Remove the protocol
		withoutProto := source
		if idx := strings.Index(withoutProto, "://"); idx >= 0 {
			withoutProto = withoutProto[idx+3:]
		}
		// github.com/user/repo/rest/of/path
		parts := strings.SplitN(withoutProto, "/", 4) // [github.com, user, repo, rest/of/path]
		if len(parts) >= 4 {
			repoURL := "https://github.com/" + parts[1] + "/" + parts[2]
			return repoURL, parts[3]
		}
	}

	return "", ""
}

// readFileWithLimit reads a file up to maxSize bytes, appending a truncation note if needed.
func readFileWithLimit(path string, maxSize int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	limitReader := io.LimitReader(f, int64(maxSize+1))
	body, err := io.ReadAll(limitReader)
	if err != nil {
		return "", err
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
