package openaicompatible

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rakunlabs/ok"
)

// ─── Streaming wire types ─────────────────────────────────────────────────

// StreamEvent is a single SSE chunk decoded from a streaming /chat/completions
// response. It mirrors the OpenAI shape one-to-one.
type StreamEvent struct {
	ID                string         `json:"id"`
	Object            string         `json:"object"`
	Created           int64          `json:"created"`
	Model             string         `json:"model"`
	SystemFingerprint string         `json:"system_fingerprint,omitempty"`
	Choices           []StreamChoice `json:"choices"`
	// Usage is populated on the final empty-choices chunk when
	// stream_options.include_usage was requested.
	Usage *Usage `json:"usage,omitempty"`
}

// StreamChoice is one streamed choice in a [StreamEvent].
type StreamChoice struct {
	Index        int         `json:"index"`
	Delta        StreamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason,omitempty"`
	// Logprobs is left as raw bytes — wire format varies between providers.
	Logprobs json.RawMessage `json:"logprobs,omitempty"`
}

// StreamDelta is the incremental content fragment in a [StreamChoice].
type StreamDelta struct {
	Role             string     `json:"role,omitempty"`
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	Refusal          string     `json:"refusal,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
}

// ─── Stream type ──────────────────────────────────────────────────────────

// Stream consumes a server-sent-events response from /chat/completions.
//
// Recv returns each parsed event as it arrives. The stream ends with
// [io.EOF] when the server emits the "[DONE]" sentinel (or closes the
// connection cleanly). Always call [Stream.Close] when done so the
// underlying TCP connection is returned to the pool.
//
// For high-level use, see [AccumulateStream], which assembles all deltas
// into a final [ChatResponse].
type Stream struct {
	resp    *http.Response
	scanner *bufio.Scanner
	closed  bool
	header  http.Header
}

// Header returns the HTTP response headers from the streaming request.
// Useful for inspecting trace IDs or rate-limit headers.
func (s *Stream) Header() http.Header { return s.header }

// Close releases the underlying connection. Idempotent.
func (s *Stream) Close() error {
	if s == nil || s.closed {
		return nil
	}
	s.closed = true
	if s.resp != nil && s.resp.Body != nil {
		// Drain any unread bytes so the connection can be reused.
		ok.DrainBody(s.resp.Body)
	}
	return nil
}

// Recv reads the next event from the stream. Returns [io.EOF] when the
// stream completes normally.
func (s *Stream) Recv() (*StreamEvent, error) {
	if s == nil || s.closed {
		return nil, io.EOF
	}
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			return nil, io.EOF
		}
		var ev StreamEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return nil, fmt.Errorf("openai-compatible: parse SSE chunk: %w (raw: %s)", err, truncate(data, 200))
		}
		// Some servers emit an inline error envelope mid-stream.
		var maybeErr struct {
			Error *errorBody `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &maybeErr); err == nil && maybeErr.Error != nil {
			return nil, &APIError{
				StatusCode: s.resp.StatusCode,
				Status:     http.StatusText(s.resp.StatusCode),
				Message:    maybeErr.Error.Message,
				Type:       maybeErr.Error.Type,
				Code:       maybeErr.Error.codeString(),
				Param:      maybeErr.Error.Param,
				RawBody:    data,
				Header:     s.header.Clone(),
			}
		}
		return &ev, nil
	}
	if err := s.scanner.Err(); err != nil {
		return nil, fmt.Errorf("openai-compatible: stream read: %w", err)
	}
	return nil, io.EOF
}

// ─── ChatStream ───────────────────────────────────────────────────────────

