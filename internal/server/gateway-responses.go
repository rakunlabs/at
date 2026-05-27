package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

// ─── OpenAI Responses API (minimal compatibility layer) ───
//
// This is a best-effort, non-streaming implementation of the OpenAI
// Responses API (POST /v1/responses). The Responses API is a superset
// of chat.completions with new ergonomics (typed output items,
// reasoning summaries, conversation state via `previous_response_id`).
//
// What we support today:
//   - String or array-of-input-items `input` field
//   - `instructions` (treated as a system message prefix)
//   - `tools` (function tools only) and `tool_choice`
//   - `temperature`, `top_p`, `max_output_tokens`, `parallel_tool_calls`,
//     `seed`, `metadata`, `user`, `reasoning.effort`, `text.format`
//   - Non-streaming response with `output[]` items: `message` (assistant
//     text), `reasoning` (summary), `function_call` items, and a
//     populated `usage` object
//
// What we do NOT support (yet):
//   - Streaming (returns 400)
//   - `previous_response_id` (conversation state — would require
//     server-side storage)
//   - `web_search`, `file_search`, `code_interpreter`, `computer_use`
//     built-in tools (only `type:"function"` tools are honoured)
//   - `store: true` semantics (responses are not persisted server-side)
//   - Image input rendering via `input_image` items (text only here)
//
// Callers using the new OpenAI Responses API can therefore point their
// SDK at this gateway and receive Responses-shaped JSON back; advanced
// features above silently degrade.

// responsesRequest is the OpenAI Responses API request body.
type responsesRequest struct {
	Model               string          `json:"model"`
	Input               json.RawMessage `json:"input"` // string OR []InputItem
	Instructions        string          `json:"instructions,omitempty"`
	Tools               []OpenAITool    `json:"tools,omitempty"`
	ToolChoice          json.RawMessage `json:"tool_choice,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	TopP                *float64        `json:"top_p,omitempty"`
	MaxOutputTokens     *int            `json:"max_output_tokens,omitempty"`
	ParallelToolCalls   *bool           `json:"parallel_tool_calls,omitempty"`
	Seed                *int            `json:"seed,omitempty"`
	Metadata            map[string]any  `json:"metadata,omitempty"`
	User                string          `json:"user,omitempty"`
	Stream              bool            `json:"stream,omitempty"`
	Store               *bool           `json:"store,omitempty"`
	PreviousResponseID  string          `json:"previous_response_id,omitempty"`
	Reasoning           *responsesReasoning `json:"reasoning,omitempty"`
	Text                *responsesText      `json:"text,omitempty"`

	// AT extensions
	AtFallbacks  []string       `json:"at_fallbacks,omitempty"`
	ExtraBody    map[string]any `json:"extra_body,omitempty"`
	MockResponse string         `json:"mock_response,omitempty"`
	TimeoutMs    int            `json:"timeout_ms,omitempty"`
}

type responsesReasoning struct {
	Effort string `json:"effort,omitempty"` // "low" | "medium" | "high"
}

type responsesText struct {
	Format *responsesTextFormat `json:"format,omitempty"`
}

type responsesTextFormat struct {
	Type       string         `json:"type"` // "text" | "json_object" | "json_schema"
	Name       string         `json:"name,omitempty"`
	Schema     map[string]any `json:"schema,omitempty"`
	Strict     *bool          `json:"strict,omitempty"`
}

// responsesResponse is the OpenAI Responses API response body.
type responsesResponse struct {
	ID                string             `json:"id"`
	Object            string             `json:"object"` // "response"
	CreatedAt         int64              `json:"created_at"`
	Status            string             `json:"status"` // "completed" | "incomplete" | "failed"
	Model             string             `json:"model"`
	Output            []responsesOutItem `json:"output"`
	Usage             responsesUsage     `json:"usage"`
	Metadata          map[string]any     `json:"metadata,omitempty"`
	ParallelToolCalls *bool              `json:"parallel_tool_calls,omitempty"`
}

// responsesOutItem is a single element of the response.output[] array.
// We emit three kinds: "message" (assistant content), "reasoning"
// (thinking summary), and "function_call" (tool invocation).
type responsesOutItem struct {
	ID      string                `json:"id"`
	Type    string                `json:"type"`
	Status  string                `json:"status,omitempty"`
	Role    string                `json:"role,omitempty"`
	Content []responsesOutContent `json:"content,omitempty"`

	// function_call fields
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`

	// reasoning fields
	Summary []responsesReasoningSummary `json:"summary,omitempty"`
}

