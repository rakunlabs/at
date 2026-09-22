package antropic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type oauthRefreshTransport func(*http.Request) (*http.Response, error)

func (f oauthRefreshTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func refreshRequestPayload(t *testing.T, r *http.Request) map[string]string {
	t.Helper()
	if got := r.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var payload map[string]string
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestOAuthRefreshRetriesPersistenceWithoutRotatingAgain(t *testing.T) {
	requests, writes := 0, 0
	client := &http.Client{Transport: oauthRefreshTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		payload := refreshRequestPayload(t, r)
		if payload["refresh_token"] != "old-refresh" || payload["scope"] != ClaudeOAuthRefreshScopes {
			t.Fatal("unexpected refresh credential")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`)), Header: make(http.Header)}, nil
	})}
	source := NewOAuthTokenSource("old-access", "old-refresh", time.Now().Add(-time.Hour), client, func(ctx context.Context, previous, access, refresh string, expiry time.Time) error {
		writes++
		if previous != "old-refresh" || access != "new-access" || refresh != "new-refresh" || expiry.IsZero() {
			t.Fatal("incorrect rotation snapshot")
		}
		if writes == 1 {
			return errors.New("temporary database failure")
		}
		return nil
	})
	if _, err := source.Token(t.Context()); err == nil {
		t.Fatal("lost persistence error")
	}
	for range 2 {
		token, err := source.Token(t.Context())
		if err != nil || token != "new-access" {
			t.Fatalf("token %q: %v", token, err)
		}
	}
	if requests != 1 || writes != 2 {
		t.Fatalf("refreshes=%d writes=%d", requests, writes)
	}
}

func TestOAuthRefreshTransportFailureReleasesLock(t *testing.T) {
	client := &http.Client{Transport: oauthRefreshTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("network down") })}
	source := NewOAuthTokenSource("", "refresh", time.Time{}, client, nil)
	if _, err := source.Token(t.Context()); err == nil {
		t.Fatal("expected network error")
	}
	done := make(chan error, 1)
	go func() { _, err := source.Token(t.Context()); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected network error on retry")
		}
	case <-time.After(time.Second):
		t.Fatal("refresh error left token source locked")
	}
}

// TestCoordinatedRefreshAdoptsStoredCredential reproduces the production
// failure: two independent sources share one provider row, one rotates the
// single-use refresh token, and the other later wakes with a consumed copy.
// The coordinator must hand it the stored credential instead of replaying.
func TestCoordinatedRefreshAdoptsStoredCredential(t *testing.T) {
	exchanges := 0
	client := &http.Client{Transport: oauthRefreshTransport(func(r *http.Request) (*http.Response, error) {
		payload := refreshRequestPayload(t, r)
		if payload["refresh_token"] != "stored-refresh" {
			return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"error":"invalid_grant"}`)), Header: make(http.Header)}, nil
		}
		exchanges++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"rotated-access","refresh_token":"rotated-refresh","expires_in":28800}`)), Header: make(http.Header)}, nil
	})}

	// Durable row, already rotated by another source.
	stored := OAuthTokens{AccessToken: "stored-access", RefreshToken: "stored-refresh", ExpiresAt: time.Now().Add(8 * time.Hour)}
	coordinator := func(ctx context.Context, exchange func(context.Context, OAuthTokens) (*OAuthTokens, error)) (*OAuthTokens, error) {
		if TokenFresh(stored.AccessToken, stored.ExpiresAt) {
			snapshot := stored
			return &snapshot, nil
		}
		fresh, err := exchange(ctx, stored)
		if err != nil {
			return nil, err
		}
		stored = *fresh
		snapshot := stored
		return &snapshot, nil
	}

	// This source still holds the credential the other one consumed.
	source := NewOAuthTokenSource("dead-access", "consumed-refresh", time.Now().Add(-time.Minute), client, nil)
	source.SetCoordinator(coordinator)

	token, err := source.Token(t.Context())
	if err != nil {
		t.Fatalf("coordinated refresh failed: %v", err)
	}
	if token != "stored-access" {
		t.Fatalf("expected the stored credential, got %q", token)
	}
	if exchanges != 0 {
		t.Fatalf("consumed refresh token was replayed (%d exchanges)", exchanges)
	}

	// The adopted credential is served from cache until it approaches expiry.
	if token, err = source.Token(t.Context()); err != nil || token != "stored-access" {
		t.Fatalf("cached token %q: %v", token, err)
	}

	// Once the durable credential really expires, exactly one exchange runs
	// and it uses the stored refresh token, not the source's stale copy.
	stored.ExpiresAt = time.Now().Add(-time.Minute)
	next := NewOAuthTokenSource("dead-access", "consumed-refresh", time.Now().Add(-time.Minute), client, nil)
	next.SetCoordinator(coordinator)
	if token, err = next.Token(t.Context()); err != nil || token != "rotated-access" {
		t.Fatalf("token %q: %v", token, err)
	}
	if exchanges != 1 || stored.RefreshToken != "rotated-refresh" {
		t.Fatalf("exchanges=%d stored=%q", exchanges, stored.RefreshToken)
	}
	if next.refreshToken != "rotated-refresh" {
		t.Fatalf("source did not adopt the rotated credential: %q", next.refreshToken)
	}
}

// TestCoordinatedRefreshRetriesPersistenceWithoutRotatingAgain covers the
// coordinator exchanging successfully but failing to save: the rotated
// credential must be retained and the save retried, never re-exchanged.
func TestCoordinatedRefreshRetriesPersistenceWithoutRotatingAgain(t *testing.T) {
	exchanges, writes := 0, 0
	client := &http.Client{Transport: oauthRefreshTransport(func(*http.Request) (*http.Response, error) {
		exchanges++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`)), Header: make(http.Header)}, nil
	})}
	source := NewOAuthTokenSource("old-access", "old-refresh", time.Now().Add(-time.Hour), client, func(ctx context.Context, previous, access, refresh string, expiry time.Time) error {
		writes++
		if previous != "old-refresh" || access != "new-access" || refresh != "new-refresh" {
			t.Fatalf("incorrect rotation snapshot: %q %q %q", previous, access, refresh)
		}
		return nil
	})
	source.SetCoordinator(func(ctx context.Context, exchange func(context.Context, OAuthTokens) (*OAuthTokens, error)) (*OAuthTokens, error) {
		fresh, err := exchange(ctx, OAuthTokens{AccessToken: "old-access", RefreshToken: "old-refresh"})
		if err != nil {
			return nil, err
		}
		return fresh, errors.New("temporary database failure")
	})
	if _, err := source.Token(t.Context()); err == nil {
		t.Fatal("lost persistence error")
	}
	if writes != 0 {
		t.Fatalf("callback ran before the retry: %d", writes)
	}
	token, err := source.Token(t.Context())
	if err != nil || token != "new-access" {
		t.Fatalf("token %q: %v", token, err)
	}
	if exchanges != 1 || writes != 1 {
		t.Fatalf("exchanges=%d writes=%d", exchanges, writes)
	}
}
