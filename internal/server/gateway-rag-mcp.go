package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/rag"
)

const defaultRAGGitCacheDir = "/tmp/at-git-cache"

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
				Description: "Fetch the original full content of a document by its source URL or path. Supports HTTP/HTTPS URLs and local git cache. Use this after rag_search to retrieve the complete original file when chunks are insufficient. Pass commit_sha and repo_url from search result metadata for exact version fetching.",
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

	// Optional git metadata for precise fetching.
	commitSHA, _ := args["commit_sha"].(string)
	branch, _ := args["branch"].(string)
	repoURL, _ := args["repo_url"].(string)

	// Resolve auth from server config.
	auth, err := resolveGitAuth(r.Context(), s.variableStore, srv)
	if err != nil {
		slog.Warn("gateway mcp rag_fetch_source: failed to resolve git auth", "error", err)
	}
	if auth != nil && auth.cleanup != nil {
		defer auth.cleanup()
	}

	var envVars []string
	if auth != nil {
		envVars = auth.envVars
	}

	// If commitSHA and repoURL are provided, try commit-specific checkout first.
	if commitSHA != "" && repoURL != "" {
		gitCacheDir := srv.Config.GitCacheDir
		if gitCacheDir == "" {
			gitCacheDir = defaultRAGGitCacheDir
		}

		authURL := repoURL
		if auth != nil {
			authURL = auth.authURL(repoURL)
		}

		repoDir, err := ensureRepoAtCommitMCP(r.Context(), authURL, repoURL, commitSHA, gitCacheDir, envVars)
		if err == nil {
			_, filePath := splitSourceToRepoAndPath(source)
			if filePath != "" {
				content, readErr := readFileWithLimit(filepath.Join(repoDir, filePath), maxSize)
				if readErr == nil {
					mcpResult(w, id, service.CallToolResult{
						Content: []service.ToolContent{
							{Type: "text", Text: content},
						},
					})
					return
				}
			}
		} else {
			slog.Warn("gateway mcp rag_fetch_source: commit-specific checkout failed, falling back",
				"repo_url", repoURL, "commit_sha", commitSHA, "error", err)
		}
	}

	content, err := fetchSourceContent(r.Context(), source, srv, maxSize, commitSHA, branch, envVars)
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

	// ── Resolve auth for git operations ──
	auth, authErr := resolveGitAuth(r.Context(), s.variableStore, srv)
	if authErr != nil {
		slog.Warn("gateway mcp rag_search_and_fetch: failed to resolve git auth", "error", authErr)
	}
	if auth != nil && auth.cleanup != nil {
		defer auth.cleanup()
	}

	// ── Format search results ──
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

		gitCacheDir := srv.Config.GitCacheDir
		if gitCacheDir == "" {
			gitCacheDir = defaultRAGGitCacheDir
		}

		var envVars []string
		if auth != nil {
			envVars = auth.envVars
		}

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
				if auth != nil {
					authURL = auth.authURL(si.repoURL)
				}

				repoDir, err := ensureRepoAtCommitMCP(r.Context(), authURL, si.repoURL, si.commitSHA, gitCacheDir, envVars)
				if err == nil {
					content, readErr := readFileWithLimit(filepath.Join(repoDir, si.path), maxSourceSize)
					if readErr == nil {
						fmt.Fprintf(&text, "=== %s ===\n%s\n\n", label, content)
						continue
					}
				} else {
					slog.Warn("gateway mcp rag_search_and_fetch: commit-specific checkout failed, falling back",
						"source", si.source, "commit_sha", si.commitSHA, "error", err)
				}
			}

			// Fall back to fetchSourceContent (cache lookup, clone, or HTTP).
			content, err := fetchSourceContent(r.Context(), si.source, srv, maxSourceSize, si.commitSHA, si.branch, envVars)
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
// SSH sources (git@host:... or ssh://...) are always resolved from the local git cache — no HTTP fallback.
//
// When commitSHA is provided, commit-specific cache lookup and clone are attempted first.
// envVars are passed to git commands for auth (e.g. GIT_SSH_COMMAND for SSH keys).
func fetchSourceContent(ctx context.Context, source string, srv *service.RAGMCPServer, maxSize int, commitSHA, branch string, envVars []string) (string, error) {
	fetchMode := srv.Config.FetchMode
	if fetchMode == "" {
		fetchMode = "auto"
	}

	gitCacheDir := srv.Config.GitCacheDir
	if gitCacheDir == "" {
		gitCacheDir = defaultRAGGitCacheDir
	}

	isSSH := isSSHSource(source)

	// SSH sources are always served from the local git cache (populated by git_fetch workflow).
	if isSSH {
		content, found := tryLocalGitCache(source, gitCacheDir, maxSize, commitSHA, branch)
		if found {
			return content, nil
		}
		// Fallback: try a clone if the cache doesn't have it yet.
		repoURL, filePath := splitSourceToRepoAndPath(source)
		content, found = fallbackCloneAndRead(ctx, repoURL, branch, filePath, gitCacheDir, maxSize, commitSHA, envVars)
		if found {
			return content, nil
		}
		return "", fmt.Errorf("source not found in local git cache (SSH sources are resolved from the git cache maintained by the git_fetch workflow)")
	}

	// Try local git cache first (for "auto" or "local" modes).
	if fetchMode == "auto" || fetchMode == "local" {
		content, found := tryLocalGitCache(source, gitCacheDir, maxSize, commitSHA, branch)
		if found {
			return content, nil
		}

		if fetchMode == "local" {
			// Fallback: try a clone before giving up.
			repoURL, filePath := splitSourceToRepoAndPath(source)
			content, found = fallbackCloneAndRead(ctx, repoURL, branch, filePath, gitCacheDir, maxSize, commitSHA, envVars)
			if found {
				return content, nil
			}
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

// cloneInFlight tracks repos currently being cloned to prevent concurrent clones
// of the same repository. Key is the cache directory hash.
var cloneInFlight sync.Map

// tryLocalGitCache attempts to read a file from the local git cache.
// The source from RAG metadata is typically "repo_url/path" — we try to
// find it by scanning the git cache directory for matching repos.
//
// When commitSHA is provided, the commit-specific cache directory is tried first
// (highest precision). When branch is provided, the branch-specific directory is
// tried before falling back to common branch names and directory scanning.
func tryLocalGitCache(source, gitCacheDir string, maxSize int, commitSHA, branch string) (string, bool) {
	repoURL, filePath := splitSourceToRepoAndPath(source)
	if repoURL == "" || filePath == "" {
		return "", false
	}

	// Strategy 0: Try commit-specific cache directory (most precise).
	if commitSHA != "" {
		dirName := hashRepoCommitKey(repoURL, commitSHA)
		fullPath := filepath.Join(gitCacheDir, dirName, filePath)
		content, err := readFileWithLimit(fullPath, maxSize)
		if err == nil {
			return content, true
		}
	}

	// Strategy 1: Try the exact hash-based directory for the known branch,
	// or fall back to common branches.
	// The git_fetch node hashes repoURL + "\x00" + branch using SHA-256 and
	// takes the first 8 bytes as hex (16 hex chars) for the directory name.
	if branch != "" {
		dirName := hashCacheKey(repoURL, branch)
		fullPath := filepath.Join(gitCacheDir, dirName, filePath)
		content, err := readFileWithLimit(fullPath, maxSize)
		if err == nil {
			return content, true
		}
	} else {
		for _, b := range []string{"main", "master", "develop"} {
			dirName := hashCacheKey(repoURL, b)
			fullPath := filepath.Join(gitCacheDir, dirName, filePath)
			content, err := readFileWithLimit(fullPath, maxSize)
			if err == nil {
				return content, true
			}
		}
	}

	// Strategy 2: Scan all cache directories for the file path.
	// This covers custom branches and other hash variations.
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

	return "", false
}

// fallbackCloneAndRead performs a one-time clone of a repository when the
// git cache directory does not contain the needed file. This is a best-effort
// fallback for cases where the git_fetch workflow hasn't populated the cache yet.
//
// When commitSHA is provided, a full clone + checkout at that commit is performed
// (not shallow, since arbitrary commits may not be reachable with shallow clone).
// When commitSHA is empty, a shallow single-branch clone is used for speed.
//
// It uses a sync.Map to prevent concurrent clones of the same repo+ref.
// envVars are passed to git commands for auth (e.g. GIT_SSH_COMMAND).
// If cloning fails, it returns ("", false) gracefully — the caller should
// treat this as a cache miss.
func fallbackCloneAndRead(ctx context.Context, repoURL, branch, filePath, gitCacheDir string, maxSize int, commitSHA string, envVars []string) (string, bool) {
	if repoURL == "" || filePath == "" {
		return "", false
	}

	// Determine the cache key and directory.
	var dirName string
	if commitSHA != "" {
		dirName = hashRepoCommitKey(repoURL, commitSHA)
	} else {
		if branch == "" {
			branch = "main"
		}
		dirName = hashCacheKey(repoURL, branch)
	}
	repoDir := filepath.Join(gitCacheDir, dirName)

	// If the directory already exists (race with another goroutine or workflow), just read.
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err == nil {
		content, err := readFileWithLimit(filepath.Join(repoDir, filePath), maxSize)
		if err == nil {
			return content, true
		}
		return "", false
	}

	// Use sync.Map to ensure only one clone per repo+ref at a time.
	// LoadOrStore returns (actual value, loaded); if loaded=true another goroutine is already cloning.
	ch := make(chan struct{})
	if actual, loaded := cloneInFlight.LoadOrStore(dirName, ch); loaded {
		// Another goroutine is cloning this repo — wait for it to finish.
		waitCh, ok := actual.(chan struct{})
		if ok {
			select {
			case <-waitCh:
			case <-ctx.Done():
				return "", false
			}
		}
		// Re-try the file read after the other clone completes.
		content, err := readFileWithLimit(filepath.Join(repoDir, filePath), maxSize)
		if err == nil {
			return content, true
		}
		return "", false
	}
	// We won the race — we'll do the clone.
	defer func() {
		close(ch)
		cloneInFlight.Delete(dirName)
	}()

	// Create cache dir if it doesn't exist.
	if err := os.MkdirAll(gitCacheDir, 0o755); err != nil {
		slog.Warn("fallback clone: failed to create cache dir", "dir", gitCacheDir, "error", err)
		return "", false
	}

	gitEnvList := mcpGitEnv(envVars)

	if commitSHA != "" {
		// Full clone + checkout at specific commit.
		cloneArgs := []string{"clone", "--no-checkout", repoURL, dirName}
		cmd := exec.CommandContext(ctx, "git", cloneArgs...)
		cmd.Dir = gitCacheDir
		cmd.Env = gitEnvList
		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		slog.Info("fallback clone: cloning repository for commit", "repo", repoURL, "commit_sha", commitSHA)
		if err := cmd.Run(); err != nil {
			_ = os.RemoveAll(repoDir)
			slog.Warn("fallback clone: clone failed", "repo", repoURL, "commit_sha", commitSHA, "error", err, "stderr", stderr.String())
			return "", false
		}

		// Checkout the specific commit.
		checkoutCmd := exec.CommandContext(ctx, "git", "checkout", commitSHA)
		checkoutCmd.Dir = repoDir
		checkoutCmd.Env = gitEnvList
		var checkoutStderr bytes.Buffer
		checkoutCmd.Stderr = &checkoutStderr
		if err := checkoutCmd.Run(); err != nil {
			_ = os.RemoveAll(repoDir)
			slog.Warn("fallback clone: checkout failed", "repo", repoURL, "commit_sha", commitSHA, "error", err, "stderr", checkoutStderr.String())
			return "", false
		}
	} else {
		// Shallow clone — depth 1, single branch.
		cloneArgs := []string{"clone", "--depth", "1", "--single-branch", "--branch", branch, repoURL, dirName}
		cmd := exec.CommandContext(ctx, "git", cloneArgs...)
		cmd.Dir = gitCacheDir
		cmd.Env = gitEnvList
		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		slog.Info("fallback clone: cloning repository", "repo", repoURL, "branch", branch)
		if err := cmd.Run(); err != nil {
			_ = os.RemoveAll(repoDir)
			slog.Warn("fallback clone: clone failed", "repo", repoURL, "branch", branch, "error", err, "stderr", stderr.String())
			return "", false
		}
	}

	// Read the requested file.
	content, err := readFileWithLimit(filepath.Join(repoDir, filePath), maxSize)
	if err == nil {
		return content, true
	}
	return "", false
}

// hashCacheKey produces the same directory name as the git_fetch workflow node:
// SHA-256 of (repoURL + "\x00" + branch), first 8 bytes as hex (16 hex chars).
func hashCacheKey(repoURL, branch string) string {
	h := sha256.Sum256([]byte(repoURL + "\x00" + branch))
	return hex.EncodeToString(h[:8])
}

// splitSourceToRepoAndPath tries to split a source string into repo URL and file path.
// Handles formats like:
//   - https://github.com/user/repo/path/to/file.go → (https://github.com/user/repo, path/to/file.go)
//   - https://github.com/user/repo/blob/main/path/to/file.go → (https://github.com/user/repo, path/to/file.go)
//   - git@github.com:user/repo.git/path/to/file.go → (git@github.com:user/repo.git, path/to/file.go)
//   - ssh://git@github.com/user/repo.git/path/to/file.go → (ssh://git@github.com/user/repo.git, path/to/file.go)
func splitSourceToRepoAndPath(source string) (string, string) {
	// Try SSH SCP-style URL: git@host:user/repo.git/path/to/file
	// The rag_ingest node builds source as repoURL + "/" + path, so for SSH repos
	// we get: git@github.com:user/repo.git/path/to/file.md
	if repoURL, filePath := splitSSHSource(source); repoURL != "" {
		return repoURL, filePath
	}

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

// splitSSHSource handles SSH-style git source URLs.
// Two formats are supported:
//
//	SCP-style: git@host:user/repo.git/path/to/file → (git@host:user/repo.git, path/to/file)
//	URI-style: ssh://git@host/user/repo.git/path/to/file → (ssh://git@host/user/repo.git, path/to/file)
//
// If the URL contains ".git/" we split on the first ".git/" boundary.
// If it doesn't contain ".git" but matches the SCP pattern (user@host:...),
// we fall back to splitting after the third path segment (host:owner/repo/rest).
func splitSSHSource(source string) (string, string) {
	// Strategy 1: Split on ".git/" boundary — works for both SCP and URI styles.
	if idx := strings.Index(source, ".git/"); idx >= 0 {
		repoURL := source[:idx+4]  // include ".git"
		filePath := source[idx+5:] // skip ".git/"
		if filePath != "" {
			return repoURL, filePath
		}
	}

	// Strategy 2: SCP-style without .git suffix — user@host:owner/repo/path/to/file
	// Detect by looking for the ":" after "@" (SCP format).
	if atIdx := strings.Index(source, "@"); atIdx >= 0 {
		afterAt := source[atIdx+1:]
		colonIdx := strings.Index(afterAt, ":")
		// Make sure the colon is before any "/" (SCP-style, not a port number in URI).
		slashIdx := strings.Index(afterAt, "/")
		if colonIdx > 0 && (slashIdx < 0 || colonIdx < slashIdx) {
			// afterAt[colonIdx+1:] is "owner/repo/path/to/file"
			rest := afterAt[colonIdx+1:]
			// Split into owner/repo/filepath (3 segments minimum).
			parts := strings.SplitN(rest, "/", 3)
			if len(parts) == 3 && parts[2] != "" {
				repoURL := source[:atIdx+1+colonIdx+1+len(parts[0])+1+len(parts[1])]
				return repoURL, parts[2]
			}
		}
	}

	// Strategy 3: ssh:// URI style without .git suffix — ssh://git@host/owner/repo/path/to/file
	if strings.HasPrefix(source, "ssh://") {
		withoutScheme := source[6:] // strip "ssh://"
		// Find the host part (may include user@).
		hostEnd := strings.Index(withoutScheme, "/")
		if hostEnd > 0 {
			rest := withoutScheme[hostEnd+1:]
			// rest is "owner/repo/path/to/file"
			parts := strings.SplitN(rest, "/", 3)
			if len(parts) == 3 && parts[2] != "" {
				repoURL := "ssh://" + withoutScheme[:hostEnd] + "/" + parts[0] + "/" + parts[1]
				return repoURL, parts[2]
			}
		}
	}

	return "", ""
}

// isSSHSource returns true if the source looks like an SSH git URL.
func isSSHSource(source string) bool {
	if strings.HasPrefix(source, "ssh://") {
		return true
	}
	// SCP-style: user@host:path — has "@" before ":" and ":" is before first "/".
	atIdx := strings.Index(source, "@")
	if atIdx < 0 {
		return false
	}
	afterAt := source[atIdx+1:]
	colonIdx := strings.Index(afterAt, ":")
	slashIdx := strings.Index(afterAt, "/")
	return colonIdx > 0 && (slashIdx < 0 || colonIdx < slashIdx)
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
