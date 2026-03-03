package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

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
