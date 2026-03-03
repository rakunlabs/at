package rag

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/tmc/langchaingo/embeddings"
)

// Ensure ATEmbedderClient implements the langchaingo EmbedderClient interface.
var _ embeddings.EmbedderClient = (*ATEmbedderClient)(nil)

// ATEmbedderClient bridges AT LLM providers to the langchaingo EmbedderClient
// interface. It supports two API formats:
//
//   - "openai" (default): OpenAI-compatible /v1/embeddings endpoint
//   - "gemini": Google Generative Language API batchEmbedContents
//
// For OpenAI-compatible providers (OpenAI, Ollama, vLLM, etc.) the embeddings
// URL is derived from the provider's base URL unless an explicit EmbeddingURL
// is set. For Gemini, the user typically provides the full endpoint URL
// (which already contains the model).
//
// When an explicit EmbeddingURL is provided, the Model field is optional —
// the URL is used as-is and may already encode the model.
type ATEmbedderClient struct {
	// embeddingsURL is the full URL for the embeddings endpoint.
	embeddingsURL string

	// model is the embedding model to use (may be empty when URL is explicit).
	model string

	// apiKey is the authentication key (Bearer token for OpenAI, x-goog-api-key for Gemini).
	apiKey string

	// bearerAuth forces the API key to be sent as "Authorization: Bearer <apiKey>"
	// regardless of the apiType. This is useful when the embedding URL points to
	// a gateway proxy (e.g. another AT instance) that authenticates via Bearer
	// token and injects the provider-specific key itself.
	bearerAuth bool

	// apiType is the embedding API format: "openai" or "gemini".
	apiType string

	// client is the HTTP client used to make requests.
	client *http.Client
}

// ATEmbedderConfig holds the configuration for creating an ATEmbedderClient.
type ATEmbedderConfig struct {
	// BaseURL is the provider's base URL. For OpenAI-compatible providers this
	// is typically the chat completions URL (e.g. "https://api.openai.com/v1/chat/completions").
	// The embeddings URL is derived by replacing the path suffix.
	// If EmbeddingURL is set, BaseURL is ignored for URL construction.
	BaseURL string

	// EmbeddingURL is an optional explicit URL for the embeddings endpoint.
	// When set, this URL is used as-is instead of deriving from BaseURL.
	// When EmbeddingURL is provided, Model is optional (the URL may already
	// contain the model, e.g. for Gemini batch endpoints).
	// Examples:
	//   OpenAI: "https://api.openai.com/v1/embeddings"
	//   Gemini: "https://generativelanguage.googleapis.com/v1beta/models/text-embedding-004:batchEmbedContents"
	EmbeddingURL string

	// APIType selects the embedding API format: "openai" (default) or "gemini".
	APIType string

	// Model is the embedding model identifier. Required when EmbeddingURL is
	// empty (needed to derive the URL). Optional when EmbeddingURL is set.
	Model string

	// APIKey is the authentication key. May be empty for local providers (Ollama).
	APIKey string

	// BearerAuth forces the API key to be sent as "Authorization: Bearer <APIKey>"
	// regardless of the APIType. This is useful when the embedding URL points to
	// a gateway proxy (e.g. another AT instance) that authenticates via Bearer
	// token and injects the provider-specific key itself.
	BearerAuth bool

	// Proxy is an optional proxy URL.
	Proxy string

	// InsecureSkipVerify disables TLS cert verification.
	InsecureSkipVerify bool
}

