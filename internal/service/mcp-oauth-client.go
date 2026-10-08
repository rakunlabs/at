package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// MCPAccessTokenSource resolves an admitted account's token on each request.
// rejected is empty for normal reads. After a 401 it is the rejected token:
// a row-locked source must adopt a newer stored token or refresh exactly once.
// The source must revalidate identity/resource admission before returning secrets.
type MCPAccessTokenSource func(ctx context.Context, rejected string) (string, error)

// WithMCPAccessToken authenticates every MCP HTTP request, including initialize,
// notifications and session cleanup. Tokens are not saved on the client. Static
// Authorization headers are overridden, never used as a fallback on failure.
func WithMCPAccessToken(source MCPAccessTokenSource) HTTPMCPClientOption {
	return func(c *HTTPMCPClient) {
		client := *c.httpClient
		base := client.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		client.Transport = &mcpOAuthTransport{base: base, source: source}
		// Even same-origin redirects could change the resource/audience. Never
		// send an MCP access token to an endpoint other than the configured one.
		client.CheckRedirect = func(*http.Request, []*http.Request) error {
			return errors.New("OAuth MCP redirects are not allowed")
		}
		c.httpClient = &client
	}
}

type mcpOAuthTransport struct {
	base   http.RoundTripper
	source MCPAccessTokenSource
}

func (t *mcpOAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// RoundTripper must close the body even if admission prevents dispatch.
	closeUndispatched := func() {
		if req.Body != nil {
			_ = req.Body.Close()
		}
	}
	if t.source == nil {
		closeUndispatched()
		return nil, errors.New("MCP OAuth token source is not configured")
	}
	token, err := t.source(req.Context(), "")
	if err != nil {
		closeUndispatched()
		return nil, fmt.Errorf("resolve MCP OAuth token: %w", err)
	}
	authorized, err := mcpBearerRequest(req, token)
	if err != nil {
		closeUndispatched()
		return nil, err
	}
	resp, err := t.base.RoundTrip(authorized)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	// Non-replayable bodies must not be retried. MCP's JSON requests have a
	// GetBody function, but callers of this transport may supply other bodies.
	if req.Body != nil && req.GetBody == nil {
		return resp, nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	newToken, err := t.source(req.Context(), token)
	if err != nil {
		return nil, fmt.Errorf("renew MCP OAuth token: %w", err)
	}
	retry, err := mcpBearerRequest(req, newToken)
	if err != nil {
		return nil, err
	}
	if req.GetBody != nil {
		retry.Body, err = req.GetBody()
		if err != nil {
			return nil, fmt.Errorf("replay MCP OAuth request: %w", err)
		}
	}
	// Deliberately call the base transport, not ourselves: a second 401 is
	// terminal, and a 403 (insufficient scope) never triggers refresh.
	return t.base.RoundTrip(retry)
}

func mcpBearerRequest(req *http.Request, token string) (*http.Request, error) {
	if token == "" || strings.ContainsAny(token, "\r\n\t ") {
		return nil, errors.New("MCP OAuth source returned an empty or invalid access token")
	}
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+token)
	return clone, nil
}

// MCPHTTPError preserves status and challenge metadata for OAuth recovery/UI
// without requiring brittle parsing of an upstream body or error string.
type MCPHTTPError struct {
	StatusCode      int
	WWWAuthenticate string
	Body            string
}

func (e *MCPHTTPError) Error() string {
	if e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden {
		return fmt.Sprintf("MCP HTTP error %d: authorization required or insufficient scope", e.StatusCode)
	}
	return fmt.Sprintf("HTTP error %d: %s", e.StatusCode, e.Body)
}
