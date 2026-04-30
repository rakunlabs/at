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

// Chat issues a non-streaming POST /chat/completions request.
//
// If req.Model is empty and the client was created with [WithModel], the
// configured default is used. The req.Stream flag is forced to false; use
// [Client.ChatStream] for streaming.
//
// Non-2xx responses are returned as either [*APIError] or [*RateLimitError].
func (c *Client) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	if req == nil {
		return nil, errors.New("openai-compatible: nil ChatRequest")
	}
	if req.Model == "" {
		req.Model = c.model
	}
	if req.Model == "" {
		return nil, errors.New("openai-compatible: ChatRequest.Model is required (or use WithModel)")
	}
	req.Stream = false
	req.StreamOptions = nil

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("openai-compatible: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	var (
		out        ChatResponse
		statusCode int
		respHeader http.Header
		rawBody    []byte
	)
	if err := ok.Do(c.httpClient, httpReq, func(r *http.Response) error {
		statusCode = r.StatusCode
		respHeader = r.Header
		var rerr error
		rawBody, rerr = io.ReadAll(r.Body)
		if rerr != nil {
			return rerr
		}
		if statusCode >= 200 && statusCode < 300 {
			if jerr := json.Unmarshal(rawBody, &out); jerr != nil {
				return fmt.Errorf("decode response: %w (body: %s)", jerr, truncate(string(rawBody), 500))
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("openai-compatible: chat request: %w", err)
	}

	if statusCode < 200 || statusCode >= 300 {
		return nil, buildAPIError(statusCode, respHeader, rawBody)
	}

	return &out, nil
}

// buildAPIError constructs the typed error for a non-2xx response.
func buildAPIError(status int, header http.Header, body []byte) error {
	apiErr := &APIError{
		StatusCode: status,
		Status:     http.StatusText(status),
		RawBody:    string(body),
		Header:     header.Clone(),
	}
	var env errorEnvelope
	if err := json.Unmarshal(body, &env); err == nil && env.Error != nil {
		apiErr.Message = env.Error.Message
		apiErr.Type = env.Error.Type
		apiErr.Code = env.Error.codeString()
		apiErr.Param = env.Error.Param
	}

	if status == http.StatusTooManyRequests ||
		apiErr.Type == "rate_limit_error" || apiErr.Type == "tokens" || apiErr.Type == "requests" ||
		apiErr.Code == "rate_limit_exceeded" {
		return &RateLimitError{
			APIError:   *apiErr,
			RetryAfter: parseRetryAfter(header),
		}
	}
	return apiErr
}