type responsesOutContent struct {
	Type        string                  `json:"type"` // "output_text" | "refusal"
	Text        string                  `json:"text,omitempty"`
	Annotations []responsesAnnotation   `json:"annotations,omitempty"`
}

type responsesAnnotation struct{}

type responsesReasoningSummary struct {
	Type string `json:"type"` // "summary_text"
	Text string `json:"text"`
}

type responsesUsage struct {
	InputTokens         int                          `json:"input_tokens"`
	InputTokensDetails  responsesInputTokensDetails  `json:"input_tokens_details"`
	OutputTokens        int                          `json:"output_tokens"`
	OutputTokensDetails responsesOutputTokensDetails `json:"output_tokens_details"`
	TotalTokens         int                          `json:"total_tokens"`
}

type responsesInputTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

type responsesOutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

// Responses handles POST /gateway/v1/responses.
func (s *Server) Responses(w http.ResponseWriter, r *http.Request) {
	auth, authErr := s.authenticateRequest(r)
	if authErr != "" {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": authErr,
				"type":    "invalid_request_error",
				"code":    "invalid_api_key",
			},
		}, http.StatusUnauthorized)
		return
	}

	var req responsesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("invalid request body: %v", err),
				"type":    "invalid_request_error",
			},
		}, http.StatusBadRequest)
		return
	}

	if req.PreviousResponseID != "" {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": "previous_response_id is not supported by this gateway; include the full conversation in the input",
				"type":    "invalid_request_error",
				"param":   "previous_response_id",
			},
		}, http.StatusBadRequest)
		return
	}

	// Mock-response short-circuit.
	if req.MockResponse != "" {
		mockResp := buildMockResponsesResponse(req.Model, req.Metadata, req.ParallelToolCalls, req.MockResponse)
		w.Header().Set("x-at-mock-response", "true")
		httpResponseJSON(w, mockResp, http.StatusOK)
		return
	}

	providerKey, actualModel, err := parseModelID(req.Model)
	if err != nil {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": err.Error(),
				"type":    "invalid_request_error",
				"param":   "model",
				"code":    "model_not_found",
			},
		}, http.StatusBadRequest)
		return
	}

	if !auth.isModelAllowed(providerKey, req.Model) {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("token does not have access to model %q", req.Model),
				"type":    "invalid_request_error",
				"param":   "model",
				"code":    "model_not_found",
			},
		}, http.StatusForbidden)
		return
	}

	if limitMessage, resetErr := s.checkTokenLimits(r.Context(), auth); resetErr != nil {
		slog.Error("token limit check failed", "error", resetErr)
	} else if limitMessage != "" {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": limitMessage,
				"type":    "tokens",
				"code":    "rate_limit_exceeded",
			},
		}, http.StatusTooManyRequests)
		return
	}

	info, ok := s.getProviderInfo(providerKey)
	if !ok {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("provider %q not found", providerKey),
				"type":    "invalid_request_error",
				"param":   "model",
				"code":    "model_not_found",
			},
		}, http.StatusNotFound)
		return
	}

	// Translate Responses input → chat.completions messages.
	chatMsgs, err := responsesInputToOpenAIMessages(req.Input, req.Instructions)
	if err != nil {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": err.Error(),
				"type":    "invalid_request_error",
				"param":   "input",
			},
		}, http.StatusBadRequest)
		return
	}

	tools := translateOpenAITools(req.Tools)

	var messages []service.Message
	switch info.providerType {
	case "anthropic", "minimax":
		var systemPrompt string
		systemPrompt, messages = translateOpenAIToAnthropic(chatMsgs)
		if systemPrompt != "" {
			messages = append([]service.Message{{Role: "system", Content: systemPrompt}}, messages...)
		}
	default:
		messages = translateOpenAIMessages(chatMsgs, s.lookupThoughtSignature)
	}

	baseOpts := responsesRequestToChatOptions(&req)

	// Per-call timeout.
	callCtx, cancel := withRequestTimeout(r.Context(), req.TimeoutMs)
	defer cancel()

	if req.Stream {
		// Streaming: no fallback (same constraint as chat streaming).
		sMessages, _ := s.buildProviderMessages(info.providerType, chatMsgs, nil)
		s.handleStreamingResponses(w, r.WithContext(callCtx), auth, info, providerKey, actualModel, req.Model, req.Metadata, req.ParallelToolCalls, sMessages, tools, cloneChatOptions(baseOpts))
		return
	}

	// Build fallback chain. Primary is implied by req.Model (validated above
	// — info is the primary). For fallbacks we go through the chain
	// resolver and skip invalid entries.
	chain := []chatCallTarget{{
		fullModel:   req.Model,
		providerKey: providerKey,
		actualModel: actualModel,
		info:        info,
	}}
	for _, m := range req.AtFallbacks {
		pKey, actual, fInfo, ferr := s.resolveModel(auth, m)
		if ferr != nil {
			slog.Warn("responses fallback skipping invalid entry", "model", m, "error", ferr.Error())
			continue
		}
		chain = append(chain, chatCallTarget{
			fullModel: m, providerKey: pKey, actualModel: actual, info: fInfo,
		})
	}

	var (
		lastErr      error
		used         chatCallTarget
		resp         *service.LLMResponse
		totalLatency int64
	)
	for i, target := range chain {
		// Re-translate messages for each target because the provider type
		// can change (e.g. fallback from openai → anthropic).
		tMessages, _ := s.buildProviderMessages(target.info.providerType, chatMsgs, nil)
		opts := cloneChatOptions(baseOpts)

		callStart := time.Now()
		r2, err := callWithGatewayRetry(callCtx, target.providerKey, target.actualModel,
			target.info.RetryAfterCap(),
			func(ctx context.Context) (*service.LLMResponse, error) {
				return target.info.provider.Chat(ctx, target.actualModel, tMessages, tools, opts)
			})
		totalLatency += time.Since(callStart).Milliseconds()
		if err == nil {
			resp = r2
			used = target
			_ = i
			break
		}
		lastErr = err
		slog.Warn("responses provider call failed",
			"attempt", i, "provider", target.providerKey, "model", target.actualModel, "error", err)
		s.recordUsageAsync(r.Context(), auth, target.fullModel, service.Usage{}, totalLatency, "error", classifyHTTPError(err), err.Error())
		if !shouldFallback(err) {
			break
		}
	}

	if resp == nil {
		status, body := classifyGatewayError(lastErr)
		addGatewayRateLimitHeaders(w, lastErr)
		httpResponseJSON(w, body, status)
		return
	}

	if used.fullModel != req.Model {
		w.Header().Set("x-at-model-used", used.fullModel)
	}
	s.cacheThoughtSignatures(resp.ToolCalls)
	out := buildResponsesResponse(used.fullModel, req.Metadata, req.ParallelToolCalls, resp)
	s.recordUsageAsync(r.Context(), auth, used.fullModel, resp.Usage, totalLatency, "ok", "", "")
	httpResponseJSON(w, out, http.StatusOK)
}

