package nodes

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// ragSearchNode performs a similarity search against RAG collections.
//
// Configuration (node.Data):
//
//	collection_ids  []string  — collections to search (empty = all)
//	num_results     float64   — max results to return (default 5)
//	score_threshold float64   — minimum similarity score 0-1 (default 0)
//
// Inputs:
//
//	query  string — the search query text (required)
//
// Outputs:
//
//	results  []RAGSearchResult — the search hits
//	text     string            — concatenated result content (convenience)
type ragSearchNode struct {
	collectionIDs  []string
	numResults     int
	scoreThreshold float32
}

func init() {
	workflow.RegisterNodeType("rag_search", newRAGSearchNode)
}

func newRAGSearchNode(node service.WorkflowNode) (workflow.Noder, error) {
	n := &ragSearchNode{
		numResults: 5,
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

	return n, nil
}

func (n *ragSearchNode) Type() string { return "rag_search" }

func (n *ragSearchNode) Validate(_ context.Context, reg *workflow.Registry) error {
	if reg.RAGSearch == nil {
		return fmt.Errorf("rag_search: RAG is not configured")
	}
	return nil
}

// Run executes the RAG search. It reads the query from inputs and returns
// both structured results and a concatenated text output.
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

	// Build concatenated text for easy downstream consumption.
	var text string
	for i, r := range results {
		if i > 0 {
			text += "\n\n---\n\n"
		}
		text += r.Content
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

	return workflow.NewResult(map[string]any{
		"results": resultsAny,
		"text":    text,
	}), nil
}