// NewATEmbedderClient creates a new ATEmbedderClient from provider configuration.
func NewATEmbedderClient(cfg ATEmbedderConfig) (*ATEmbedderClient, error) {
	if cfg.BaseURL == "" && cfg.EmbeddingURL == "" {
		return nil, fmt.Errorf("embedding base URL or embedding URL is required")
	}

	apiType := strings.ToLower(cfg.APIType)
	if apiType == "" {
		apiType = "openai"
	}

	// When no explicit EmbeddingURL is set, Model is required to derive the URL.
	if cfg.EmbeddingURL == "" && cfg.Model == "" {
		return nil, fmt.Errorf("embedding model is required when embedding URL is not set")
	}

	// Determine the embeddings endpoint URL.
	var embeddingsURL string
	if cfg.EmbeddingURL != "" {
		embeddingsURL = cfg.EmbeddingURL
	} else {
		switch apiType {
		case "gemini":
			// Gemini batch endpoint: {base}/v1beta/models/{model}:batchEmbedContents
			base := strings.TrimSuffix(cfg.BaseURL, "/")
			embeddingsURL = fmt.Sprintf("%s/v1beta/models/%s:batchEmbedContents", base, cfg.Model)
		default:
			// OpenAI-compatible: derive from provider base URL.
			var err error
			embeddingsURL, err = deriveEmbeddingsURL(cfg.BaseURL)
			if err != nil {
				return nil, fmt.Errorf("derive embeddings URL: %w", err)
			}
		}
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.InsecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}
	if cfg.Proxy != "" {
		proxyURL, err := url.Parse(cfg.Proxy)
		if err != nil {
			return nil, fmt.Errorf("parse proxy URL: %w", err)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	return &ATEmbedderClient{
		embeddingsURL: embeddingsURL,
		model:         cfg.Model,
		apiKey:        cfg.APIKey,
		bearerAuth:    cfg.BearerAuth,
		apiType:       apiType,
		client:        &http.Client{Transport: transport},
	}, nil
}

// CreateEmbedding implements the langchaingo EmbedderClient interface.
func (c *ATEmbedderClient) CreateEmbedding(ctx context.Context, texts []string) ([][]float32, error) {
	switch c.apiType {
	case "gemini":
		return c.createEmbeddingGemini(ctx, texts)
	default:
		return c.createEmbeddingOpenAI(ctx, texts)
	}
}

// ─── OpenAI Format ───

// createEmbeddingOpenAI calls the OpenAI-compatible /v1/embeddings endpoint.
func (c *ATEmbedderClient) createEmbeddingOpenAI(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody := openaiEmbeddingRequest{
		Model: c.model,
		Input: texts,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.embeddingsURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create embedding request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read embedding response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding request failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result openaiEmbeddingResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("unmarshal embedding response: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("embedding API error: %s", result.Error.Message)
	}

	// Sort by index to ensure correct ordering.
	embs := make([][]float32, len(result.Data))
	for _, d := range result.Data {
		if d.Index < 0 || d.Index >= len(embs) {
			return nil, fmt.Errorf("embedding response index %d out of range [0, %d)", d.Index, len(embs))
		}
		embs[d.Index] = d.Embedding
	}

	return embs, nil
}

// ─── Gemini Format ───

// createEmbeddingGemini always uses the batchEmbedContents endpoint since
// langchaingo's EmbedderImpl always sends texts as batches (even single texts
// are sent as []string{text}).
func (c *ATEmbedderClient) createEmbeddingGemini(ctx context.Context, texts []string) ([][]float32, error) {
	// Build the model field for the request body.
	// If model is set, use "models/{model}". If empty (URL already contains model),
	// omit it from the request.
	modelField := ""
	if c.model != "" {
		modelField = "models/" + c.model
	}

	requests := make([]geminiEmbedContentRequest, len(texts))
	for i, text := range texts {
		requests[i] = geminiEmbedContentRequest{
			Model: modelField,
			Content: geminiContent{
				Parts: []geminiPart{{Text: text}},
			},
		}
	}

	reqBody := geminiBatchEmbedRequest{
		Requests: requests,
	}

	result, err := c.doGeminiRequest(ctx, c.embeddingsURL, reqBody)
	if err != nil {
		return nil, err
	}

	var resp geminiBatchEmbedResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal gemini batch embedding response: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("gemini batch embedding error: %s (code %d)", resp.Error.Message, resp.Error.Code)
	}

	embs := make([][]float32, len(resp.Embeddings))
	for i, e := range resp.Embeddings {
		embs[i] = e.Values
	}

	return embs, nil
}

// doGeminiRequest performs an HTTP POST to a Gemini endpoint with the given body.
func (c *ATEmbedderClient) doGeminiRequest(ctx context.Context, targetURL string, reqBody any) ([]byte, error) {
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal gemini request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create gemini request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		if c.bearerAuth {
			// Gateway proxy auth: send API key as Bearer token. The gateway's
			// Proxy() method will inject the provider-specific API key header
			// (x-goog-api-key) when forwarding to Gemini.
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		} else {
			req.Header.Set("x-goog-api-key", c.apiKey)
		}
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini embedding request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read gemini response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini embedding request failed (status %d): %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// ─── URL Derivation ───

// deriveEmbeddingsURL converts a provider base URL to an OpenAI-compatible
// embeddings endpoint URL.
// Examples:
//
//	"https://api.openai.com/v1/chat/completions" → "https://api.openai.com/v1/embeddings"
//	"https://api.openai.com/v1/embeddings"       → "https://api.openai.com/v1/embeddings" (already correct)
//	"http://localhost:11434/v1/chat/completions"  → "http://localhost:11434/v1/embeddings"  (Ollama)
//	"https://example.com/v1"                      → "https://example.com/v1/embeddings"
func deriveEmbeddingsURL(baseURL string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base URL %q: %w", baseURL, err)
	}

	path := u.Path

	// If it already ends with /embeddings, use as-is.
	if len(path) >= 11 && path[len(path)-11:] == "/embeddings" {
		return u.String(), nil
	}

	// Strip known suffixes to get the versioned base path.
	suffixes := []string{"/chat/completions", "/completions"}
	for _, suffix := range suffixes {
		if len(path) >= len(suffix) && path[len(path)-len(suffix):] == suffix {
			path = path[:len(path)-len(suffix)]
			break
		}
	}

	// Append /embeddings.
	if len(path) > 0 && path[len(path)-1] == '/' {
		path += "embeddings"
	} else {
		path += "/embeddings"
	}

	u.Path = path

	return u.String(), nil
}

// ─── OpenAI Embeddings API Types ───

type openaiEmbeddingRequest struct {
	Model string   `json:"model,omitempty"`
	Input []string `json:"input"`
}

type openaiEmbeddingResponse struct {
	Data  []openaiEmbeddingData `json:"data"`
	Error *openaiEmbeddingError `json:"error,omitempty"`
	Usage *openaiEmbeddingUsage `json:"usage,omitempty"`
}

type openaiEmbeddingData struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

type openaiEmbeddingError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

type openaiEmbeddingUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// ─── Gemini Embedding API Types ───
//
// Batch: POST /v1beta/models/{model}:batchEmbedContents
//   Request:  { requests: [ { model, content: { parts: [{ text }] } } ] }
//   Response: { embeddings: [ { values: [] } ] }

type geminiEmbedContentRequest struct {
	Model   string        `json:"model,omitempty"`
	Content geminiContent `json:"content"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiEmbeddingValues struct {
	Values []float32 `json:"values"`
}

type geminiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

type geminiBatchEmbedRequest struct {
	Requests []geminiEmbedContentRequest `json:"requests"`
}

type geminiBatchEmbedResponse struct {
	Embeddings []geminiEmbeddingValues `json:"embeddings,omitempty"`
	Error      *geminiError            `json:"error,omitempty"`
}