// ChatStream issues a streaming POST /chat/completions request.
//
// It forces req.Stream=true and sets stream_options.include_usage so the
// final event carries token usage. The returned [*Stream] must be closed
// by the caller.
func (c *Client) ChatStream(ctx context.Context, req *ChatRequest) (*Stream, error) {
	if req == nil {
		return nil, errors.New("openai-compatible: nil ChatRequest")
	}
	if req.Model == "" {
		req.Model = c.model
	}
	if req.Model == "" {
		return nil, errors.New("openai-compatible: ChatRequest.Model is required (or use WithModel)")
	}
	req.Stream = true
	if req.StreamOptions == nil {
		req.StreamOptions = &StreamOptions{IncludeUsage: true}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("openai-compatible: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// Streaming responses must not be retried — retrying mid-stream would
	// lose data and double-charge tokens.
	httpReq = httpReq.WithContext(ok.CtxWithRetryPolicy(httpReq.Context(), ok.OptionRetry.WithRetryDisable()))
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai-compatible: stream request: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer ok.DrainBody(resp.Body)
		raw, _ := io.ReadAll(resp.Body)
		return nil, buildAPIError(resp.StatusCode, resp.Header, raw)
	}

	scanner := bufio.NewScanner(resp.Body)
	// Allow up to 10 MiB per SSE line — multimodal events can be large.
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	return &Stream{
		resp:    resp,
		scanner: scanner,
		header:  resp.Header,
	}, nil
}

// ─── Stream accumulation ──────────────────────────────────────────────────

// AccumulateStream consumes the entire stream and assembles a final
// [*ChatResponse], joining content fragments and reassembling tool-call
// argument fragments back into well-formed JSON.
//
// It calls onChunk (if non-nil) for every received event before merging
// it into the accumulator. onChunk should not retain references to the
// event past the call — its slices are reused.
//
// The stream is left open; callers should still call s.Close() afterward.
func AccumulateStream(s *Stream, onChunk func(*StreamEvent)) (*ChatResponse, error) {
	if s == nil {
		return nil, errors.New("openai-compatible: nil Stream")
	}

	resp := &ChatResponse{Object: "chat.completion"}
	// Per-choice accumulators.
	type toolAccum struct {
		id        string
		name      string
		arguments strings.Builder
	}
	type choiceAccum struct {
		content          strings.Builder
		reasoningContent strings.Builder
		refusal          strings.Builder
		role             string
		finishReason     string
		toolOrder        []int
		toolsByIndex     map[int]*toolAccum
	}
	choices := map[int]*choiceAccum{}
	getChoice := func(idx int) *choiceAccum {
		if c, ok := choices[idx]; ok {
			return c
		}
		c := &choiceAccum{toolsByIndex: map[int]*toolAccum{}}
		choices[idx] = c
		return c
	}

	for {
		ev, err := s.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		if onChunk != nil {
			onChunk(ev)
		}
		if resp.ID == "" {
			resp.ID = ev.ID
		}
		if resp.Created == 0 {
			resp.Created = ev.Created
		}
		if resp.Model == "" {
			resp.Model = ev.Model
		}
		if ev.SystemFingerprint != "" {
			resp.SystemFingerprint = ev.SystemFingerprint
		}
		if ev.Usage != nil {
			resp.Usage = ev.Usage
		}
		for _, sc := range ev.Choices {
			ca := getChoice(sc.Index)
			if sc.Delta.Role != "" {
				ca.role = sc.Delta.Role
			}
			if sc.Delta.Content != "" {
				ca.content.WriteString(sc.Delta.Content)
			}
			if sc.Delta.ReasoningContent != "" {
				ca.reasoningContent.WriteString(sc.Delta.ReasoningContent)
			}
			if sc.Delta.Refusal != "" {
				ca.refusal.WriteString(sc.Delta.Refusal)
			}
			for i, tc := range sc.Delta.ToolCalls {
				idx := i
				if tc.Index != nil {
					idx = *tc.Index
				}
				acc, ok := ca.toolsByIndex[idx]
				if !ok {
					acc = &toolAccum{}
					ca.toolsByIndex[idx] = acc
					ca.toolOrder = append(ca.toolOrder, idx)
				}
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					acc.arguments.WriteString(tc.Function.Arguments)
				}
			}
			if sc.FinishReason != nil && *sc.FinishReason != "" {
				ca.finishReason = *sc.FinishReason
			}
		}
	}

	// Materialise choices in index order.
	indices := make([]int, 0, len(choices))
	for i := range choices {
		indices = append(indices, i)
	}
	sortInts(indices)

	for _, i := range indices {
		ca := choices[i]
		role := ca.role
		if role == "" {
			role = RoleAssistant
		}
		msg := Message{Role: role}
		if s := ca.content.String(); s != "" {
			msg.Content = s
		}
		if s := ca.reasoningContent.String(); s != "" {
			msg.ReasoningContent = s
		}
		if s := ca.refusal.String(); s != "" {
			msg.Refusal = s
		}
		if len(ca.toolOrder) > 0 {
			msg.ToolCalls = make([]ToolCall, 0, len(ca.toolOrder))
			for _, idx := range ca.toolOrder {
				t := ca.toolsByIndex[idx]
				msg.ToolCalls = append(msg.ToolCalls, ToolCall{
					ID:   t.id,
					Type: "function",
					Function: ToolCallFunction{
						Name:      t.name,
						Arguments: t.arguments.String(),
					},
				})
			}
		}
		resp.Choices = append(resp.Choices, Choice{
			Index:        i,
			Message:      msg,
			FinishReason: ca.finishReason,
		})
	}

	return resp, nil
}

// sortInts is a tiny insertion sort to avoid pulling in sort just for this.
func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}
