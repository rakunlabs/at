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
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/common"
	"github.com/rakunlabs/at/internal/service/ratelimit"
)

const CodexDefaultResponsesURL = "https://chatgpt.com/backend-api/codex/responses"

type codexModelsResponse struct {
	Models []struct {
		Slug string `json:"slug"`
	} `json:"models"`
}

// CodexProvider adapts AT's provider interfaces to the ChatGPT Codex Responses
// endpoint used by ChatGPT Plus and Pro subscriptions.
type CodexProvider struct {
	Model         string
	BaseURL       string
	AccountID     string
	ClientVersion string

	tokenSource TokenSource
	httpClient  *http.Client
	limiter     *ratelimit.Limiter
}

// CodexProviderOption configures a CodexProvider.
type CodexProviderOption func(*CodexProvider)

// WithCodexBaseURL overrides the Responses endpoint, primarily for tests.
func WithCodexBaseURL(baseURL string) CodexProviderOption {
	return func(p *CodexProvider) {
		if baseURL != "" {
			p.BaseURL = baseURL
		}
	}
}

// WithCodexHTTPClient replaces the HTTP client used for inference and proxying.
func WithCodexHTTPClient(client *http.Client) CodexProviderOption {
	return func(p *CodexProvider) {
		if client != nil {
			p.httpClient = client
		}
	}
}

// WithCodexRateLimiter attaches a per-provider rate limiter.
func WithCodexRateLimiter(limiter *ratelimit.Limiter) CodexProviderOption {
	return func(p *CodexProvider) {
		p.limiter = limiter
	}
}

// WithCodexClientVersion sets the semver sent to the Codex model catalog.
func WithCodexClientVersion(version string) CodexProviderOption {
	return func(p *CodexProvider) {
		p.ClientVersion = NormalizeCodexClientVersion(version)
	}
}

// NormalizeCodexClientVersion converts an AT release version to major.minor.patch.
func NormalizeCodexClientVersion(version string) string {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if index := strings.IndexByte(version, '-'); index >= 0 {
		version = version[:index]
	}
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return "0.0.0"
	}
	for _, part := range parts {
		if part == "" {
			return "0.0.0"
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return "0.0.0"
			}
		}
	}
	return version
}

