package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// supportedNativeTypes lists provider types that support native proxy.
var supportedNativeTypes = map[string]bool{
	"gemini":    true,
	"anthropic": true,
}

// defaultBaseURLs maps provider types to their default base URLs.
var defaultBaseURLs = map[string]string{
	"gemini":    "https://generativelanguage.googleapis.com",
	"anthropic": "https://api.anthropic.com",
}

// nativeProxyClient is a shared HTTP client for native proxy requests.
// It has a generous timeout since LLM responses can take a while,
// and streaming responses need to stay open.
var nativeProxyClient = &http.Client{
	Timeout: 10 * time.Minute,
	// Don't follow redirects — let the client see them.
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// NativeProxy handles POST /gateway/v1/native/{provider_key}/*
//
// It proxies the request body unchanged to the upstream provider's native API.
// Only minimal parsing is done: extracting the provider key for lookup and the
// model name for access control / logging.
//
// Supported provider types: "gemini", "anthropic".
func (s *Server) NativeProxy(w http.ResponseWriter, r *http.Request) {
	// ── Auth ──
	auth, authErr := s.authenticateRequest(r)
	if authErr != "" {
		httpResponse(w, authErr, http.StatusUnauthorized)
		return
	}

	// ── Extract provider key and remaining path ──
	// Ada route: /v1/native/{provider_key}/*
	providerKey := r.PathValue("provider_key")
	upstreamPath := "/" + r.PathValue("*") // wildcard value has no leading "/"

	if providerKey == "" {
		httpResponse(w, "missing provider key in path", http.StatusBadRequest)
		return
	}

	if upstreamPath == "/" {
		httpResponse(w, "missing upstream path after provider key", http.StatusBadRequest)
		return
	}

	// ── Provider lookup ──
	info, ok := s.getProviderInfo(providerKey)
	if !ok {
		httpResponse(w, fmt.Sprintf("unknown provider %q", providerKey), http.StatusNotFound)
		return
	}

	// ── Provider type gate ──
	if !supportedNativeTypes[info.providerType] {
		supported := make([]string, 0, len(supportedNativeTypes))
		for k := range supportedNativeTypes {
			supported = append(supported, fmt.Sprintf("%q", k))
		}
		httpResponse(w, fmt.Sprintf(
			"native proxy not supported for provider type %q (supported: %s)",
			info.providerType, strings.Join(supported, ", "),
		), http.StatusBadRequest)
		return
	}

	// ── Read request body (needed for Anthropic model extraction) ──
	// We read the body once and use it for both model extraction and forwarding.
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to read request body: %v", err), http.StatusBadRequest)
		return
	}

	// ── Extract model name for access control ──
	model, modelErr := extractNativeModel(info.providerType, upstreamPath, bodyBytes)
	if modelErr != "" {
		httpResponse(w, modelErr, http.StatusBadRequest)
		return
	}

	fullModelID := providerKey + "/" + model
	if !auth.isModelAllowed(providerKey, fullModelID) {
		httpResponse(w, fmt.Sprintf("model %q is not allowed for this token", fullModelID), http.StatusForbidden)
		return
	}

	// ── Build upstream URL ──
	baseURL := info.config.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURLs[info.providerType]
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	upstreamURL := baseURL + upstreamPath
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	if _, err := url.Parse(upstreamURL); err != nil {
		httpResponse(w, fmt.Sprintf("invalid upstream URL: %v", err), http.StatusInternalServerError)
		return
	}

	slog.Debug("native proxy",
		"provider", providerKey,
		"type", info.providerType,
		"model", model,
		"method", r.Method,
		"upstream", upstreamURL,
	)

	// ── Build upstream request ──
	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, bytes.NewReader(bodyBytes))
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to create upstream request: %v", err), http.StatusInternalServerError)
		return
	}

	// Copy Content-Type from the original request.
	if ct := r.Header.Get("Content-Type"); ct != "" {
		upstreamReq.Header.Set("Content-Type", ct)
	} else {
		upstreamReq.Header.Set("Content-Type", "application/json")
	}

	// Set provider-specific auth headers.
	setNativeAuthHeaders(upstreamReq, &info)

	// Copy any extra headers from provider config.
	for k, v := range info.config.ExtraHeaders {
		upstreamReq.Header.Set(k, v)
	}

	// ── Execute upstream request ──
	resp, err := nativeProxyClient.Do(upstreamReq)
	if err != nil {
		httpResponse(w, fmt.Sprintf("upstream request failed: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// ── Copy response back to client ──
	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// For streaming responses (SSE), flush each chunk as it arrives.
	if flusher, ok := w.(http.Flusher); ok && isSSEResponse(resp) {
		buf := make([]byte, 4096)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				if _, writeErr := w.Write(buf[:n]); writeErr != nil {
					slog.Error("native proxy: write to client failed", "error", writeErr)
					return
				}
				flusher.Flush()
			}
			if readErr != nil {
				if readErr != io.EOF {
					slog.Error("native proxy: read from upstream failed", "error", readErr)
				}
				return
			}
		}
	}

	// Non-streaming: copy the full body.
	if _, err := io.Copy(w, resp.Body); err != nil {
		slog.Error("native proxy: failed to copy response body", "error", err)
	}
}