// handleStreamingResponses emits OpenAI Responses-API SSE events derived
// from a single Chat or ChatStream upstream call.
//
// Event types emitted (subset of OpenAI's Responses streaming protocol):
//   - response.created
//   - response.output_item.added (message item)
//   - response.output_text.delta (per content chunk)
//   - response.output_text.done
//   - response.output_item.added (function_call item) — for tool calls
//   - response.function_call_arguments.delta
//   - response.output_item.done
//   - response.completed
//
// We do NOT support reasoning streaming (reasoning items are emitted as
// completed items at the end if reasoning content was returned).
func (s *Server) handleStreamingResponses(
	w http.ResponseWriter,
	r *http.Request,
	auth *authResult,
	info ProviderInfo,
	providerKey, actualModel, fullModel string,
	metadata map[string]any,
	parallelToolCalls *bool,
	messages []service.Message,
	tools []service.Tool,
	opts *service.ChatOptions,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": "streaming not supported by this server",
				"type":    "server_error",
			},
		}, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	respID := "resp_" + generateChatID()
	createdAt := time.Now().Unix()

	emit := func(eventType string, data any) {
		buf, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, buf)
		flusher.Flush()
	}

	// 1. response.created
	emit("response.created", map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id":                  respID,
			"object":              "response",
			"created_at":          createdAt,
			"status":              "in_progress",
			"model":               fullModel,
			"output":              []any{},
			"metadata":            metadata,
			"parallel_tool_calls": parallelToolCalls,
		},
	})

	// Try true streaming first.
	callStart := time.Now()
	var (
		usage          *service.Usage
		messageItemID  = "msg_" + generateChatID()
		messageStarted bool
		fullText       strings.Builder
		reasoningText  strings.Builder
		toolCallItems  = make(map[string]*responsesOutItem) // by tool_call ID
		toolCallOrder  []string
	)

	emitMessageStart := func() {
		if messageStarted {
			return
		}
		emit("response.output_item.added", map[string]any{
			"type":         "response.output_item.added",
			"output_index": 0,
			"item": map[string]any{
				"id":      messageItemID,
				"type":    "message",
				"status":  "in_progress",
				"role":    "assistant",
				"content": []any{},
			},
		})
		messageStarted = true
	}

	emitDelta := func(text string) {
		if text == "" {
			return
		}
		emitMessageStart()
		fullText.WriteString(text)
		emit("response.output_text.delta", map[string]any{
			"type":          "response.output_text.delta",
			"item_id":       messageItemID,
			"output_index":  0,
			"content_index": 0,
			"delta":         text,
		})
	}

	if sp, ok := info.provider.(service.LLMStreamProvider); ok {
		chunks, _, err := sp.ChatStream(r.Context(), actualModel, messages, tools, opts)
		if err != nil {
			emit("response.failed", map[string]any{
				"type":  "response.failed",
				"error": map[string]any{"message": err.Error(), "type": "server_error"},
			})
			s.recordUsageAsync(r.Context(), auth, fullModel, service.Usage{}, time.Since(callStart).Milliseconds(), "error", classifyHTTPError(err), err.Error())
			return
		}
		for chunk := range chunks {
			if chunk.Error != nil {
				emit("response.failed", map[string]any{
					"type":  "response.failed",
					"error": map[string]any{"message": chunk.Error.Error(), "type": "server_error"},
				})
				return
			}
			if chunk.Usage != nil {
				u := *chunk.Usage
				usage = &u
			}
			emitDelta(chunk.Content)
			if chunk.ReasoningContent != "" {
				reasoningText.WriteString(chunk.ReasoningContent)
			}
			for _, tc := range chunk.ToolCalls {
				item, exists := toolCallItems[tc.ID]
				if !exists {
					item = &responsesOutItem{
						ID:     "fc_" + generateChatID(),
						Type:   "function_call",
						Status: "in_progress",
						CallID: tc.ID,
						Name:   tc.Name,
					}
					toolCallItems[tc.ID] = item
					toolCallOrder = append(toolCallOrder, tc.ID)
					emit("response.output_item.added", map[string]any{
						"type":         "response.output_item.added",
						"output_index": len(toolCallOrder),
						"item": map[string]any{
							"id":      item.ID,
							"type":    "function_call",
							"status":  "in_progress",
							"call_id": tc.ID,
							"name":    tc.Name,
						},
					})
				}
				argsJSON, _ := json.Marshal(tc.Arguments)
				item.Arguments = string(argsJSON)
				emit("response.function_call_arguments.delta", map[string]any{
					"type":         "response.function_call_arguments.delta",
					"item_id":      item.ID,
					"output_index": indexOf(toolCallOrder, tc.ID) + 1,
					"delta":        string(argsJSON),
				})
			}
		}
	} else {
		// Provider doesn't support streaming — fall back to one Chat call
		// and emit the whole result as a single delta.
		resp, err := info.provider.Chat(r.Context(), actualModel, messages, tools, opts)
		if err != nil {
			emit("response.failed", map[string]any{
				"type":  "response.failed",
				"error": map[string]any{"message": err.Error(), "type": "server_error"},
			})
			s.recordUsageAsync(r.Context(), auth, fullModel, service.Usage{}, time.Since(callStart).Milliseconds(), "error", classifyHTTPError(err), err.Error())
			return
		}
		if resp.ReasoningContent != "" {
			reasoningText.WriteString(resp.ReasoningContent)
		}
		emitDelta(resp.Content)
		for _, tc := range resp.ToolCalls {
			item := &responsesOutItem{
				ID:     "fc_" + generateChatID(),
				Type:   "function_call",
				Status: "completed",
				CallID: tc.ID,
				Name:   tc.Name,
			}
			argsJSON, _ := json.Marshal(tc.Arguments)
			item.Arguments = string(argsJSON)
			toolCallItems[tc.ID] = item
			toolCallOrder = append(toolCallOrder, tc.ID)
			emit("response.output_item.added", map[string]any{
				"type":         "response.output_item.added",
				"output_index": len(toolCallOrder),
				"item": map[string]any{
					"id":        item.ID,
					"type":      "function_call",
					"status":    "in_progress",
					"call_id":   tc.ID,
					"name":      tc.Name,
					"arguments": "",
				},
			})
			emit("response.function_call_arguments.delta", map[string]any{
				"type":         "response.function_call_arguments.delta",
				"item_id":      item.ID,
				"output_index": len(toolCallOrder),
				"delta":        string(argsJSON),
			})
		}
		if usage == nil {
			u := resp.Usage
			usage = &u
		}
		s.cacheThoughtSignatures(resp.ToolCalls)
	}

	// Close out the message item with done event (if started).
	if messageStarted {
		emit("response.output_text.done", map[string]any{
			"type":          "response.output_text.done",
			"item_id":       messageItemID,
			"output_index":  0,
			"content_index": 0,
			"text":          fullText.String(),
		})
		emit("response.output_item.done", map[string]any{
			"type":         "response.output_item.done",
			"output_index": 0,
			"item": map[string]any{
				"id":     messageItemID,
				"type":   "message",
				"status": "completed",
				"role":   "assistant",
				"content": []any{map[string]any{
					"type": "output_text",
					"text": fullText.String(),
				}},
			},
		})
	}

	// Close out tool-call items.
	for i, tcID := range toolCallOrder {
		item := toolCallItems[tcID]
		emit("response.output_item.done", map[string]any{
			"type":         "response.output_item.done",
			"output_index": i + 1,
			"item": map[string]any{
				"id":        item.ID,
				"type":      "function_call",
				"status":    "completed",
				"call_id":   item.CallID,
				"name":      item.Name,
				"arguments": item.Arguments,
			},
		})
	}

	// Final response.completed
	output := make([]responsesOutItem, 0, 1+len(toolCallOrder))
	if reasoningText.Len() > 0 {
		output = append(output, responsesOutItem{
			ID:   "rs_" + generateChatID(),
			Type: "reasoning",
			Summary: []responsesReasoningSummary{{
				Type: "summary_text",
				Text: reasoningText.String(),
			}},
		})
	}
	if messageStarted {
		text := fullText.String()
		output = append(output, responsesOutItem{
			ID:     messageItemID,
			Type:   "message",
			Status: "completed",
			Role:   "assistant",
			Content: []responsesOutContent{{
				Type: "output_text",
				Text: text,
			}},
		})
	}
	for _, tcID := range toolCallOrder {
		output = append(output, *toolCallItems[tcID])
	}

	finalResp := map[string]any{
		"id":                  respID,
		"object":              "response",
		"created_at":          createdAt,
		"status":              "completed",
		"model":               fullModel,
		"output":              output,
		"metadata":            metadata,
		"parallel_tool_calls": parallelToolCalls,
	}
	if usage != nil {
		finalResp["usage"] = responsesUsage{
			InputTokens:  usage.TotalInputTokens(),
			OutputTokens: usage.CompletionTokens,
			TotalTokens:  usage.TotalTokenCount(),
			InputTokensDetails: responsesInputTokensDetails{
				CachedTokens: usage.CacheReadTokens,
			},
			OutputTokensDetails: responsesOutputTokensDetails{
				ReasoningTokens: usage.ReasoningTokens,
			},
		}
		s.recordUsageAsync(r.Context(), auth, fullModel, *usage, time.Since(callStart).Milliseconds(), "ok", "", "")
	}
	emit("response.completed", map[string]any{
		"type":     "response.completed",
		"response": finalResp,
	})
}

