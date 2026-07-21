// Package cohere implements an LLM provider that talks to Cohere's native
// chat (`/v2/chat`), rerank (`/v2/rerank`), and embeddings (`/v2/embed`)
// endpoints.
//
// Cohere's wire format is close to OpenAI but not identical, and the
// rerank endpoint is something nobody else offers natively — first-class
// support is meaningful for retrieval use cases.
package cohere

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/common"
	"github.com/rakunlabs/at/internal/service/ratelimit"
)

const (
	defaultBaseURL = "https://api.cohere.com"
	defaultModel   = "command-r-plus-08-2024"
)

// Provider implements service.LLMProvider plus EmbeddingProvider and RerankProvider.
type Provider struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
	limiter    *ratelimit.Limiter
}

// Option mutates a Provider during construction.
type Option func(*Provider)

// WithRateLimiter attaches a per-provider rate limiter.
func WithRateLimiter(l *ratelimit.Limiter) Option {
	return func(p *Provider) { p.limiter = l }
}

// New creates a Cohere provider.
func New(apiKey, model, baseURL, proxy string, insecureSkipVerify bool, opts ...Option) (*Provider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("cohere: api key required")
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if model == "" {
		model = defaultModel
	}

	httpClient := &http.Client{Timeout: 5 * time.Minute}
	if proxy != "" || insecureSkipVerify {
		t := http.DefaultTransport.(*http.Transport).Clone()
		if proxy != "" {
			u, err := url.Parse(proxy)
			if err != nil {
				return nil, fmt.Errorf("parse proxy URL: %w", err)
			}
			t.Proxy = http.ProxyURL(u)
		}
		if insecureSkipVerify {
			if t.TLSClientConfig == nil {
				t.TLSClientConfig = &tls.Config{} //nolint:gosec // operator opt-in
			}
			t.TLSClientConfig.InsecureSkipVerify = true
		}
		httpClient.Transport = t
	}

	p := &Provider{
		apiKey:     apiKey,
		model:      model,
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		httpClient: httpClient,
	}
	for _, o := range opts {
		o(p)
	}
	return p, nil
}

// doJSON wraps an authenticated JSON request/response.
func (p *Provider) doJSON(ctx context.Context, method, path string, body any, out any) (http.Header, int, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, reader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("cohere http: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.Header, resp.StatusCode, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return resp.Header, resp.StatusCode, &service.RateLimitError{
			StatusCode: resp.StatusCode,
			RetryAfter: common.ParseRetryAfter(resp.Header),
			Provider:   "cohere",
			Message:    string(respBody),
			Underlying: fmt.Errorf("cohere 429: %s", string(respBody)),
		}
	}
	if resp.StatusCode >= 400 {
		return resp.Header, resp.StatusCode, fmt.Errorf("cohere API error (status %d): %s", resp.StatusCode, string(respBody))
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return resp.Header, resp.StatusCode, fmt.Errorf("decode: %w (body: %s)", err, string(respBody))
		}
	}
	return resp.Header, resp.StatusCode, nil
}

// ─── Chat ───

