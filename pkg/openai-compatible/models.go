package openaicompatible

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/rakunlabs/ok"
)

// Model is one entry returned by GET /models.
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created,omitempty"`
	OwnedBy string `json:"owned_by,omitempty"`
}

// ModelList is the response shape of GET /models.
type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// ListModels calls GET /models on the configured server.
//
// Not all OpenAI-compatible servers implement this endpoint — the AT
// gateway does (it returns the merged list across all configured providers
// in "provider/model" form), as do OpenAI, Ollama, vLLM, LiteLLM, etc.
func (c *Client) ListModels(ctx context.Context) (*ModelList, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "models", nil)
	if err != nil {
		return nil, err
	}

	var (
		out        ModelList
		statusCode int
		respHeader http.Header
		raw        []byte
	)
	if err := ok.Do(c.httpClient, httpReq, func(r *http.Response) error {
		statusCode = r.StatusCode
		respHeader = r.Header
		var rerr error
		raw, rerr = io.ReadAll(r.Body)
		if rerr != nil {
			return rerr
		}
		if statusCode >= 200 && statusCode < 300 {
			if jerr := json.Unmarshal(raw, &out); jerr != nil {
				return fmt.Errorf("decode models: %w (body: %s)", jerr, truncate(string(raw), 500))
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("openai-compatible: list models: %w", err)
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, buildAPIError(statusCode, respHeader, raw)
	}
	return &out, nil
}
