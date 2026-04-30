package openaicompatible

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/rakunlabs/ok"
)

// EmbeddingRequest is the body of POST /embeddings.
//
// Input must be either a string or a []string. The server may also accept
// []int / [][]int for already-tokenised input — in that case use Extra to
// override.
type EmbeddingRequest struct {
	Model          string `json:"model"`
	Input          any    `json:"input"`
	EncodingFormat string `json:"encoding_format,omitempty"` // "float" (default) or "base64"
	Dimensions     *int   `json:"dimensions,omitempty"`
	User           string `json:"user,omitempty"`

	// Extra carries arbitrary additional fields merged into the JSON body.
	Extra map[string]any `json:"-"`
}

// MarshalJSON merges Extra into the wire body without overwriting typed fields.
func (r EmbeddingRequest) MarshalJSON() ([]byte, error) {
	type alias EmbeddingRequest
	base, err := json.Marshal(alias(r))
	if err != nil {
		return nil, err
	}
	if len(r.Extra) == 0 {
		return base, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		return nil, err
	}
	for k, v := range r.Extra {
		if _, exists := m[k]; exists {
			continue
		}
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		m[k] = raw
	}
	return json.Marshal(m)
}

// EmbeddingResponse is the body of POST /embeddings.
type EmbeddingResponse struct {
	Object string         `json:"object"`
	Model  string         `json:"model"`
	Data   []EmbeddingObj `json:"data"`
	Usage  *Usage         `json:"usage,omitempty"`
}

// EmbeddingObj is one vector in the embeddings response.
type EmbeddingObj struct {
	Object string `json:"object"`
	Index  int    `json:"index"`
	// Embedding is []float64 when EncodingFormat is "float" (default), or a
	// base64 string when EncodingFormat is "base64". It is decoded as
	// json.RawMessage to support both shapes — call AsFloat / AsBase64.
	Embedding json.RawMessage `json:"embedding"`
}

// AsFloat decodes the embedding as a []float64. Use this when the request
// did not set EncodingFormat or set it to "float".
func (e EmbeddingObj) AsFloat() ([]float64, error) {
	var v []float64
	if err := json.Unmarshal(e.Embedding, &v); err != nil {
		return nil, fmt.Errorf("openai-compatible: decode embedding as float: %w", err)
	}
	return v, nil
}

// AsBase64 returns the embedding as a base64 string. Use this when the
// request set EncodingFormat to "base64".
func (e EmbeddingObj) AsBase64() (string, error) {
	var v string
	if err := json.Unmarshal(e.Embedding, &v); err != nil {
		return "", fmt.Errorf("openai-compatible: decode embedding as base64: %w", err)
	}
	return v, nil
}

// Embeddings calls POST /embeddings.
func (c *Client) Embeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	if req == nil {
		return nil, errors.New("openai-compatible: nil EmbeddingRequest")
	}
	if req.Model == "" {
		req.Model = c.model
	}
	if req.Model == "" {
		return nil, errors.New("openai-compatible: EmbeddingRequest.Model is required (or use WithModel)")
	}
	if req.Input == nil {
		return nil, errors.New("openai-compatible: EmbeddingRequest.Input is required")
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("openai-compatible: marshal embeddings request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	var (
		out        EmbeddingResponse
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
				return fmt.Errorf("decode embeddings: %w (body: %s)", jerr, truncate(string(raw), 500))
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("openai-compatible: embeddings request: %w", err)
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, buildAPIError(statusCode, respHeader, raw)
	}
	return &out, nil
}
