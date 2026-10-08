package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPOAuthClientRefresh(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		want   int
	}{
		{"401 refresh once", http.StatusUnauthorized, 2},
		{"403 never refresh", http.StatusForbidden, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls, requests := 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Header.Get("Authorization") != "Bearer fresh" {
					w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://example.com/meta"`)
					w.WriteHeader(tt.status)
					_, _ = w.Write([]byte("upstream-secret"))
					return
				}
				var request MCPRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				_ = json.NewEncoder(w).Encode(MCPResponse{Jsonrpc: "2.0", ID: request.ID, Result: json.RawMessage(`{"protocolVersion":"2025-03-26","tools":[]}`)})
			}))
			defer srv.Close()
			source := func(ctx context.Context, rejected string) (string, error) {
				calls++
				if rejected != "" {
					if rejected != "stale" {
						t.Errorf("rejected token=%q", rejected)
					}
					return "fresh", nil
				}
				return "stale", nil
			}
			client, err := NewHTTPMCPClient(t.Context(), srv.URL, WithHeaders(map[string]string{"Authorization": "Bearer static"}), WithMCPAccessToken(source))
			if tt.status == http.StatusForbidden {
				var httpErr *MCPHTTPError
				if !errors.As(err, &httpErr) || httpErr.StatusCode != tt.status || httpErr.WWWAuthenticate == "" || strings.Contains(err.Error(), "upstream-secret") {
					t.Fatalf("expected safe typed 403, got %v", err)
				}
				if calls != tt.want || requests != tt.want {
					t.Fatalf("token calls=%d requests=%d", calls, requests)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// Initialize and its notification each authenticate, including a
			// single refresh in this intentionally always-stale token source.
			if calls != 4 || requests != 4 {
				t.Fatalf("token calls=%d requests=%d", calls, requests)
			}
			if _, err := client.ListTools(t.Context()); err != nil {
				t.Fatal(err)
			}
			if calls != 6 || requests != 6 {
				t.Fatalf("token calls=%d requests=%d", calls, requests)
			}
		})
	}
}

func TestMCPOAuthRetryIsBounded(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := NewHTTPMCPClient(t.Context(), srv.URL, WithMCPAccessToken(func(context.Context, string) (string, error) {
		calls++
		return "token", nil
	}))
	var httpErr *MCPHTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 401 || calls != 2 {
		t.Fatalf("calls=%d error=%v", calls, err)
	}
}

func TestMCPOAuthFailsClosed(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Redirect(w, r, "/redirect", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	for _, source := range []MCPAccessTokenSource{
		nil,
		func(context.Context, string) (string, error) { return "", nil },
		func(context.Context, string) (string, error) { return "bad\r\ntoken", nil },
		func(context.Context, string) (string, error) { return "", fmt.Errorf("admission revoked") },
	} {
		if _, err := NewHTTPMCPClient(t.Context(), srv.URL, WithHeaders(map[string]string{"Authorization": "Bearer static"}), WithMCPAccessToken(source)); err == nil {
			t.Fatal("invalid token source accepted")
		}
	}
	if requests != 0 {
		t.Fatal("static credential used as fallback")
	}
	if _, err := NewHTTPMCPClient(t.Context(), srv.URL, WithMCPAccessToken(func(context.Context, string) (string, error) { return "token", nil })); err == nil {
		t.Fatal("OAuth redirect followed")
	}
	if requests != 1 {
		t.Fatalf("redirect was followed: requests=%d", requests)
	}
}
