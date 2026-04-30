package openaicompatible

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// APIError wraps a non-2xx response from the server. The HTTP status code is
// always populated; Type, Code, and Param come from the OpenAI-style error
// envelope and may be empty for non-OpenAI servers.
type APIError struct {
	StatusCode int
	Status     string
	Message    string
	Type       string
	Code       string
	Param      string
	// RawBody is the original (possibly truncated) response body, useful
	// for debugging when the server returned a non-standard error shape.
	RawBody string
	// Header is the response header (kept so callers can inspect rate-limit
	// or trace headers).
	Header http.Header
}

func (e *APIError) Error() string {
	parts := []string{fmt.Sprintf("openai-compatible: HTTP %d", e.StatusCode)}
	if e.Type != "" {
		parts = append(parts, e.Type)
	}
	if e.Code != "" {
		parts = append(parts, e.Code)
	}
	msg := e.Message
	if msg == "" && e.RawBody != "" {
		msg = truncate(e.RawBody, 500)
	}
	if msg != "" {
		return strings.Join(parts, " ") + ": " + msg
	}
	return strings.Join(parts, " ")
}

// RateLimitError is a typed error returned when the server responds with
// HTTP 429 or an explicit rate-limit error envelope. Callers can use
// [errors.As] to detect it and honour the suggested RetryAfter delay.
type RateLimitError struct {
	APIError
	// RetryAfter is the parsed value of the Retry-After response header, if
	// present. Zero means the header was absent or unparseable; callers
	// should fall back to their own backoff policy in that case.
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	base := e.APIError.Error()
	if e.RetryAfter > 0 {
		return base + fmt.Sprintf(" (retry-after %s)", e.RetryAfter)
	}
	return base
}

// Unwrap so errors.Is(err, &APIError{}) and errors.As both work.
func (e *RateLimitError) Unwrap() error { return &e.APIError }

// IsRateLimit reports whether err is or wraps a [*RateLimitError].
func IsRateLimit(err error) bool {
	var rle *RateLimitError
	return errors.As(err, &rle)
}

// IsAPIError reports whether err is or wraps an [*APIError].
func IsAPIError(err error) bool {
	var ae *APIError
	return errors.As(err, &ae)
}

// errorEnvelope is the OpenAI-style error JSON shape:
//
//	{"error": {"message": "...", "type": "...", "code": "...", "param": "..."}}
type errorEnvelope struct {
	Error *errorBody `json:"error"`
}

type errorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"` // sometimes string, sometimes int
	Param   string `json:"param"`
}

// codeString coerces the code field (which servers report as either string
// or integer) to a string.
func (b *errorBody) codeString() string {
	switch v := b.Code.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

// parseRetryAfter extracts a Retry-After header. The header may be an
// integer (seconds) or an HTTP-date.
func parseRetryAfter(h http.Header) time.Duration {
	if h == nil {
		return 0
	}
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d < 0 {
			return 0
		}
		return d
	}
	return 0
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
