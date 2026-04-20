package server

import "strings"

// classifyHTTPError maps a provider-side LLM error to a stable error_code for
// the usage dashboard. Kept in sync with nodes.classifyLLMError (duplicated to
// avoid a cross-package dependency on internals). See internal/service/workflow/nodes/usage.go.
func classifyHTTPError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "429"),
		strings.Contains(msg, "rate limit"),
		strings.Contains(msg, "rate_limit"),
		strings.Contains(msg, "too many requests"):
		return "rate_limit"
	case strings.Contains(msg, "deadline exceeded"),
		strings.Contains(msg, "timeout"),
		strings.Contains(msg, "timed out"),
		strings.Contains(msg, "context canceled"):
		return "timeout"
	case strings.Contains(msg, "401"),
		strings.Contains(msg, "403"),
		strings.Contains(msg, "unauthorized"),
		strings.Contains(msg, "forbidden"),
		strings.Contains(msg, "invalid_api_key"),
		strings.Contains(msg, "invalid api key"):
		return "auth"
	case strings.Contains(msg, "quota"),
		strings.Contains(msg, "insufficient"),
		strings.Contains(msg, "billing"),
		strings.Contains(msg, "exceeded your current"):
		return "quota"
	case strings.Contains(msg, "400"),
		strings.Contains(msg, "invalid_request"),
		strings.Contains(msg, "bad request"):
		return "invalid_request"
	case strings.Contains(msg, "500"),
		strings.Contains(msg, "502"),
		strings.Contains(msg, "503"),
		strings.Contains(msg, "504"),
		strings.Contains(msg, "520"):
		return "provider_error"
	default:
		return "unknown"
	}
}
