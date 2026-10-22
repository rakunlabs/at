package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// TestConnectionAPI handles POST /api/v1/connections/{id}/test. For an OAuth
// connector with a stored refresh token it mints a fresh access token (and
// adopts a rotated refresh token), refreshes the account label and records
// the outcome in metadata, so "Verified" on the Connections page reflects the
// credential working now rather than the day it was authorized. The access
// token itself is never returned or stored. Other kinds report whether their
// required fields are present.
func (s *Server) TestConnectionAPI(w http.ResponseWriter, r *http.Request) {
	if s.connectionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	conn, err := s.connectionStore.GetConnection(r.Context(), id)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to load connection: %v", err), http.StatusInternalServerError)
		return
	}
	if conn == nil || conn.Provider == service.GitSSHCredentialProvider {
		httpResponse(w, fmt.Sprintf("connection %q not found", id), http.StatusNotFound)
		return
	}
	if conn.Credentials.MCPOAuth != nil {
		httpResponse(w, "MCP accounts refresh automatically on use; reconnect them instead", http.StatusBadRequest)
		return
	}
	connector, err := s.resolveConnector(r.Context(), conn.Provider)
	if err != nil {
		httpResponse(w, "failed to resolve connector: "+err.Error(), http.StatusInternalServerError)
		return
	}

	result := map[string]any{"connection_id": conn.ID, "provider": conn.Provider}
	if !isOAuth2Connector(connector) {
		missing := missingConnectorFields(connector, conn)
		result["ok"] = len(missing) == 0
		if len(missing) > 0 {
			result["message"] = "missing: " + strings.Join(missing, ", ")
		} else {
			result["message"] = "all required fields are set (the service itself was not contacted)"
		}
		httpResponseJSON(w, result, http.StatusOK)
		return
	}
	if conn.Credentials.RefreshToken == "" {
		result["ok"] = false
		result["message"] = "not authorized yet; use Connect"
		httpResponseJSON(w, result, http.StatusOK)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	tok, testErr := refreshConnectorToken(ctx, connector, conn.Credentials.ClientID, conn.Credentials.ClientSecret, conn.Credentials.RefreshToken)
	if conn.Metadata == nil {
		conn.Metadata = map[string]any{}
	}
	now := time.Now().UTC()
	conn.Metadata["oauth_checked_at"] = now.Format(time.RFC3339)
	if testErr != nil {
		conn.Metadata["oauth_status"] = "failed"
		conn.Metadata["oauth_error"] = testErr.Error()
	} else {
		conn.Metadata["oauth_status"] = "verified"
		conn.Metadata["oauth_verified_at"] = now.Format(time.RFC3339)
		delete(conn.Metadata, "oauth_error")
		if tok.ExpiresIn > 0 {
			conn.Metadata["oauth_access_expires_at"] = now.Add(time.Duration(tok.ExpiresIn) * time.Second).Format(time.RFC3339)
		}
		if tok.RefreshToken != "" {
			conn.Credentials.RefreshToken = tok.RefreshToken
		}
		if label := fetchConnectorAccountLabel(ctx, connector, tok.AccessToken); label != "" {
			conn.AccountLabel = label
		}
	}
	conn.UpdatedBy = s.getUserEmail(r)
	if _, err := s.connectionStore.UpdateConnection(r.Context(), conn.ID, *conn); err != nil {
		if workspaceBusinessError(w, err) {
			return
		}
		httpResponse(w, fmt.Sprintf("failed to record result: %v", err), http.StatusInternalServerError)
		return
	}
	result["ok"] = testErr == nil
	result["account_label"] = conn.AccountLabel
	if testErr != nil {
		result["message"] = testErr.Error() + " — authorize again with Connect"
	} else {
		result["message"] = "token refreshed successfully"
	}
	httpResponseJSON(w, result, http.StatusOK)
}

func missingConnectorFields(c *service.Connector, conn *service.Connection) []string {
	if c == nil {
		return nil
	}
	values := map[string]bool{}
	creds := conn.Credentials
	var missing []string
	for _, f := range c.Fields {
		if !f.Required {
			continue
		}
		set := false
		switch {
		case strings.HasSuffix(f.Key, "_client_id"):
			set = creds.ClientID != ""
		case strings.HasSuffix(f.Key, "_client_secret"):
			set = creds.ClientSecret != ""
		case strings.HasSuffix(f.Key, "_refresh_token"):
			set = creds.RefreshToken != ""
		case strings.HasSuffix(f.Key, "_api_key"):
			set = creds.APIKey != ""
		default:
			set = creds.Extra[f.Key] != ""
		}
		if !set && !values[f.Key] {
			values[f.Key] = true
			label := f.Label
			if label == "" {
				label = f.Key
			}
			missing = append(missing, label)
		}
	}
	return missing
}

// refreshConnectorToken performs one refresh_token grant against the
// connector's token endpoint.
func refreshConnectorToken(ctx context.Context, c *service.Connector, clientID, clientSecret, refreshToken string) (*oauthTokenResult, error) {
	form := url.Values{
		"client_id":     {clientID},
		"refresh_token": {refreshToken},
		"grant_type":    {"refresh_token"},
	}
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.OAuth.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("invalid token response (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || out.Error != "" {
		msg := strings.Trim(strings.Join([]string{out.Error, out.ErrorDesc}, ": "), ": ")
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("refresh token rejected: %s", msg)
	}
	if out.AccessToken == "" {
		return nil, fmt.Errorf("token endpoint returned no access token")
	}
	return &oauthTokenResult{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken, ExpiresIn: out.ExpiresIn}, nil
}
