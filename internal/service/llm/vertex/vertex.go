package vertex

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
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/rakunlabs/at/internal/service"
)

// Vertex AI OpenAI-compatible endpoint format:
// https://{LOCATION}-aiplatform.googleapis.com/v1/projects/{PROJECT_ID}/locations/{LOCATION}/endpoints/openapi/chat/completions

const scope = "https://www.googleapis.com/auth/cloud-platform"

type Provider struct {
	Model       string
	EndpointURL string

	tokenSource oauth2.TokenSource
	client      *klient.Client
}

// New creates a Vertex AI provider.
//
// endpointURL is the full OpenAI-compatible chat completions endpoint, e.g.:
//
//	https://us-central1-aiplatform.googleapis.com/v1/projects/my-project/locations/us-central1/endpoints/openapi/chat/completions
//
// proxy is an optional HTTP/HTTPS/SOCKS5 proxy URL. If empty, no proxy is used.
// Authentication uses Google Application Default Credentials (ADC).
// Set GOOGLE_APPLICATION_CREDENTIALS env var to your service account key file,
// or run on GCE/Cloud Run/GKE where ADC is automatically available.
func New(model, endpointURL, proxy string, insecureSkipVerify bool) (*Provider, error) {
	if endpointURL == "" {
		return nil, fmt.Errorf("vertex provider requires a base_url with the full endpoint URL, e.g.: " +
			"https://us-central1-aiplatform.googleapis.com/v1/projects/PROJECT/locations/LOCATION/endpoints/openapi/chat/completions")
	}

	// Use Application Default Credentials for automatic token refresh.
	ts, err := google.DefaultTokenSource(context.Background(), scope)
	if err != nil {
		return nil, fmt.Errorf("failed to get Google credentials (set GOOGLE_APPLICATION_CREDENTIALS or run on GCE): %w", err)
	}

	klientOpts := []klient.OptionClientFn{
		klient.WithDisableBaseURLCheck(true),
		klient.WithLogger(slog.Default()),
	}
	if proxy != "" {
		klientOpts = append(klientOpts, klient.WithProxy(proxy))
	}
	if insecureSkipVerify {
		klientOpts = append(klientOpts, klient.WithInsecureSkipVerify(true))
	}

	client, err := klient.New(klientOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create http client: %w", err)
	}

	return &Provider{
		Model:       model,
		EndpointURL: endpointURL,
		tokenSource: ts,
		client:      client,
	}, nil
}

// Response types matching the OpenAI-compatible format returned by Vertex AI.
type vertexResponse struct {
	Error   *vertexError `json:"error,omitempty"`
	Choices []choice     `json:"choices"`
	Usage   *vertexUsage `json:"usage,omitempty"`
}

type vertexError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

type vertexUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type choice struct {
	Message      choiceMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type choiceMessage struct {
	Content   string     `json:"content"`
	ToolCalls []toolCall `json:"tool_calls"`
}

type toolCall struct {
	ID       string       `json:"id"`
	Function functionCall `json:"function"`
}

type functionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func (p *Provider) Chat(ctx context.Context, model string, messages []service.Message, tools []service.Tool) (*service.LLMResponse, error) {
	if model == "" {
		model = p.Model
	}

	// Get a fresh access token (auto-refreshes when expired).
	token, err := p.tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %w", err)
	}

	reqBody := p.buildRequestBody(model, messages, tools)

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.EndpointURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)

	var result vertexResponse
	var headers http.Header
	if err := p.client.Do(req, func(r *http.Response) error {
		headers = r.Header
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
			Content:  fmt.Sprintf("Error from Vertex AI: %s (code: %d)", result.Error.Message, result.Error.Code),
			Finished: true,
		}, nil
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("no response choices from Vertex AI")
	}

	ch := result.Choices[0]
	llmResp := &service.LLMResponse{
		Content:  ch.Message.Content,
		Finished: ch.FinishReason != "tool_calls",
		Header:   headers,
	}

	if result.Usage != nil {
		llmResp.Usage = service.Usage{
			PromptTokens:     result.Usage.PromptTokens,
			CompletionTokens: result.Usage.CompletionTokens,
			TotalTokens:      result.Usage.TotalTokens,
		}
	}

	for _, tc := range ch.Message.ToolCalls {
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

// streamChoice is the SSE chunk format from Vertex AI (OpenAI-compatible).
type streamChoice struct {
	Delta        streamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type streamDelta struct {
	Role      string     `json:"role,omitempty"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
}

type streamResponse struct {
	Error   *vertexError   `json:"error,omitempty"`
	Choices []streamChoice `json:"choices"`
	Usage   *vertexUsage   `json:"usage,omitempty"`
}

// ChatStream implements service.LLMStreamProvider for Vertex AI's
// OpenAI-compatible SSE streaming format.
func (p *Provider) ChatStream(ctx context.Context, model string, messages []service.Message, tools []service.Tool) (<-chan service.StreamChunk, http.Header, error) {
	if model == "" {
		model = p.Model
	}

	token, err := p.tokenSource.Token()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get access token: %w", err)
	}

	reqBody := p.buildRequestBody(model, messages, tools)
	reqBody["stream"] = true
	reqBody["stream_options"] = map[string]any{"include_usage": true}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.EndpointURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)

	resp, err := p.client.HTTP.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("streaming request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		bodyData, _ := io.ReadAll(resp.Body)
		return nil, nil, fmt.Errorf("vertex returned status %d: %s", resp.StatusCode, string(bodyData))
	}

	ch := make(chan service.StreamChunk, 64)

	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // 10MB max line size (images can produce large SSE events)
		for scanner.Scan() {
			line := scanner.Text()

			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}

			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")

			if data == "[DONE]" {
				return
			}

			var sr streamResponse
			if err := json.Unmarshal([]byte(data), &sr); err != nil {
				ch <- service.StreamChunk{Error: fmt.Errorf("failed to parse SSE chunk: %w", err)}
				return
			}

			if sr.Error != nil {
				ch <- service.StreamChunk{Error: fmt.Errorf("vertex error: %s (code: %d)", sr.Error.Message, sr.Error.Code)}
				return
			}

			// Vertex may send a final chunk with empty choices but populated
			// usage when stream_options.include_usage is set. Capture it.
			if len(sr.Choices) == 0 {
				if sr.Usage != nil {
					ch <- service.StreamChunk{
						Usage: &service.Usage{
							PromptTokens:     sr.Usage.PromptTokens,
							CompletionTokens: sr.Usage.CompletionTokens,
							TotalTokens:      sr.Usage.TotalTokens,
						},
					}
				}
				continue
			}

			sChoice := sr.Choices[0]
			chunk := service.StreamChunk{
				Content: sChoice.Delta.Content,
			}

			for _, tc := range sChoice.Delta.ToolCalls {
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

			if sChoice.FinishReason != nil {
				chunk.FinishReason = *sChoice.FinishReason
			}

			ch <- chunk
		}

		if err := scanner.Err(); err != nil {
			ch <- service.StreamChunk{Error: fmt.Errorf("stream read error: %w", err)}
		}
	}()

	return ch, resp.Header, nil
}

func (p *Provider) SendRequest(ctx context.Context, method string, path string, body io.Reader, headers http.Header) (*http.Response, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	// Vertex EndpointURL is typically:
	// https://{LOCATION}-aiplatform.googleapis.com/v1/projects/{PROJECT}/locations/{LOCATION}/endpoints/openapi/chat/completions
	// We want to extract the base part, e.g. "https://{LOCATION}-aiplatform.googleapis.com" or slightly deeper?
	// The path provided is likely relative to the API version or project.
	// If path starts with /v1/, we assume it's relative to the host.
	// Let's parse the EndpointURL.
	baseURL := p.EndpointURL
	// Naive strip
	if idx := strings.Index(baseURL, "/v1/"); idx > 0 {
		baseURL = baseURL[:idx]
	}

	url := baseURL + path

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}

	for k, v := range headers {
		req.Header[k] = v
	}

	token, err := p.tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)

	return p.client.HTTP.Do(req)
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
