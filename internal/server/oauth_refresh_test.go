package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestVerifyConnectionOAuthTokens(t *testing.T) {
	t.Run("offline connection requires a new refresh token", func(t *testing.T) {
		connector := &service.Connector{OAuth: &service.ConnectorOAuth{AccessType: "offline", TokenURL: "https://unused.example"}}
		err := verifyConnectionOAuthTokens(t.Context(), connector, "client", "secret", &oauthTokenResult{AccessToken: "access"})
		if err == nil || !strings.Contains(err.Error(), "did not return a refresh token") {
			t.Fatalf("expected missing refresh token error, got %v", err)
		}
	})

	t.Run("provider rejection is reported", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Bad Request"}`))
		}))
		defer upstream.Close()

		connector := &service.Connector{OAuth: &service.ConnectorOAuth{AccessType: "offline", TokenURL: upstream.URL}}
		err := verifyConnectionOAuthTokens(t.Context(), connector, "client", "secret", &oauthTokenResult{RefreshToken: "rejected"})
		if err == nil || !strings.Contains(err.Error(), "invalid_grant") {
			t.Fatalf("expected invalid_grant error, got %v", err)
		}
	})

	t.Run("verified response and rotated refresh token are adopted", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse form: %v", err)
			}
			for key, want := range map[string]string{
				"client_id":     "client",
				"client_secret": "secret",
				"refresh_token": "initial-refresh",
				"grant_type":    "refresh_token",
			} {
				if got := r.Form.Get(key); got != want {
					t.Errorf("%s = %q, want %q", key, got, want)
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"verified-access","refresh_token":"rotated-refresh","expires_in":3600}`))
		}))
		defer upstream.Close()

		connector := &service.Connector{OAuth: &service.ConnectorOAuth{AccessType: "offline", TokenURL: upstream.URL}}
		tok := &oauthTokenResult{AccessToken: "initial-access", RefreshToken: "initial-refresh", ExpiresIn: 60}
		if err := verifyConnectionOAuthTokens(t.Context(), connector, "client", "secret", tok); err != nil {
			t.Fatalf("verify token: %v", err)
		}
		if tok.AccessToken != "verified-access" || tok.RefreshToken != "rotated-refresh" || tok.ExpiresIn != 3600 {
			t.Fatalf("verified tokens not adopted: %+v", tok)
		}
	})

	t.Run("online connector is unchanged", func(t *testing.T) {
		connector := &service.Connector{OAuth: &service.ConnectorOAuth{TokenURL: "https://unused.example"}}
		tok := &oauthTokenResult{AccessToken: "access"}
		if err := verifyConnectionOAuthTokens(t.Context(), connector, "client", "secret", tok); err != nil {
			t.Fatalf("online connector verification: %v", err)
		}
	})
}
