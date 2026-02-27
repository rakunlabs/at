package antropic

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

const DefaultBaseURL = "https://api.anthropic.com"

type Provider struct {
	APIKey string
	Model  string

	client *klient.Client
}

type AnthropicResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Error      Error          `json:"error"`
	Role       string         `json:"role"`
	Content    []ContentBlock `json:"content"`
	Model      string         `json:"model"`
	StopReason string         `json:"stop_reason"`
	Usage      Usage          `json:"usage"`
}

type Error struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Response structures
type ContentBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text"`
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func New(apiKey, model, baseURL, proxy string, insecureSkipVerify bool) (*Provider, error) {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}

	klientOpts := []klient.OptionClientFn{
		klient.WithBaseURL(baseURL),
		klient.WithLogger(slog.Default()),
		klient.WithHeaderSet(http.Header{
			"X-Api-Key":         []string{apiKey},
			"Anthropic-Version": []string{"2023-06-01"},
			"Content-Type":      []string{"application/json"},
		}),
	}
	if proxy != "" {
		klientOpts = append(klientOpts, klient.WithProxy(proxy))
	}
	if insecureSkipVerify {
		klientOpts = append(klientOpts, klient.WithInsecureSkipVerify(true))
	}

	client, err := klient.New(klientOpts...)
	if err != nil {
		return nil, err
	}

	return &Provider{
		APIKey: apiKey,
		Model:  model,
		client: client,
	}, nil
}

func (p *Provider) Chat(ctx context.Context, model string, messages []service.Message, tools []service.Tool) (*service.LLMResponse, error) {
	if model == "" {
		model = p.Model
	}

	reqBody := p.buildRequestBody(model, messages, tools)

	jsonData, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/messages", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	var result AnthropicResponse
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

	llmResp := &service.LLMResponse{
		Finished: result.StopReason != "tool_use",
		Header:   headers,
	}

	if result.Type == "error" {
		llmResp.Content = fmt.Sprintf("Error from Anthropic: %s", result.Error.Message)

		return llmResp, nil
	}

	// Map upstream usage to the internal Usage struct.
	llmResp.Usage = service.Usage{
		PromptTokens:     result.Usage.InputTokens,
		CompletionTokens: result.Usage.OutputTokens,
		TotalTokens:      result.Usage.InputTokens + result.Usage.OutputTokens,
	}

	for _, block := range result.Content {
		switch block.Type {
		case "text":
			llmResp.Content += block.Text
		case "tool_use":
			llmResp.ToolCalls = append(llmResp.ToolCalls, service.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Input,
			})
		}
	}

	return llmResp, nil
}

// ─── Streaming ───

// Anthropic SSE event types for streaming.
type streamEvent struct {
	Type  string          `json:"type"`
	Delta json.RawMessage `json:"delta,omitempty"`

	// For content_block_start
	ContentBlock *ContentBlock `json:"content_block,omitempty"`
}

type textDelta struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolInputDelta struct {
	Type        string `json:"type"`
	PartialJSON string `json:"partial_json"`
}

type messageDelta struct {
	StopReason string `json:"stop_reason"`
	Usage      *Usage `json:"usage,omitempty"` // output_tokens on message_delta
}

// messageStartBody is the top-level structure of an Anthropic message_start event.
type messageStartBody struct {
	Type    string               `json:"type"`
	Message *messageStartMessage `json:"message,omitempty"`
}

type messageStartMessage struct {
	Usage *Usage `json:"usage,omitempty"` // input_tokens on message_start
}