type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	Tools          []chatTool    `json:"tools,omitempty"`
	Temperature    *float64      `json:"temperature,omitempty"`
	P              *float64      `json:"p,omitempty"`
	MaxTokens      *int          `json:"max_tokens,omitempty"`
	StopSeq        []string      `json:"stop_sequences,omitempty"`
	Seed           *int          `json:"seed,omitempty"`
	ToolChoice     string        `json:"tool_choice,omitempty"`
	ResponseFormat any           `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters,omitempty"`
	} `json:"function"`
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatResponse struct {
	ID      string `json:"id"`
	Message struct {
		Role      string         `json:"role"`
		Content   []contentBlock `json:"content"`
		ToolCalls []chatToolCall `json:"tool_calls"`
	} `json:"message"`
	FinishReason string `json:"finish_reason"`
	Usage        struct {
		BilledUnits struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"billed_units"`
		Tokens struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"tokens"`
	} `json:"usage"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Chat implements service.LLMProvider.
func (p *Provider) Chat(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (*service.LLMResponse, error) {
	if model == "" {
		model = p.model
	}
	release, err := p.limiter.Acquire(ctx, common.EstimateInputTokens("", messages, tools))
	if err != nil {
		return nil, err
	}
	defer release()

	body := chatRequest{
		Model:    model,
		Messages: translateMessagesToCohere(messages),
	}
	if opts != nil {
		body.Temperature = opts.Temperature
		body.P = opts.TopP
		if opts.MaxCompletionTokens != nil {
			body.MaxTokens = opts.MaxCompletionTokens
		} else if opts.MaxTokens != nil {
			body.MaxTokens = opts.MaxTokens
		}
		if len(opts.Stop) > 0 {
			body.StopSeq = opts.Stop
		}
		body.Seed = opts.Seed
		body.ToolChoice = translateCohereToolChoice(opts.ToolChoice)
		body.ResponseFormat = translateCohereResponseFormat(opts.ResponseFormat)
	}
	for _, t := range tools {
		ct := chatTool{Type: "function"}
		ct.Function.Name = t.Name
		ct.Function.Description = t.Description
		ct.Function.Parameters = service.SanitizeSchema(t.InputSchema)
		body.Tools = append(body.Tools, ct)
	}

	// Marshal once, merging extra_body for the litellm escape hatch.
	jsonData, err := marshalCohereWithExtra(body, opts)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v2/chat", bytes.NewReader(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cohere http: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, &service.RateLimitError{
			StatusCode: resp.StatusCode,
			RetryAfter: common.ParseRetryAfter(resp.Header),
			Provider:   "cohere",
			Message:    string(respBody),
			Underlying: fmt.Errorf("cohere 429: %s", string(respBody)),
		}
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("cohere API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var parsed chatResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode response: %w (body: %s)", err, string(respBody))
	}

	out := &service.LLMResponse{
		Header:       resp.Header,
		Finished:     parsed.FinishReason != "TOOL_CALL",
		FinishReason: parsed.FinishReason,
		Usage: service.Usage{
			PromptTokens:     pickInt(parsed.Usage.Tokens.InputTokens, parsed.Usage.BilledUnits.InputTokens),
			CompletionTokens: pickInt(parsed.Usage.Tokens.OutputTokens, parsed.Usage.BilledUnits.OutputTokens),
		},
	}
	out.Usage.TotalTokens = out.Usage.PromptTokens + out.Usage.CompletionTokens

	for _, block := range parsed.Message.Content {
		if block.Type == "text" {
			out.Content += block.Text
		}
	}
	for _, tc := range parsed.Message.ToolCalls {
		var args map[string]any
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
		out.ToolCalls = append(out.ToolCalls, service.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}
	return out, nil
}

func translateMessagesToCohere(messages []service.Message) []chatMessage {
	out := make([]chatMessage, 0, len(messages))
	for _, m := range messages {
		cm := chatMessage{Role: m.Role}
		switch c := m.Content.(type) {
		case string:
			cm.Content = c
		case map[string]any:
			// Gateway passthrough — OpenAI-shape.
			if txt, ok := c["content"].(string); ok {
				cm.Content = txt
			}
			if tcID, ok := c["tool_call_id"].(string); ok {
				cm.ToolCallID = tcID
			}
			if raw, ok := c["tool_calls"].([]any); ok {
				for _, x := range raw {
					tcMap, ok := x.(map[string]any)
					if !ok {
						continue
					}
					var tc chatToolCall
					tc.ID, _ = tcMap["id"].(string)
					tc.Type, _ = tcMap["type"].(string)
					if fn, ok := tcMap["function"].(map[string]any); ok {
						tc.Function.Name, _ = fn["name"].(string)
						tc.Function.Arguments, _ = fn["arguments"].(string)
					}
					cm.ToolCalls = append(cm.ToolCalls, tc)
				}
			}
		default:
			cm.Content = c
		}
		out = append(out, cm)
	}
	return out
}

// translateCohereToolChoice maps an OpenAI-style tool_choice to Cohere's
// v2/chat vocabulary. Cohere only supports "REQUIRED" and "NONE"; "auto" is
// the default (omitted). Forcing a *specific* tool is not supported — the
// closest behaviour is REQUIRED (the model must call some tool).
func translateCohereToolChoice(v any) string {
	switch x := v.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "required", "any":
			return "REQUIRED"
		case "none":
			return "NONE"
		}
	case map[string]any:
		t, _ := x["type"].(string)
		switch strings.ToLower(t) {
		case "function":
			return "REQUIRED"
		case "any", "required":
			return "REQUIRED"
		case "none":
			return "NONE"
		}
	}
	return ""
}

// translateCohereResponseFormat maps an OpenAI-style response_format to
// Cohere's v2/chat shape: {"type":"json_object","schema":{...}}.
func translateCohereResponseFormat(rf map[string]any) any {
	if len(rf) == 0 {
		return nil
	}
	t, _ := rf["type"].(string)
	switch t {
	case "json_object":
		return map[string]any{"type": "json_object"}
	case "json_schema":
		out := map[string]any{"type": "json_object"}
		if js, ok := rf["json_schema"].(map[string]any); ok {
			if schema, ok := js["schema"].(map[string]any); ok {
				out["schema"] = schema
			}
		}
		return out
	case "text":
		return map[string]any{"type": "text"}
	}
	return nil
}

// marshalCohereWithExtra marshals the typed chatRequest and merges
// opts.ExtraBody on top so callers can override or extend fields.
func marshalCohereWithExtra(body chatRequest, opts *service.ChatOptions) ([]byte, error) {
	base, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	if opts == nil || len(opts.ExtraBody) == 0 {
		return base, nil
	}
	var merged map[string]any
	if err := json.Unmarshal(base, &merged); err != nil {
		return nil, err
	}
	for k, v := range opts.ExtraBody {
		merged[k] = v
	}
	return json.Marshal(merged)
}

func pickInt(primary, fallback int) int {
	if primary != 0 {
		return primary
	}
	return fallback
}

// ─── Embeddings ───

type embedRequest struct {
	Model          string   `json:"model"`
	Texts          []string `json:"texts"`
	InputType      string   `json:"input_type"`
	EmbeddingTypes []string `json:"embedding_types"`
}

type embedResponse struct {
	Embeddings struct {
		Float [][]float64 `json:"float"`
	} `json:"embeddings"`
	Meta struct {
		BilledUnits struct {
			InputTokens int `json:"input_tokens"`
		} `json:"billed_units"`
	} `json:"meta"`
}

// CreateEmbedding implements service.EmbeddingProvider.
func (p *Provider) CreateEmbedding(ctx context.Context, req service.EmbeddingRequest) (*service.EmbeddingResponse, error) {
	model := req.Model
	if model == "" {
		model = "embed-english-v3.0"
	}
	body := embedRequest{
		Model:          model,
		Texts:          req.Input,
		InputType:      "search_document",
		EmbeddingTypes: []string{"float"},
	}
	var parsed embedResponse
	if _, _, err := p.doJSON(ctx, http.MethodPost, "/v2/embed", body, &parsed); err != nil {
		return nil, err
	}
	return &service.EmbeddingResponse{
		Embeddings: parsed.Embeddings.Float,
		Model:      model,
		Usage: service.Usage{
			PromptTokens: parsed.Meta.BilledUnits.InputTokens,
			TotalTokens:  parsed.Meta.BilledUnits.InputTokens,
		},
	}, nil
}

// ─── Rerank ───

type rerankRequest struct {
	Model           string   `json:"model"`
	Query           string   `json:"query"`
	Documents       []string `json:"documents"`
	TopN            int      `json:"top_n,omitempty"`
	ReturnDocuments *bool    `json:"return_documents,omitempty"`
}

type rerankResponse struct {
	ID      string `json:"id"`
	Results []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
		Document       *struct {
			Text string `json:"text"`
		} `json:"document,omitempty"`
	} `json:"results"`
	Meta struct {
		BilledUnits struct {
			SearchUnits int `json:"search_units"`
		} `json:"billed_units"`
	} `json:"meta"`
}

// Rerank implements service.RerankProvider.
func (p *Provider) Rerank(ctx context.Context, req service.RerankRequest) (*service.RerankResponse, error) {
	model := req.Model
	if model == "" {
		model = "rerank-english-v3.0"
	}
	body := rerankRequest{
		Model:           model,
		Query:           req.Query,
		Documents:       req.Documents,
		TopN:            req.TopN,
		ReturnDocuments: req.ReturnDocuments,
	}
	var parsed rerankResponse
	if _, _, err := p.doJSON(ctx, http.MethodPost, "/v2/rerank", body, &parsed); err != nil {
		return nil, err
	}
	out := &service.RerankResponse{
		ID:    parsed.ID,
		Model: model,
	}
	for _, r := range parsed.Results {
		entry := service.RerankResult{
			Index:          r.Index,
			RelevanceScore: r.RelevanceScore,
		}
		if r.Document != nil {
			entry.Document = r.Document.Text
		} else if req.ReturnDocuments != nil && *req.ReturnDocuments && r.Index < len(req.Documents) {
			entry.Document = req.Documents[r.Index]
		}
		out.Results = append(out.Results, entry)
	}
	return out, nil
}

// ─── Proxy ───

// Proxy forwards a raw HTTP request to the Cohere API.
func (p *Provider) Proxy(w http.ResponseWriter, r *http.Request, path string) error {
	target := p.baseURL + path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("read proxy body: %w", err)
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	for k, v := range r.Header {
		if strings.EqualFold(k, "authorization") || strings.EqualFold(k, "host") {
			continue
		}
		req.Header[k] = v
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		slog.Warn("cohere proxy copy failed", "error", err)
	}
	return nil
}
