package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/worldline-go/klient"

	"github.com/rakunlabs/at/internal/service"
)

const DefaultBaseURL = "https://api.openai.com/v1/chat/completions"

type Provider struct {
	APIKey  string
	Model   string
	BaseURL string

	client      *klient.Client
	tokenSource TokenSource
}

// Option configures the Provider.
type Option func(*Provider)

// WithTokenSource sets a token source for per-request authentication.
// When set, the token source is called before each request and the returned
// token is used as the Bearer token, overriding the static APIKey.
func WithTokenSource(ts TokenSource) Option {
	return func(p *Provider) {
		p.tokenSource = ts
	}
}

// New creates an OpenAI-compatible provider.
//
// extraHeaders allows setting additional HTTP headers for providers that
// require them (e.g., GitHub Models recommends Accept and X-GitHub-Api-Version).
func New(apiKey, model, baseURL string, extraHeaders map[string]string, opts ...Option) (*Provider, error) {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}

	headers := http.Header{
		"Content-Type": []string{"application/json"},
	}
	if apiKey != "" {
		headers["Authorization"] = []string{"Bearer " + apiKey}
	}
	for k, v := range extraHeaders {
		headers[k] = []string{v}
	}

	client, err := klient.New(
		klient.WithBaseURL(baseURL),
		klient.WithLogger(slog.Default()),
		klient.WithHeaderSet(headers),
	)
	if err != nil {
		return nil, err
	}

	p := &Provider{
		APIKey:  apiKey,
		Model:   model,
		BaseURL: baseURL,
		client:  client,
	}

	for _, o := range opts {
		o(p)
	}

	return p, nil
}

type OpenAIResponse struct {
	Error   *OpenAIError `json:"error,omitempty"`
	Choices []Choice     `json:"choices"`
}

type OpenAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

type Choice struct {
	Message      ChoiceMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type ChoiceMessage struct {
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func (p *Provider) Chat(ctx context.Context, model string, messages []service.Message, tools []service.Tool) (*service.LLMResponse, error) {
	if model == "" {
		model = p.Model
	}

	reqBody := p.buildRequestBody(model, messages, tools)

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	// If a token source is configured, get a fresh token and set it on the
	// request. klient's TransportKlient only applies default headers when they
	// are not already present, so this overrides the static APIKey header.
	if p.tokenSource != nil {
		token, err := p.tokenSource.Token(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get auth token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	var result OpenAIResponse
	if err := p.client.Do(req, func(r *http.Response) error {
		bodyData, err := io.ReadAll(r.Body)
		if err != nil {
			return err
		}

		if err := json.Unmarshal(bodyData, &result); err != nil {
			return fmt.Errorf("failed to decode response: %w (body: %s)", err, string(bodyData))
		}

		return nil
	}); err != nil {
		return nil, err
	}

	if result.Error != nil {
		return &service.LLMResponse{
			Content:  fmt.Sprintf("Error from provider: %s", result.Error.Message),
			Finished: true,
		}, nil
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("no response choices from provider")
	}

	choice := result.Choices[0]
	llmResp := &service.LLMResponse{
		Content:  choice.Message.Content,
		Finished: choice.FinishReason != "tool_calls",
	}

	for _, tc := range choice.Message.ToolCalls {
		var args map[string]any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return nil, fmt.Errorf("failed to parse tool call arguments: %w", err)
		}

		llmResp.ToolCalls = append(llmResp.ToolCalls, service.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}

	return llmResp, nil
}

// ─── Streaming ───

// streamChoice is the SSE chunk format returned by OpenAI-compatible APIs.
type streamChoice struct {
	Delta        streamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type streamDelta struct {
	Role      string     `json:"role,omitempty"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type streamResponse struct {
	Error   *OpenAIError   `json:"error,omitempty"`
	Choices []streamChoice `json:"choices"`
}

// ChatStream implements service.LLMStreamProvider for true SSE streaming.
func (p *Provider) ChatStream(ctx context.Context, model string, messages []service.Message, tools []service.Tool) (<-chan service.StreamChunk, error) {
	if model == "" {
		model = p.Model
	}

	reqBody := p.buildRequestBody(model, messages, tools)
	reqBody["stream"] = true

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	// Override auth header when a token source is configured (see Chat() comment).
	if p.tokenSource != nil {
		token, err := p.tokenSource.Token(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get auth token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	// Use the klient's HTTP client which has transport with headers and base URL.
	resp, err := p.client.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("streaming request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		bodyData, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("provider returned status %d: %s", resp.StatusCode, string(bodyData))
	}

	ch := make(chan service.StreamChunk, 64)

	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()

			// Skip empty lines and SSE comments
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}

			// SSE data lines
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")

			// End of stream
			if data == "[DONE]" {
				return
			}

			var sr streamResponse
			if err := json.Unmarshal([]byte(data), &sr); err != nil {
				ch <- service.StreamChunk{Error: fmt.Errorf("failed to parse SSE chunk: %w", err)}
				return
			}

			if sr.Error != nil {
				ch <- service.StreamChunk{Error: fmt.Errorf("provider error: %s", sr.Error.Message)}
				return
			}

			if len(sr.Choices) == 0 {
				continue
			}

			choice := sr.Choices[0]
			chunk := service.StreamChunk{
				Content: choice.Delta.Content,
			}

			// Parse tool calls from delta
			for _, tc := range choice.Delta.ToolCalls {
				var args map[string]any
				if tc.Function.Arguments != "" {
					json.Unmarshal([]byte(tc.Function.Arguments), &args)
				}
				chunk.ToolCalls = append(chunk.ToolCalls, service.ToolCall{
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: args,
				})
			}

			if choice.FinishReason != nil {
				chunk.FinishReason = *choice.FinishReason
			}

			ch <- chunk
		}

		if err := scanner.Err(); err != nil {
			ch <- service.StreamChunk{Error: fmt.Errorf("stream read error: %w", err)}
		}
	}()

	return ch, nil
}

// buildRequestBody creates the common request body for Chat and ChatStream.
func (p *Provider) buildRequestBody(model string, messages []service.Message, tools []service.Tool) map[string]any {
	openaiTools := make([]map[string]any, len(tools))
	for i, tool := range tools {
		openaiTools[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  tool.InputSchema,
			},
		}
	}

	reqMessages := make([]any, len(messages))
	for i, msg := range messages {
		if m, ok := msg.Content.(map[string]any); ok {
			reqMessages[i] = m
		} else {
			reqMessages[i] = map[string]any{
				"role":    msg.Role,
				"content": msg.Content,
			}
		}
	}

	reqBody := map[string]any{
		"model":    model,
		"messages": reqMessages,
	}
	if len(tools) > 0 {
		reqBody["tools"] = openaiTools
	}

	return reqBody
}