// ChatStream implements service.LLMStreamProvider for Anthropic's SSE format.
func (p *Provider) ChatStream(ctx context.Context, model string, messages []service.Message, tools []service.Tool) (<-chan service.StreamChunk, http.Header, error) {
	if model == "" {
		model = p.Model
	}

	reqBody := p.buildRequestBody(model, messages, tools)
	reqBody["stream"] = true

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/messages", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, nil, err
	}

	// Use the klient's HTTP client directly for streaming.
	resp, err := p.client.HTTP.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("streaming request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		bodyData, _ := io.ReadAll(resp.Body)
		return nil, nil, fmt.Errorf("anthropic returned status %d: %s", resp.StatusCode, string(bodyData))
	}

	ch := make(chan service.StreamChunk, 64)

	go func() {
		defer close(ch)
		defer resp.Body.Close()

		// Track the current content block for tool_use streaming.
		// Anthropic streams tool input as partial JSON fragments that
		// need to be accumulated and parsed at the end.
		var currentToolID string
		var currentToolName string
		var toolInputBuf strings.Builder

		// Accumulate token usage from message_start and message_delta events.
		var usageInputTokens int
		var usageOutputTokens int

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // 10MB max line size (images can produce large SSE events)
		for scanner.Scan() {
			line := scanner.Text()

			// Skip empty lines and SSE comments
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}

			// We only care about data lines
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")

			var event streamEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				ch <- service.StreamChunk{Error: fmt.Errorf("failed to parse SSE event: %w", err)}
				return
			}

			switch event.Type {
			case "message_start":
				// message_start contains initial usage (input_tokens).
				var msb messageStartBody
				if err := json.Unmarshal([]byte(data), &msb); err == nil && msb.Message != nil && msb.Message.Usage != nil {
					usageInputTokens = msb.Message.Usage.InputTokens
				}

			case "content_block_start":
				// A new content block is starting. If it's a tool_use block,
				// track its ID and name for accumulating input fragments.
				if event.ContentBlock != nil && event.ContentBlock.Type == "tool_use" {
					currentToolID = event.ContentBlock.ID
					currentToolName = event.ContentBlock.Name
					toolInputBuf.Reset()
				}

			case "content_block_delta":
				if len(event.Delta) == 0 {
					continue
				}

				// Try text delta first
				var td textDelta
				if err := json.Unmarshal(event.Delta, &td); err == nil && td.Type == "text_delta" {
					ch <- service.StreamChunk{Content: td.Text}
					continue
				}

				// Try tool input delta
				var tid toolInputDelta
				if err := json.Unmarshal(event.Delta, &tid); err == nil && tid.Type == "input_json_delta" {
					toolInputBuf.WriteString(tid.PartialJSON)
				}

			case "content_block_stop":
				// If we were accumulating tool input, parse and emit it now.
				if currentToolID != "" {
					var args map[string]any
					if toolInputBuf.Len() > 0 {
						json.Unmarshal([]byte(toolInputBuf.String()), &args)
					}
					ch <- service.StreamChunk{
						ToolCalls: []service.ToolCall{{
							ID:        currentToolID,
							Name:      currentToolName,
							Arguments: args,
						}},
					}
					currentToolID = ""
					currentToolName = ""
					toolInputBuf.Reset()
				}

			case "message_delta":
				if len(event.Delta) == 0 {
					continue
				}
				var md messageDelta
				if err := json.Unmarshal(event.Delta, &md); err == nil {
					if md.Usage != nil {
						usageOutputTokens = md.Usage.OutputTokens
					}
					if md.StopReason != "" {
						finishReason := "stop"
						if md.StopReason == "tool_use" {
							finishReason = "tool_calls"
						}
						ch <- service.StreamChunk{FinishReason: finishReason}
					}
				}

			case "message_stop":
				// Emit accumulated usage on the final event.
				total := usageInputTokens + usageOutputTokens
				ch <- service.StreamChunk{
					Usage: &service.Usage{
						PromptTokens:     usageInputTokens,
						CompletionTokens: usageOutputTokens,
						TotalTokens:      total,
					},
				}
				return

			case "error":
				var errMsg struct {
					Error Error `json:"error"`
				}
				if err := json.Unmarshal([]byte(data), &errMsg); err == nil {
					ch <- service.StreamChunk{Error: fmt.Errorf("anthropic error: %s", errMsg.Error.Message)}
				} else {
					ch <- service.StreamChunk{Error: fmt.Errorf("anthropic stream error: %s", data)}
				}
				return
			}
		}

		if err := scanner.Err(); err != nil {
			ch <- service.StreamChunk{Error: fmt.Errorf("stream read error: %w", err)}
		}
	}()

	return ch, resp.Header, nil
}

// buildRequestBody creates the common request body for Chat and ChatStream.
func (p *Provider) buildRequestBody(model string, messages []service.Message, tools []service.Tool) map[string]any {
	anthropicTools := make([]map[string]any, len(tools))
	for i, tool := range tools {
		anthropicTools[i] = map[string]any{
			"name":         tool.Name,
			"description":  tool.Description,
			"input_schema": tool.InputSchema,
		}
	}

	// Extract system messages — Anthropic uses a top-level "system" parameter
	// rather than including system messages in the messages array.
	var systemPrompt string
	var filteredMessages []service.Message
	for _, msg := range messages {
		if msg.Role == "system" {
			if s, ok := msg.Content.(string); ok {
				if systemPrompt != "" {
					systemPrompt += "\n"
				}
				systemPrompt += s
			}
		} else {
			filteredMessages = append(filteredMessages, msg)
		}
	}

	reqBody := map[string]any{
		"model":      model,
		"max_tokens": 4096,
		"messages":   filteredMessages,
	}
	if systemPrompt != "" {
		reqBody["system"] = systemPrompt
	}
	if len(tools) > 0 {
		reqBody["tools"] = anthropicTools
	}

	return reqBody
}