// NewCodexProvider creates a provider for the ChatGPT Codex Responses API.
func NewCodexProvider(model, accountID string, tokenSource TokenSource, opts ...CodexProviderOption) *CodexProvider {
	p := &CodexProvider{
		Model:         model,
		BaseURL:       CodexDefaultResponsesURL,
		AccountID:     accountID,
		ClientVersion: "0.0.0",
		tokenSource:   tokenSource,
		httpClient:    http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// SetTokenRefreshCallback wires persistence to a refreshable Codex token source.
func (p *CodexProvider) SetTokenRefreshCallback(fn CodexTokenRefreshCallback) {
	if source, ok := p.tokenSource.(*CodexTokenSource); ok {
		source.SetRefreshCallback(fn)
	}
}

// SetTokenReloadCallback wires recovery from credentials rotated by another process.
func (p *CodexProvider) SetTokenReloadCallback(fn CodexTokenReloadCallback) {
	if source, ok := p.tokenSource.(*CodexTokenSource); ok {
		source.SetReloadCallback(fn)
	}
}

func (p *CodexProvider) accountID() string {
	if source, ok := p.tokenSource.(interface{ AccountID() string }); ok {
		if accountID := source.AccountID(); accountID != "" {
			return accountID
		}
	}
	return p.AccountID
}

func (p *CodexProvider) authorize(ctx context.Context, req *http.Request) error {
	if p.tokenSource == nil {
		return fmt.Errorf("Codex token source is not configured")
	}
	token, err := p.tokenSource.Token(ctx)
	if err != nil {
		return fmt.Errorf("get Codex access token: %w", err)
	}
	if token == "" {
		return fmt.Errorf("Codex token source returned an empty access token")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	accountID := p.accountID()
	if accountID == "" {
		return fmt.Errorf("ChatGPT account ID is missing; authorize the provider again")
	}
	req.Header.Set("ChatGPT-Account-ID", accountID)
	if req.Header.Get("Originator") == "" {
		req.Header.Set("Originator", "codex_cli_rs")
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "at")
	}
	return nil
}

// Chat implements service.LLMProvider by collecting the Codex SSE stream.
func (p *CodexProvider) Chat(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (*service.LLMResponse, error) {
	stream, headers, err := p.ChatStream(ctx, model, messages, tools, opts)
	if err != nil {
		return nil, err
	}

	response := &service.LLMResponse{Header: headers}
	for chunk := range stream {
		if chunk.Error != nil {
			return nil, chunk.Error
		}
		response.Content += chunk.Content
		response.ReasoningContent += chunk.ReasoningContent
		response.ToolCalls = append(response.ToolCalls, chunk.ToolCalls...)
		if chunk.Usage != nil {
			response.Usage = *chunk.Usage
		}
		if chunk.FinishReason != "" {
			response.FinishReason = chunk.FinishReason
		}
	}
	if response.FinishReason == "" {
		return nil, fmt.Errorf("Codex stream closed without response.completed")
	}
	response.Finished = response.FinishReason != "tool_calls"
	return response, nil
}

// ChatStream implements service.LLMStreamProvider using the Responses SSE protocol.
func (p *CodexProvider) ChatStream(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (<-chan service.StreamChunk, http.Header, error) {
	if model == "" {
		model = p.Model
	}
	release, err := p.limiter.Acquire(ctx, common.EstimateInputTokens("", messages, tools))
	if err != nil {
		return nil, nil, err
	}
	releaseOnce := sync.OnceFunc(release)

	body, err := json.Marshal(buildCodexRequest(model, messages, tools, opts))
	if err != nil {
		releaseOnce()
		return nil, nil, fmt.Errorf("marshal Codex request: %w", err)
	}
	send := func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("build Codex request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		if err := p.authorize(ctx, req); err != nil {
			return nil, err
		}
		return p.httpClient.Do(req)
	}

	resp, err := send()
	if err != nil {
		releaseOnce()
		return nil, nil, fmt.Errorf("Codex request failed: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp, err = p.recoverUnauthorized(ctx, resp, send)
		if err != nil {
			releaseOnce()
			return nil, nil, fmt.Errorf("Codex request recovery failed: %w", err)
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		defer releaseOnce()
		respBody, _ := io.ReadAll(resp.Body)
		message := codexErrorMessage(respBody)
		if resp.StatusCode == http.StatusTooManyRequests {
			return nil, nil, &service.RateLimitError{
				StatusCode: resp.StatusCode,
				RetryAfter: common.ParseRetryAfter(resp.Header),
				Provider:   "openai-codex",
				Message:    message,
				Underlying: fmt.Errorf("Codex API returned status %d: %s", resp.StatusCode, message),
			}
		}
		return nil, nil, fmt.Errorf("Codex API returned status %d: %s", resp.StatusCode, message)
	}

	ch := make(chan service.StreamChunk, 64)
	go p.readCodexStream(resp, ch, releaseOnce)
	return ch, resp.Header, nil
}

// Models returns the model slugs available to the connected ChatGPT account.
func (p *CodexProvider) Models(ctx context.Context) ([]string, error) {
	modelsURL, err := p.proxyURL("/models", "client_version="+url.QueryEscape(p.ClientVersion))
	if err != nil {
		return nil, err
	}
	send := func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("build Codex models request: %w", err)
		}
		if err := p.authorize(ctx, req); err != nil {
			return nil, err
		}
		return p.httpClient.Do(req)
	}
	resp, err := send()
	if err != nil {
		return nil, fmt.Errorf("Codex models request failed: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp, err = p.recoverUnauthorized(ctx, resp, send)
		if err != nil {
			return nil, fmt.Errorf("Codex models request recovery failed: %w", err)
		}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read Codex models response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Codex models endpoint returned %d: %s", resp.StatusCode, codexErrorMessage(body))
	}
	var result codexModelsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse Codex models response: %w", err)
	}
	models := make([]string, 0, len(result.Models))
	for _, model := range result.Models {
		if model.Slug != "" {
			models = append(models, model.Slug)
		}
	}
	return models, nil
}

func (p *CodexProvider) recoverUnauthorized(ctx context.Context, resp *http.Response, send func() (*http.Response, error)) (*http.Response, error) {
	source, ok := p.tokenSource.(*CodexTokenSource)
	if !ok {
		return resp, nil
	}
	resp.Body.Close()
	reloaded, err := source.Reload(ctx)
	if err != nil {
		return nil, err
	}
	if reloaded {
		resp, err = send()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusUnauthorized {
			return resp, nil
		}
		resp.Body.Close()
	}
	source.Invalidate()
	return send()
}

func codexErrorMessage(body []byte) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		if envelope.Error.Message != "" {
			return envelope.Error.Message
		}
		if envelope.Message != "" {
			return envelope.Message
		}
	}
	return truncate(strings.TrimSpace(string(body)), 500)
}

type codexSSEEvent struct {
	Type     string          `json:"type"`
	Delta    string          `json:"delta"`
	Text     string          `json:"text"`
	Item     json.RawMessage `json:"item"`
	Response json.RawMessage `json:"response"`
}

type codexResponseItem struct {
	Type             string          `json:"type"`
	CallID           string          `json:"call_id"`
	Name             string          `json:"name"`
	Arguments        string          `json:"arguments"`
	EncryptedContent string          `json:"encrypted_content"`
	Summary          json.RawMessage `json:"summary"`
	Content          []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

type codexCompletedResponse struct {
	Usage *struct {
		InputTokens       int `json:"input_tokens"`
		OutputTokens      int `json:"output_tokens"`
		TotalTokens       int `json:"total_tokens"`
		InputTokenDetails *struct {
			CachedTokens     int `json:"cached_tokens"`
			CacheWriteTokens int `json:"cache_write_tokens"`
		} `json:"input_tokens_details"`
		OutputTokenDetails *struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (p *CodexProvider) readCodexStream(resp *http.Response, ch chan<- service.StreamChunk, release func()) {
	defer close(ch)
	defer resp.Body.Close()
	defer release()

	var completed bool
	var emittedText bool
	var emittedReasoning bool
	var sawToolCall bool
	var pendingReasoningState string
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event codexSSEEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			ch <- service.StreamChunk{Error: fmt.Errorf("parse Codex SSE event: %w", err)}
			return
		}

		switch event.Type {
		case "response.output_text.delta":
			emittedText = emittedText || event.Delta != ""
			if event.Delta != "" {
				ch <- service.StreamChunk{Content: event.Delta}
			}
		case "response.output_text.done":
			if !emittedText && event.Text != "" {
				emittedText = true
				ch <- service.StreamChunk{Content: event.Text}
			}
		case "response.reasoning_summary_text.delta":
			emittedReasoning = emittedReasoning || event.Delta != ""
			if event.Delta != "" {
				ch <- service.StreamChunk{ReasoningContent: event.Delta}
			}
		case "response.reasoning_summary_text.done":
			if !emittedReasoning && event.Text != "" {
				emittedReasoning = true
				ch <- service.StreamChunk{ReasoningContent: event.Text}
			}
		case "response.output_item.done":
			var item codexResponseItem
			if err := json.Unmarshal(event.Item, &item); err != nil {
				ch <- service.StreamChunk{Error: fmt.Errorf("parse Codex output item: %w", err)}
				return
			}
			switch item.Type {
			case "reasoning":
				if item.EncryptedContent != "" {
					pendingReasoningState = string(event.Item)
				}
			case "function_call":
				arguments := map[string]any{}
				if item.Arguments != "" {
					if err := json.Unmarshal([]byte(item.Arguments), &arguments); err != nil {
						ch <- service.StreamChunk{Error: fmt.Errorf("parse Codex function arguments: %w", err)}
						return
					}
				}
				sawToolCall = true
				ch <- service.StreamChunk{ToolCalls: []service.ToolCall{{
					ID:               item.CallID,
					Name:             item.Name,
					Arguments:        arguments,
					ThoughtSignature: pendingReasoningState,
				}}}
				pendingReasoningState = ""
			case "message":
				if !emittedText {
					var text strings.Builder
					for _, content := range item.Content {
						if content.Type == "output_text" {
							text.WriteString(content.Text)
						}
					}
					if text.Len() > 0 {
						emittedText = true
						ch <- service.StreamChunk{Content: text.String()}
					}
				}
			}
		case "response.completed":
			var response codexCompletedResponse
			if err := json.Unmarshal(event.Response, &response); err != nil {
				ch <- service.StreamChunk{Error: fmt.Errorf("parse Codex completed response: %w", err)}
				return
			}
			finishReason := "stop"
			if sawToolCall {
				finishReason = "tool_calls"
			}
			chunk := service.StreamChunk{FinishReason: finishReason}
			if response.Usage != nil {
				usage := codexServiceUsage(response)
				chunk.Usage = &usage
			}
			ch <- chunk
			completed = true
			return
		case "response.failed", "response.incomplete":
			var response codexCompletedResponse
			_ = json.Unmarshal(event.Response, &response)
			message := event.Type
			if response.Error != nil && response.Error.Message != "" {
				message = response.Error.Message
			}
			if response.Error != nil && response.Error.Code == "rate_limit_exceeded" {
				ch <- service.StreamChunk{Error: &service.RateLimitError{
					StatusCode: http.StatusTooManyRequests,
					Provider:   "openai-codex",
					Message:    message,
					Underlying: fmt.Errorf("Codex %s: %s", event.Type, message),
				}}
				return
			}
			ch <- service.StreamChunk{Error: fmt.Errorf("Codex %s: %s", event.Type, message)}
			return
		}
	}
	if err := scanner.Err(); err != nil {
		ch <- service.StreamChunk{Error: fmt.Errorf("read Codex SSE stream: %w", err)}
		return
	}
	if !completed {
		ch <- service.StreamChunk{Error: fmt.Errorf("Codex stream closed before response.completed")}
	}
}

func codexServiceUsage(response codexCompletedResponse) service.Usage {
	u := response.Usage
	if u == nil {
		return service.Usage{}
	}
	var cached, cacheWrite, reasoning int
	if u.InputTokenDetails != nil {
		cached = u.InputTokenDetails.CachedTokens
		cacheWrite = u.InputTokenDetails.CacheWriteTokens
	}
	if u.OutputTokenDetails != nil {
		reasoning = u.OutputTokenDetails.ReasoningTokens
	}
	prompt := u.InputTokens - cached - cacheWrite
	if prompt < 0 {
		prompt = 0
	}
	return service.Usage{
		PromptTokens:     prompt,
		CompletionTokens: u.OutputTokens,
		CacheReadTokens:  cached,
		CacheWriteTokens: cacheWrite,
		TotalTokens:      u.TotalTokens,
		ReasoningTokens:  reasoning,
	}
}

// Proxy forwards native requests through the Codex authentication and limiter.
func (p *CodexProvider) Proxy(w http.ResponseWriter, r *http.Request, path string) error {
	target, err := p.proxyURL(path, r.URL.RawQuery)
	if err != nil {
		return err
	}
	release, err := p.limiter.Acquire(r.Context(), 0)
	if err != nil {
		return err
	}
	defer release()
	if err := p.authorize(r.Context(), r); err != nil {
		return err
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL = target
			req.Host = target.Host
		},
		Transport: p.httpClient.Transport,
		ErrorHandler: func(writer http.ResponseWriter, _ *http.Request, proxyErr error) {
			slog.Error("Codex proxy error", "error", proxyErr)
			http.Error(writer, fmt.Sprintf("proxy error: %v", proxyErr), http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
	return nil
}

func (p *CodexProvider) proxyURL(path, rawQuery string) (*url.URL, error) {
	base, err := url.Parse(p.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse Codex base URL: %w", err)
	}
	if path != "" {
		rootPath := strings.TrimSuffix(base.Path, "/responses")
		base.Path = strings.TrimSuffix(rootPath, "/") + "/" + strings.TrimPrefix(path, "/")
	}
	if rawQuery != "" {
		if base.RawQuery == "" {
			base.RawQuery = rawQuery
		} else {
			base.RawQuery += "&" + rawQuery
		}
	}
	return base, nil
}

func buildCodexRequest(model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) map[string]any {
	requestTools := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		requestTools = append(requestTools, map[string]any{
			"type":        "function",
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  service.SanitizeSchema(tool.InputSchema),
			"strict":      false,
		})
	}

	parallel := true
	toolChoice := any("auto")
	reasoning := map[string]any{"summary": "auto"}
	request := map[string]any{
		"model":               model,
		"instructions":        "",
		"input":               codexInput(messages),
		"tools":               requestTools,
		"tool_choice":         toolChoice,
		"parallel_tool_calls": parallel,
		"reasoning":           reasoning,
		"stream":              true,
		"store":               false,
		"include":             []string{"reasoning.encrypted_content"},
	}
	if opts != nil {
		if opts.ToolChoice != nil {
			request["tool_choice"] = codexToolChoice(opts.ToolChoice)
		}
		if opts.ParallelToolCalls != nil {
			request["parallel_tool_calls"] = *opts.ParallelToolCalls
		}
		if opts.ReasoningEffort != "" {
			reasoning["effort"] = opts.ReasoningEffort
		}
		if len(opts.ResponseFormat) > 0 {
			request["text"] = map[string]any{"format": codexTextFormat(opts.ResponseFormat)}
		}
		if opts.ServiceTier != "" {
			request["service_tier"] = opts.ServiceTier
		}
		// ChatGPT's Codex endpoint does not accept the Responses API's output
		// token, sampling, or metadata controls. ExtraBody remains available for
		// explicitly opting into fields added to the endpoint in the future.
		for key, value := range opts.ExtraBody {
			request[key] = value
		}
	}
	// The ChatGPT Codex endpoint only supports streamed, non-stored responses.
	request["stream"] = true
	request["store"] = false
	request["include"] = []string{"reasoning.encrypted_content"}
	return request
}

func codexToolChoice(choice any) any {
	object, ok := choice.(map[string]any)
	if !ok {
		return choice
	}
	if object["type"] != "function" {
		return choice
	}
	function, _ := object["function"].(map[string]any)
	name, _ := function["name"].(string)
	if name == "" {
		return choice
	}
	return map[string]any{"type": "function", "name": name}
}

func codexTextFormat(format map[string]any) map[string]any {
	if format["type"] != "json_schema" {
		return format
	}
	jsonSchema, _ := format["json_schema"].(map[string]any)
	if jsonSchema == nil {
		return format
	}
	result := map[string]any{"type": "json_schema"}
	for _, key := range []string{"name", "schema", "strict", "description"} {
		if value, ok := jsonSchema[key]; ok {
			result[key] = value
		}
	}
	return result
}

func codexInput(messages []service.Message) []any {
	var input []any
	for _, message := range messages {
		switch content := message.Content.(type) {
		case []service.ContentBlock:
			input = append(input, codexContentBlockInput(message.Role, content)...)
		case map[string]any:
			input = append(input, codexMapMessageInput(content)...)
		default:
			if text := codexContentText(content); text != "" {
				input = append(input, codexMessageItem(message.Role, text))
			}
		}
	}
	return input
}

func codexContentBlockInput(role string, blocks []service.ContentBlock) []any {
	var input []any
	var content []any
	for _, block := range blocks {
		switch block.Type {
		case "text":
			content = append(content, codexTextItem(role, block.Text))
		case "image", "image_url":
			if role == "assistant" || block.Source == nil {
				continue
			}
			imageURL := block.Source.URL
			if imageURL == "" && block.Source.Data != "" {
				imageURL = "data:" + block.Source.MediaType + ";base64," + block.Source.Data
			}
			if imageURL != "" {
				content = append(content, map[string]any{"type": "input_image", "image_url": imageURL})
			}
		case "tool_use":
			arguments, _ := json.Marshal(block.Input)
			input = appendMessageContent(input, role, content)
			content = nil
			input = appendCodexReasoningState(input, block.ThoughtSignature)
			input = append(input, map[string]any{
				"type":      "function_call",
				"call_id":   block.ID,
				"name":      block.Name,
				"arguments": string(arguments),
			})
		case "tool_result":
			input = appendMessageContent(input, role, content)
			content = nil
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": block.ToolUseID,
				"output":  block.Content,
			})
		}
	}
	return appendMessageContent(input, role, content)
}

func codexMapMessageInput(message map[string]any) []any {
	if itemType, _ := message["type"].(string); itemType != "" && itemType != "message" {
		return []any{message}
	}
	role, _ := message["role"].(string)
	if role == "tool" {
		callID, _ := message["tool_call_id"].(string)
		return []any{map[string]any{
			"type":    "function_call_output",
			"call_id": callID,
			"output":  codexContentText(message["content"]),
		}}
	}
	var input []any
	if text := codexContentText(message["content"]); text != "" {
		input = append(input, codexMessageItem(role, text))
	}
	for _, call := range codexObjectSlice(message["tool_calls"]) {
		function, _ := call["function"].(map[string]any)
		thoughtSignature, _ := call["thought_signature"].(string)
		input = appendCodexReasoningState(input, thoughtSignature)
		input = append(input, map[string]any{
			"type":      "function_call",
			"call_id":   call["id"],
			"name":      function["name"],
			"arguments": function["arguments"],
		})
	}
	return input
}

func appendCodexReasoningState(input []any, signature string) []any {
	if signature == "" {
		return input
	}
	var item map[string]any
	if json.Unmarshal([]byte(signature), &item) != nil || item["type"] != "reasoning" || item["encrypted_content"] == nil {
		return input
	}
	return append(input, item)
}

func codexMessageItem(role, text string) map[string]any {
	if role == "system" {
		role = "developer"
	}
	return map[string]any{
		"type":    "message",
		"role":    role,
		"content": []any{codexTextItem(role, text)},
	}
}

func codexTextItem(role, text string) map[string]any {
	itemType := "input_text"
	if role == "assistant" {
		itemType = "output_text"
	}
	return map[string]any{"type": itemType, "text": text}
}

func appendMessageContent(input []any, role string, content []any) []any {
	if len(content) == 0 {
		return input
	}
	if role == "system" {
		role = "developer"
	}
	return append(input, map[string]any{"type": "message", "role": role, "content": content})
}

func codexContentText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var text strings.Builder
		for _, item := range value {
			if part, ok := item.(map[string]any); ok {
				if value, _ := part["text"].(string); value != "" {
					text.WriteString(value)
				}
			}
		}
		return text.String()
	case []map[string]any:
		var text strings.Builder
		for _, part := range value {
			if value, _ := part["text"].(string); value != "" {
				text.WriteString(value)
			}
		}
		return text.String()
	default:
		if value == nil {
			return ""
		}
		data, _ := json.Marshal(value)
		return string(data)
	}
}

func codexObjectSlice(value any) []map[string]any {
	switch items := value.(type) {
	case []map[string]any:
		return items
	case []any:
		result := make([]map[string]any, 0, len(items))
		for _, item := range items {
			if object, ok := item.(map[string]any); ok {
				result = append(result, object)
			}
		}
		return result
	default:
		return nil
	}
}

var (
	_ service.LLMProvider       = (*CodexProvider)(nil)
	_ service.LLMStreamProvider = (*CodexProvider)(nil)
)