// indexOf returns the position of s in slice, or -1 if absent.
func indexOf(slice []string, s string) int {
	for i, v := range slice {
		if v == s {
			return i
		}
	}
	return -1
}

// buildMockResponsesResponse mirrors buildMockChatResponse for the Responses API shape.
func buildMockResponsesResponse(model string, metadata map[string]any, parallelToolCalls *bool, content string) *responsesResponse {
	return &responsesResponse{
		ID:                "resp_mock_" + generateChatID(),
		Object:            "response",
		CreatedAt:         time.Now().Unix(),
		Status:            "completed",
		Model:             model,
		Metadata:          metadata,
		ParallelToolCalls: parallelToolCalls,
		Output: []responsesOutItem{{
			ID:     "msg_mock_" + generateChatID(),
			Type:   "message",
			Status: "completed",
			Role:   "assistant",
			Content: []responsesOutContent{{
				Type: "output_text",
				Text: content,
			}},
		}},
		Usage: responsesUsage{
			OutputTokens: len(content) / 4,
			TotalTokens:  len(content) / 4,
		},
	}
}

// responsesInputToOpenAIMessages translates the Responses API input + instructions
// into the chat.completions messages array our existing translators understand.
//
// input may be:
//   - a JSON string (single user message)
//   - an array of items, where each item is either:
//       - {type:"message", role:"user"|"assistant"|"system", content: string | content_parts}
//       - {type:"function_call", call_id, name, arguments}
//       - {type:"function_call_output", call_id, output}
func responsesInputToOpenAIMessages(raw json.RawMessage, instructions string) ([]OpenAIMessage, error) {
	var msgs []OpenAIMessage
	if instructions != "" {
		msgs = append(msgs, OpenAIMessage{
			Role:    "system",
			Content: json.RawMessage(mustJSONString(instructions)),
		})
	}

	if len(raw) == 0 {
		return msgs, nil
	}

	// Single string input.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		msgs = append(msgs, OpenAIMessage{
			Role:    "user",
			Content: json.RawMessage(mustJSONString(s)),
		})
		return msgs, nil
	}

	// Array of items.
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("input must be a string or an array of input items: %v", err)
	}

	for _, item := range items {
		itype, _ := item["type"].(string)
		if itype == "" {
			// Legacy / shorthand: a bare {role, content} object.
			if role, ok := item["role"].(string); ok && role != "" {
				m, err := responsesMessageItemToOpenAI(item)
				if err != nil {
					return nil, err
				}
				msgs = append(msgs, m)
			}
			continue
		}

		switch itype {
		case "message":
			m, err := responsesMessageItemToOpenAI(item)
			if err != nil {
				return nil, err
			}
			msgs = append(msgs, m)
		case "function_call":
			callID, _ := item["call_id"].(string)
			if callID == "" {
				callID, _ = item["id"].(string)
			}
			name, _ := item["name"].(string)
			args, _ := item["arguments"].(string)
			msgs = append(msgs, OpenAIMessage{
				Role:    "assistant",
				Content: json.RawMessage(`""`),
				ToolCalls: []OpenAIToolCall{{
					ID:   callID,
					Type: "function",
					Function: OpenAIFunctionCall{
						Name:      name,
						Arguments: args,
					},
				}},
			})
		case "function_call_output":
			callID, _ := item["call_id"].(string)
			if callID == "" {
				callID, _ = item["id"].(string)
			}
			output, _ := item["output"].(string)
			msgs = append(msgs, OpenAIMessage{
				Role:       "tool",
				ToolCallID: callID,
				Content:    json.RawMessage(mustJSONString(output)),
			})
		case "reasoning":
			// Reasoning items from previous turns aren't replayable to most
			// providers; drop them silently.
			continue
		default:
			return nil, fmt.Errorf("unsupported input item type %q", itype)
		}
	}
	return msgs, nil
}