// extractNativeModel extracts the model name from the request based on
// the provider type. Returns the model name or an error message string.
//
// - Gemini: model is in the URL path (/v1beta/models/{model}:{method})
// - Anthropic: model is in the JSON request body ({"model": "..."})
func extractNativeModel(providerType, upstreamPath string, body []byte) (string, string) {
	switch providerType {
	case "gemini":
		model := extractGeminiModelFromPath(upstreamPath)
		if model == "" {
			return "", "could not extract model from upstream path; expected /v1beta/models/{model}:{method}"
		}
		return model, ""

	case "anthropic":
		model := extractAnthropicModelFromBody(body)
		if model == "" {
			return "", "could not extract model from request body; expected {\"model\": \"...\"}"
		}
		return model, ""

	default:
		return "", fmt.Sprintf("unsupported provider type %q for model extraction", providerType)
	}
}

// extractGeminiModelFromPath extracts the model name from a Gemini API path.
//
// Gemini paths look like:
//
//	/v1beta/models/gemini-2.5-flash:generateContent
//	/v1beta/models/gemini-2.5-pro:streamGenerateContent
//	/v1/models/gemini-2.5-flash:generateContent
//
// Returns the model name (e.g., "gemini-2.5-flash") or "" if not found.
func extractGeminiModelFromPath(path string) string {
	const marker = "/models/"
	idx := strings.Index(path, marker)
	if idx < 0 {
		return ""
	}

	rest := path[idx+len(marker):]
	if rest == "" {
		return ""
	}

	if colonIdx := strings.IndexByte(rest, ':'); colonIdx > 0 {
		return rest[:colonIdx]
	}
	if slashIdx := strings.IndexByte(rest, '/'); slashIdx > 0 {
		return rest[:slashIdx]
	}

	return rest
}

// extractAnthropicModelFromBody extracts the "model" field from an Anthropic
// JSON request body. Only the top-level "model" key is read; the rest of the
// body is not parsed.
//
// Returns the model name (e.g., "claude-sonnet-4-20250514") or "" if not found.
func extractAnthropicModelFromBody(body []byte) string {
	var partial struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &partial); err != nil {
		return ""
	}
	return partial.Model
}

// setNativeAuthHeaders sets the appropriate authentication headers for the
// upstream provider based on the provider type.
func setNativeAuthHeaders(req *http.Request, info *ProviderInfo) {
	switch info.providerType {
	case "gemini":
		if info.config.APIKey != "" {
			req.Header.Set("x-goog-api-key", info.config.APIKey)
		}
	case "anthropic":
		if info.config.APIKey != "" {
			req.Header.Set("x-api-key", info.config.APIKey)
		}
		req.Header.Set("anthropic-version", "2023-06-01")
	}
}

// isSSEResponse checks if the upstream response is a Server-Sent Events stream.
func isSSEResponse(resp *http.Response) bool {
	ct := resp.Header.Get("Content-Type")
	return strings.Contains(ct, "text/event-stream")
}
