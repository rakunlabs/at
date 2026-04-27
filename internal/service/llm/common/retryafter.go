package common

import (
	"net/http"
	"strconv"
	"time"
)

// ParseRetryAfter parses an HTTP Retry-After header value into a duration.
//
// The header may be:
//   - A non-negative integer number of seconds (RFC 7231).
//   - An HTTP-date specifying a future timestamp (RFC 7231).
//
// Returns 0 (no wait suggested) when the header is missing, malformed,
// or refers to a past timestamp.
func ParseRetryAfter(h http.Header) time.Duration {
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}
	// Integer seconds form.
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	// HTTP-date form.
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d <= 0 {
			return 0
		}
		return d
	}
	return 0
}
