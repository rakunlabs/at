package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func withSearchEndpoints(t *testing.T, srv *httptest.Server) {
	t.Helper()
	old := []string{tavilyEndpoint, exaEndpoint, braveEndpoint, firecrawlEndpoint}
	tavilyEndpoint, exaEndpoint, braveEndpoint, firecrawlEndpoint = srv.URL+"/tavily", srv.URL+"/exa", srv.URL+"/brave", srv.URL+"/firecrawl"
	t.Cleanup(func() {
		tavilyEndpoint, exaEndpoint, braveEndpoint, firecrawlEndpoint = old[0], old[1], old[2], old[3]
		webSearchCooldowns.mu.Lock()
		webSearchCooldowns.until = map[string]time.Time{}
		webSearchCooldowns.mu.Unlock()
	})
}

func TestWebSearchProvidersAndFallback(t *testing.T) {
	var tavilyCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/tavily":
			tavilyCalls.Add(1)
			if r.Header.Get("Authorization") != "Bearer tv-key" {
				t.Errorf("tavily auth = %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
		case "/exa":
			if r.Header.Get("x-api-key") != "exa-key" {
				t.Errorf("exa auth = %q", r.Header.Get("x-api-key"))
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["query"] != "go generics" || body["numResults"] != float64(3) {
				t.Errorf("exa body = %v", body)
			}
			_, _ = w.Write([]byte(`{"results":[{"title":"Generics","url":"https://go.dev/doc/tutorial/generics","highlights":["Type parameters","constraints"]}]}`))
		case "/brave":
			if r.URL.Query().Get("q") != "go generics" || r.Header.Get("X-Subscription-Token") != "brave-key" {
				t.Errorf("brave request = %s %q", r.URL.RawQuery, r.Header.Get("X-Subscription-Token"))
			}
			_, _ = w.Write([]byte(`{"web":{"results":[{"title":"<strong>Go</strong> generics","url":"https://example.com","description":"About <strong>generics</strong>"}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	withSearchEndpoints(t, srv)

	vars := map[string]string{"tavily_api_key": "tv-key", "exa_api_key": "exa-key"}
	lookup := func(k string) string { return vars[k] }

	got, err := runWebSearch(context.Background(), srv.Client(), "go generics", 3, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "(exa)") || !strings.Contains(got, "1. Generics\n   https://go.dev/doc/tutorial/generics\n   Type parameters … constraints") {
		t.Fatalf("result:\n%s", got)
	}
	// Tavily is cooling down after its 429, so it is not asked again.
	if _, err := runWebSearch(context.Background(), srv.Client(), "go generics", 3, lookup); err != nil {
		t.Fatal(err)
	}
	if tavilyCalls.Load() != 1 {
		t.Fatalf("tavily calls = %d, want 1 (cooldown)", tavilyCalls.Load())
	}

	// A preferred provider goes first.
	vars["brave_api_key"], vars["web_search_provider"] = "brave-key", "brave"
	got, err = runWebSearch(context.Background(), srv.Client(), "go generics", 3, lookup)
	if err != nil || !strings.Contains(got, "(brave)") || !strings.Contains(got, "Go generics") || strings.Contains(got, "<strong>") {
		t.Fatalf("brave: %q %v", got, err)
	}
}

func TestWebSearchNotConfiguredAndAllCooling(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	withSearchEndpoints(t, srv)

	if _, err := runWebSearch(context.Background(), srv.Client(), "q", 3, func(string) string { return "" }); err == nil || !strings.Contains(err.Error(), "tavily_api_key") {
		t.Fatalf("unconfigured: %v", err)
	}
	lookup := func(k string) string {
		if k == "tavily_api_key" {
			return "k"
		}
		return ""
	}
	if _, err := runWebSearch(context.Background(), srv.Client(), "q", 3, lookup); err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("first: %v", err)
	}
	if _, err := runWebSearch(context.Background(), srv.Client(), "q", 3, lookup); err == nil || !strings.Contains(err.Error(), "cooling down") {
		t.Fatalf("second: %v", err)
	}
}
