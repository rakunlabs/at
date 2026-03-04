package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/rag"
)

// RAGMCPHandler handles MCP protocol requests for the RAG server.
// It implements the Streamable HTTP transport: clients POST JSON-RPC 2.0
// messages to /mcp/rag and receive JSON-RPC 2.0 responses.
func (s *Server) RAGMCPHandler(w http.ResponseWriter, r *http.Request) {
	if s.ragService == nil {
		httpResponse(w, "rag service not configured", http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodPost {
		httpResponse(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req service.MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	// Route by method.
	switch req.Method {
	case "initialize":
		s.mcpRAGInitialize(w, req)
	case "notifications/initialized":
		// Client acknowledgement — no response needed.
		w.WriteHeader(http.StatusOK)
	case "tools/list":
		s.mcpRAGListTools(w, req)
	case "tools/call":
		s.mcpRAGCallTool(w, r, req)
	default:
		mcpError(w, req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

// ─── MCP Handlers ───

func (s *Server) mcpRAGInitialize(w http.ResponseWriter, req service.MCPRequest) {
	result := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]string{
			"name":    "at-rag",
			"version": "1.0.0",
		},
	}

	mcpResult(w, req.ID, result)
}

func (s *Server) mcpRAGListTools(w http.ResponseWriter, req service.MCPRequest) {
	tools := service.ListToolsResult{
		Tools: []service.Tool{
			{
				Name:        "rag_search",
				Description: "Search documents in the RAG knowledge base by semantic similarity. Returns relevant document chunks matching the query.",
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
				Description: "Fetch the original full content of a document by its source URL or path. Use this after rag_search to retrieve the complete original file when chunks are insufficient. Only HTTP/HTTPS URLs are supported.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"source": map[string]any{
							"type":        "string",
							"description": "The source URL from the rag_search result metadata (must be an HTTP/HTTPS URL)",
						},
						"max_size": map[string]any{
							"type":        "integer",
							"description": "Maximum content size in bytes to return (default: 102400, max: 1048576). Content is truncated if larger.",
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
		},
	}

	mcpResult(w, req.ID, tools)
}

func (s *Server) mcpRAGCallTool(w http.ResponseWriter, r *http.Request, req service.MCPRequest) {
	// Parse tool call params.
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

	switch params.Name {
	case "rag_search":
		s.mcpRAGSearch(w, r, req.ID, params.Arguments)
	case "rag_list_collections":
		s.mcpRAGListCollections(w, r, req.ID)
	case "rag_fetch_source":
		s.mcpRAGFetchSource(w, r, req.ID, params.Arguments)
	case "rag_search_and_fetch":
		s.mcpRAGSearchAndFetch(w, r, req.ID, params.Arguments)
	default:
		mcpError(w, req.ID, -32602, fmt.Sprintf("unknown tool: %s", params.Name))
	}
}

func (s *Server) mcpRAGSearch(w http.ResponseWriter, r *http.Request, id int, args map[string]any) {
	// Parse search arguments.
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

	if n, ok := args["num_results"].(float64); ok {
		searchReq.NumResults = int(n)
	}

	if t, ok := args["score_threshold"].(float64); ok {
		searchReq.ScoreThreshold = float32(t)
	}

	results, err := s.ragService.Search(r.Context(), searchReq)
	if err != nil {
		slog.Error("mcp rag_search failed", "error", err)
		mcpError(w, id, -32000, fmt.Sprintf("search failed: %v", err))
		return
	}

	// Format results as text for MCP.
	var text string
	if len(results) == 0 {
		text = "No results found."
	} else {
		for i, res := range results {
			source := ""
			if s, ok := res.Metadata["source"].(string); ok {
				source = s
			}
			text += fmt.Sprintf("--- Result %d (score: %.4f, source: %s) ---\n%s\n\n",
				i+1, res.Score, source, res.Content)
		}
	}

	mcpResult(w, id, service.CallToolResult{
		Content: []service.ToolContent{
			{Type: "text", Text: text},
		},
	})
}

func (s *Server) mcpRAGListCollections(w http.ResponseWriter, r *http.Request, id int) {
	collectionsResult, err := s.ragCollectionStore.ListRAGCollections(r.Context(), nil)
	if err != nil {
		slog.Error("mcp rag_list_collections failed", "error", err)
		mcpError(w, id, -32000, fmt.Sprintf("list collections failed: %v", err))
		return
	}

	collections := collectionsResult.Data
	if collections == nil {
		collections = []service.RAGCollection{}
	}

	// Format as structured text.
	data, _ := json.MarshalIndent(collections, "", "  ")

	mcpResult(w, id, service.CallToolResult{
		Content: []service.ToolContent{
			{Type: "text", Text: string(data)},
		},
	})
}

func (s *Server) mcpRAGFetchSource(w http.ResponseWriter, r *http.Request, id int, args map[string]any) {
	source, _ := args["source"].(string)
	if source == "" {
		mcpError(w, id, -32602, "source argument is required")
		return
	}

	// Only allow HTTP/HTTPS URLs for security.
	sourceLower := strings.ToLower(source)
	if !strings.HasPrefix(sourceLower, "http://") && !strings.HasPrefix(sourceLower, "https://") {
		mcpError(w, id, -32602, fmt.Sprintf("only HTTP/HTTPS URLs are supported, got: %s", source))
		return
	}

	// Default and max content size limits.
	maxSize := 102400 // 100KB default
	if n, ok := args["max_size"].(float64); ok && int(n) > 0 {
		maxSize = int(n)
	}
	if maxSize > 1048576 { // 1MB hard cap
		maxSize = 1048576
	}

	// Fetch the source URL.
	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, source, nil)
	if err != nil {
		mcpError(w, id, -32000, fmt.Sprintf("invalid source URL: %v", err))
		return
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		slog.Error("mcp rag_fetch_source: fetch failed", "source", source, "error", err)
		mcpError(w, id, -32000, fmt.Sprintf("failed to fetch source: %v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		mcpError(w, id, -32000, fmt.Sprintf("source returned HTTP %d", resp.StatusCode))
		return
	}

	// Read content with size limit.
	limitReader := io.LimitReader(resp.Body, int64(maxSize+1))
	body, err := io.ReadAll(limitReader)
	if err != nil {
		slog.Error("mcp rag_fetch_source: read failed", "source", source, "error", err)
		mcpError(w, id, -32000, fmt.Sprintf("failed to read source content: %v", err))
		return
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

	mcpResult(w, id, service.CallToolResult{
		Content: []service.ToolContent{
			{Type: "text", Text: text},
		},
	})
}

func (s *Server) mcpRAGSearchAndFetch(w http.ResponseWriter, r *http.Request, id int, args map[string]any) {
	result, err := s.execRAGSearchAndFetch(r, args)
	if err != nil {
		slog.Error("mcp rag_search_and_fetch failed", "error", err)
		mcpError(w, id, -32000, err.Error())
		return
	}

	mcpResult(w, id, service.CallToolResult{
		Content: []service.ToolContent{
			{Type: "text", Text: result},
		},
	})
}

// ─── RAG Chat UI Endpoints ───
//
// These endpoints expose RAG tools directly to the Chat UI without requiring
// the full MCP JSON-RPC protocol. They reuse the same execution logic as the
// MCP tool handlers above.

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
			Description: "Search documents in the RAG knowledge base by semantic similarity. Returns relevant document chunks matching the query.",
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
			Description: "Fetch the original full content of a document by its source URL or path. Use this after rag_search to retrieve the complete original file when chunks are insufficient. Only HTTP/HTTPS URLs are supported.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"source": map[string]any{
						"type":        "string",
						"description": "The source URL from the rag_search result metadata (must be an HTTP/HTTPS URL)",
					},
					"max_size": map[string]any{
						"type":        "integer",
						"description": "Maximum content size in bytes to return (default: 102400, max: 1048576). Content is truncated if larger.",
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

	var result string
	var execErr error

	switch req.Name {
	case "rag_search":
		result, execErr = s.execRAGSearch(r, req.Arguments)
	case "rag_list_collections":
		result, execErr = s.execRAGListCollections(r)
	case "rag_fetch_source":
		result, execErr = s.execRAGFetchSource(r, req.Arguments)
	case "rag_search_and_fetch":
		result, execErr = s.execRAGSearchAndFetch(r, req.Arguments)
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
		source := ""
		if s, ok := res.Metadata["source"].(string); ok {
			source = s
		}
		text += fmt.Sprintf("--- Result %d (score: %.4f, source: %s) ---\n%s\n\n",
			i+1, res.Score, source, res.Content)
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
func (s *Server) execRAGFetchSource(r *http.Request, args map[string]any) (string, error) {
	source, _ := args["source"].(string)
	if source == "" {
		return "", fmt.Errorf("source argument is required")
	}

	sourceLower := strings.ToLower(source)
	if !strings.HasPrefix(sourceLower, "http://") && !strings.HasPrefix(sourceLower, "https://") {
		return "", fmt.Errorf("only HTTP/HTTPS URLs are supported, got: %s", source)
	}

	maxSize := 102400
	if n, ok := args["max_size"].(float64); ok && int(n) > 0 {
		maxSize = int(n)
	}
	if maxSize > 1048576 {
		maxSize = 1048576
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
// It searches, then fetches the top unique source files via HTTP and returns combined results.
func (s *Server) execRAGSearchAndFetch(r *http.Request, args map[string]any) (string, error) {
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

	// ── Format search results ──
	var text strings.Builder
	text.WriteString("## Search Results\n\n")

	// Collect unique sources in order of best score.
	seen := make(map[string]bool)
	var uniqueSources []string

	for i, res := range results {
		source := ""
		if s, ok := res.Metadata["source"].(string); ok {
			source = s
		}
		fmt.Fprintf(&text, "--- Result %d (score: %.4f, source: %s) ---\n%s\n\n",
			i+1, res.Score, source, res.Content)

		// Track unique sources for fetching.
		if source != "" && !seen[source] {
			seen[source] = true
			uniqueSources = append(uniqueSources, source)
		}
	}

	// ── Fetch source files (HTTP only — no git cache in non-gateway version) ──
	if len(uniqueSources) > maxSources {
		uniqueSources = uniqueSources[:maxSources]
	}

	if len(uniqueSources) > 0 {
		text.WriteString("## Fetched Sources\n\n")

		for _, source := range uniqueSources {
			sourceLower := strings.ToLower(source)
			if !strings.HasPrefix(sourceLower, "http://") && !strings.HasPrefix(sourceLower, "https://") {
				fmt.Fprintf(&text, "=== %s (skipped: not an HTTP URL) ===\n\n", source)
				continue
			}

			httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, source, nil)
			if err != nil {
				fmt.Fprintf(&text, "=== %s (fetch failed: %s) ===\n\n", source, err.Error())
				continue
			}

			resp, err := http.DefaultClient.Do(httpReq)
			if err != nil {
				fmt.Fprintf(&text, "=== %s (fetch failed: %s) ===\n\n", source, err.Error())
				continue
			}

			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				fmt.Fprintf(&text, "=== %s (fetch failed: HTTP %d) ===\n\n", source, resp.StatusCode)
				continue
			}

			limitReader := io.LimitReader(resp.Body, int64(maxSourceSize+1))
			body, err := io.ReadAll(limitReader)
			resp.Body.Close()
			if err != nil {
				fmt.Fprintf(&text, "=== %s (read failed: %s) ===\n\n", source, err.Error())
				continue
			}

			content := string(body)
			if len(body) > maxSourceSize {
				content = string(body[:maxSourceSize])
				content += fmt.Sprintf("\n\n[Content truncated at %d bytes]", maxSourceSize)
			}

			fmt.Fprintf(&text, "=== %s ===\n%s\n\n", source, content)
		}
	}

	return text.String(), nil
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