func responsesMessageItemToOpenAI(item map[string]any) (OpenAIMessage, error) {
	role, _ := item["role"].(string)
	if role == "" {
		return OpenAIMessage{}, fmt.Errorf("message item missing role")
	}
	contentRaw, hasContent := item["content"]
	if !hasContent {
		return OpenAIMessage{Role: role, Content: json.RawMessage(`""`)}, nil
	}
	switch c := contentRaw.(type) {
	case string:
		return OpenAIMessage{
			Role:    role,
			Content: json.RawMessage(mustJSONString(c)),
		}, nil
	case []any:
		// Translate Responses content parts to chat.completions content parts.
		// input_text → text, output_text → text, input_image → image_url,
		// input_file → file. Anything else passes through.
		parts := make([]map[string]any, 0, len(c))
		for _, raw := range c {
			p, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			t, _ := p["type"].(string)
			switch t {
			case "input_text", "output_text", "text":
				text, _ := p["text"].(string)
				parts = append(parts, map[string]any{"type": "text", "text": text})
			case "input_image":
				url, _ := p["image_url"].(string)
				if url == "" {
					// Some clients nest image_url like {image_url:{url:"..."}}
					if inner, ok := p["image_url"].(map[string]any); ok {
						url, _ = inner["url"].(string)
					}
				}
				if url == "" {
					continue
				}
				parts = append(parts, map[string]any{
					"type":      "image_url",
					"image_url": map[string]any{"url": url},
				})
			case "input_file":
				parts = append(parts, p)
			default:
				parts = append(parts, p)
			}
		}
		buf, err := json.Marshal(parts)
		if err != nil {
			return OpenAIMessage{}, err
		}
		return OpenAIMessage{Role: role, Content: buf}, nil
	default:
		buf, err := json.Marshal(c)
		if err != nil {
			return OpenAIMessage{}, err
		}
		return OpenAIMessage{Role: role, Content: buf}, nil
	}
}

