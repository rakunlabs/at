package antropic

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type oauthRefreshTransport func(*http.Request) (*http.Response, error)

func (f oauthRefreshTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOAuthRefreshRetriesPersistenceWithoutRotatingAgain(t *testing.T) {
	requests, writes := 0, 0
	client := &http.Client{Transport: oauthRefreshTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.PostForm.Get("refresh_token") != "old-refresh" {
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