// responsesRequestToChatOptions builds ChatOptions from the Responses API
// request shape (different field names from chat.completions).
func responsesRequestToChatOptions(req *responsesRequest) *service.ChatOptions {
	opts := &service.ChatOptions{}
	hasAny := false

	if req.MaxOutputTokens != nil {
		opts.MaxCompletionTokens = req.MaxOutputTokens
		hasAny = true
	}
	if req.Temperature != nil {
		opts.Temperature = req.Temperature
		hasAny = true
	}
	if req.TopP != nil {
		opts.TopP = req.TopP
		hasAny = true
	}
	if req.Seed != nil {
		opts.Seed = req.Seed
		hasAny = true
	}
	if req.ParallelToolCalls != nil {
		opts.ParallelToolCalls = req.ParallelToolCalls
		hasAny = true
	}
	if tc := parseToolChoice(req.ToolChoice); tc != nil {
		opts.ToolChoice = tc
		hasAny = true
	}
	if req.User != "" {
		opts.User = req.User
		hasAny = true
	}
	if len(req.Metadata) > 0 {
		opts.Metadata = req.Metadata
		hasAny = true
	}
	if req.Reasoning != nil && req.Reasoning.Effort != "" {
		opts.ReasoningEffort = req.Reasoning.Effort
		hasAny = true
	}
	if len(req.ExtraBody) > 0 {
		opts.ExtraBody = req.ExtraBody
		hasAny = true
	}
	if req.Text != nil && req.Text.Format != nil {
		// Translate the Responses "text.format" object back into the
		// chat.completions "response_format" object for our providers.
		rf := map[string]any{"type": req.Text.Format.Type}
		if req.Text.Format.Type == "json_schema" {
			inner := map[string]any{}
			if req.Text.Format.Name != "" {
				inner["name"] = req.Text.Format.Name
			}
			if req.Text.Format.Strict != nil {
				inner["strict"] = *req.Text.Format.Strict
			}
			if len(req.Text.Format.Schema) > 0 {
				inner["schema"] = req.Text.Format.Schema
			}
			rf["json_schema"] = inner
		}
		opts.ResponseFormat = rf
		hasAny = true
	}

	if !hasAny {
		return nil
	}
	return opts
}

// buildResponsesResponse maps an internal LLMResponse to the OpenAI
// Responses API output shape.
func buildResponsesResponse(model string, metadata map[string]any, parallelToolCalls *bool, resp *service.LLMResponse) *responsesResponse {
	out := &responsesResponse{
		ID:                "resp_" + ulid.Make().String(),
		Object:            "response",
		CreatedAt:         time.Now().Unix(),
		Model:             model,
		Metadata:          metadata,
		ParallelToolCalls: parallelToolCalls,
	}

	// Status: incomplete when stopped for "length"; failed not derivable here.
	switch normalizeFinishReason(resp) {
	case "length":
		out.Status = "incomplete"
	case "content_filter":
		out.Status = "incomplete"
	default:
		out.Status = "completed"
	}

	// Reasoning summary (when the provider returned thinking text).
	if strings.TrimSpace(resp.ReasoningContent) != "" {
		out.Output = append(out.Output, responsesOutItem{
			ID:   "rs_" + ulid.Make().String(),
			Type: "reasoning",
			Summary: []responsesReasoningSummary{{
				Type: "summary_text",
				Text: resp.ReasoningContent,
			}},
		})
	}

	// Assistant message (when content is non-empty).
	if strings.TrimSpace(resp.Content) != "" {
		out.Output = append(out.Output, responsesOutItem{
			ID:     "msg_" + ulid.Make().String(),
			Type:   "message",
			Status: "completed",
			Role:   "assistant",
			Content: []responsesOutContent{{
				Type: "output_text",
				Text: resp.Content,
			}},
		})
	}

	// Tool calls become function_call output items.
	for _, tc := range resp.ToolCalls {
		argsJSON, _ := json.Marshal(tc.Arguments)
		out.Output = append(out.Output, responsesOutItem{
			ID:        "fc_" + ulid.Make().String(),
			Type:      "function_call",
			Status:    "completed",
			CallID:    tc.ID,
			Name:      tc.Name,
			Arguments: string(argsJSON),
		})
	}

	out.Usage = responsesUsage{
		InputTokens:  resp.Usage.TotalInputTokens(),
		OutputTokens: resp.Usage.CompletionTokens,
		TotalTokens:  resp.Usage.TotalTokenCount(),
		InputTokensDetails: responsesInputTokensDetails{
			CachedTokens: resp.Usage.CacheReadTokens,
		},
		OutputTokensDetails: responsesOutputTokensDetails{
			ReasoningTokens: resp.Usage.ReasoningTokens,
		},
	}

	return out
}

// mustJSONString JSON-encodes a string and returns the encoded form.
func mustJSONString(s string) string {
	buf, _ := json.Marshal(s)
	return string(buf)
}
