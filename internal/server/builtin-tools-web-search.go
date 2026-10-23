package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// search_web queries a hosted search API with a key stored as a workspace
// variable, so models without native search (OpenAI-compatible, local) can
// search too. A provider that answers 429 cools down (Retry-After, default
// 60s) and the next configured provider is tried.
const (
	webSearchToolName       = "search_web"
	webSearchProviderVar    = "web_search_provider"
	webSearchDefaultResults = 8
	webSearchMaxResults     = 20
	webSearchTimeout        = 30 * time.Second
	webSearchSnippetChars   = 1200
)

type webSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

type webSearchProvider struct {
	id     string
	keyVar string
	search func(ctx context.Context, client *http.Client, key, query string, limit int) ([]webSearchResult, error)
}

// webSearchProviders lists the supported providers in fallback order.
var webSearchProviders = []webSearchProvider{
	{id: "tavily", keyVar: "tavily_api_key", search: searchTavily},
	{id: "exa", keyVar: "exa_api_key", search: searchExa},
	{id: "brave", keyVar: "brave_api_key", search: searchBrave},
	{id: "firecrawl", keyVar: "firecrawl_api_key", search: searchFirecrawl},
}

// webSearchRateLimitError carries a provider's Retry-After.
type webSearchRateLimitError struct {
	provider   string
	retryAfter time.Duration
}

func (e *webSearchRateLimitError) Error() string {
	return fmt.Sprintf("%s rate limit exceeded", e.provider)
}

// webSearchCooldowns is per process: each replica relearns from its own 429.
var webSearchCooldowns = struct {
	mu    sync.Mutex
	until map[string]time.Time
}{until: map[string]time.Time{}}

func webSearchCoolingDown(id string, now time.Time) bool {
	webSearchCooldowns.mu.Lock()
	defer webSearchCooldowns.mu.Unlock()
	return now.Before(webSearchCooldowns.until[id])
}

func webSearchCoolDown(id string, d time.Duration) {
	if d <= 0 {
		d = time.Minute
	}
	d = min(max(d, time.Second), 15*time.Minute)
	webSearchCooldowns.mu.Lock()
	webSearchCooldowns.until[id] = time.Now().Add(d)
	webSearchCooldowns.mu.Unlock()
}

var webSearchToolDef = builtinToolDef{
	Name: webSearchToolName,
	Description: "Search the web and return result titles, URLs and snippets. Use url_fetch to read a result. " +
		"Backed by a hosted search API (Tavily, Exa, Brave or Firecrawl) whose key is configured as a workspace variable.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query":       map[string]any{"type": "string", "description": "The search query"},
			"max_results": map[string]any{"type": "integer", "description": "Number of results (default 8, max 20)"},
		},
		"required": []string{"query"},
	},
}

func (s *Server) execWebSearch(ctx context.Context, args map[string]any) (string, error) {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return "", errors.New("query is required")
	}
	limit := webSearchDefaultResults
	if n, ok := args["max_results"].(float64); ok && n > 0 {
		limit = min(int(n), webSearchMaxResults)
	}
	return runWebSearch(ctx, http.DefaultClient, query, limit, func(key string) string {
		return s.webSearchVariable(ctx, key)
	})
}

// webSearchVariable reads a configuration variable through the caller's
// variable-use admission, falling back to the installation lookup for
// unbound callers.
func (s *Server) webSearchVariable(ctx context.Context, key string) string {
	if s.variableStore == nil {
		return ""
	}
	if _, _, bound := service.ExecutionFromContext(ctx); bound {
		if service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "variables.use", ResourceID: key}) != nil {
			return ""
		}
		if resolver, ok := s.variableStore.(variableUseResolver); ok {
			v, err := resolver.ResolveVariableForUse(ctx, key)
			if err != nil || v == nil {
				return ""
			}
			return strings.TrimSpace(v.Value)
		}
	}
	v, err := s.variableStore.GetVariableByKey(ctx, key)
	if err != nil || v == nil {
		return ""
	}
	return strings.TrimSpace(v.Value)
}

// runWebSearch picks the configured providers: web_search_provider names one
// ("random" shuffles them), otherwise every provider with a key, in order.
func runWebSearch(ctx context.Context, client *http.Client, query string, limit int, lookup func(string) string) (string, error) {
	type candidate struct {
		provider webSearchProvider
		key      string
	}
	var candidates []candidate
	for _, p := range webSearchProviders {
		if key := lookup(p.keyVar); key != "" {
			candidates = append(candidates, candidate{p, key})
		}
	}
	if len(candidates) == 0 {
		return "", errors.New("web search is not configured: add one of the workspace variables tavily_api_key, exa_api_key, brave_api_key or firecrawl_api_key")
	}
	switch preferred := strings.ToLower(lookup(webSearchProviderVar)); preferred {
	case "", "auto":
	case "random":
		rand.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
	default:
		for i, c := range candidates {
			if c.provider.id == preferred {
				candidates[0], candidates[i] = candidates[i], candidates[0]
				break
			}
		}
	}

	ctx, cancel := context.WithTimeout(ctx, webSearchTimeout)
	defer cancel()
	now := time.Now()
	var errs []string
	cooling := 0
	for _, c := range candidates {
		if webSearchCoolingDown(c.provider.id, now) {
			cooling++
			continue
		}
		results, err := c.provider.search(ctx, client, c.key, query, limit)
		if err == nil {
			return formatWebSearch(c.provider.id, query, results), nil
		}
		var rle *webSearchRateLimitError
		if errors.As(err, &rle) {
			webSearchCoolDown(c.provider.id, rle.retryAfter)
		}
		errs = append(errs, fmt.Sprintf("%s: %v", c.provider.id, err))
		if ctx.Err() != nil {
			break
		}
	}
	if cooling == len(candidates) {
		return "", errors.New("every configured web search provider is cooling down after a rate limit; try again shortly")
	}
	return "", fmt.Errorf("web search failed: %s", strings.Join(errs, "; "))
}

func formatWebSearch(provider, query string, results []webSearchResult) string {
	if len(results) == 0 {
		return fmt.Sprintf("No results for %q (%s).", query, provider)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Search results for %q (%s):\n", query, provider)
	for i, r := range results {
		fmt.Fprintf(&b, "\n%d. %s\n   %s\n", i+1, firstNonEmpty(strings.TrimSpace(r.Title), r.URL), r.URL)
		if snippet := strings.Join(strings.Fields(r.Snippet), " "); snippet != "" {
			if len(snippet) > webSearchSnippetChars {
				snippet = truncateUTF8(snippet, webSearchSnippetChars) + "…"
			}
			fmt.Fprintf(&b, "   %s\n", snippet)
		}
	}
	return b.String()
}

// webSearchDo sends one request and decodes a JSON answer into out.
func webSearchDo(client *http.Client, req *http.Request, provider string, out any) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		retry := time.Duration(0)
		if secs, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil {
			retry = time.Duration(secs) * time.Second
		}
		return &webSearchRateLimitError{provider: provider, retryAfter: retry}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet := strings.TrimSpace(string(body[:min(len(body), 300)]))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func jsonPost(ctx context.Context, endpoint string, payload any) (*http.Request, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// Endpoints are variables so tests can point them at a local server.
var (
	tavilyEndpoint    = "https://api.tavily.com/search"
	exaEndpoint       = "https://api.exa.ai/search"
	braveEndpoint     = "https://api.search.brave.com/res/v1/web/search"
	firecrawlEndpoint = "https://api.firecrawl.dev/v1/search"
)

func searchTavily(ctx context.Context, client *http.Client, key, query string, limit int) ([]webSearchResult, error) {
	req, err := jsonPost(ctx, tavilyEndpoint, map[string]any{"query": query, "max_results": limit, "search_depth": "basic"})
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	var out struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := webSearchDo(client, req, "tavily", &out); err != nil {
		return nil, err
	}
	results := make([]webSearchResult, 0, len(out.Results))
	for _, r := range out.Results {
		results = append(results, webSearchResult{Title: r.Title, URL: r.URL, Snippet: r.Content})
	}
	return results, nil
}

func searchExa(ctx context.Context, client *http.Client, key, query string, limit int) ([]webSearchResult, error) {
	req, err := jsonPost(ctx, exaEndpoint, map[string]any{
		"query": query, "numResults": limit, "type": "auto",
		"contents": map[string]any{"highlights": map[string]any{"numSentences": 3}},
	})
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", key)
	var out struct {
		Results []struct {
			Title      string   `json:"title"`
			URL        string   `json:"url"`
			Highlights []string `json:"highlights"`
			Text       string   `json:"text"`
		} `json:"results"`
	}
	if err := webSearchDo(client, req, "exa", &out); err != nil {
		return nil, err
	}
	results := make([]webSearchResult, 0, len(out.Results))
	for _, r := range out.Results {
		snippet := strings.Join(r.Highlights, " … ")
		if snippet == "" {
			snippet = r.Text
		}
		results = append(results, webSearchResult{Title: r.Title, URL: r.URL, Snippet: snippet})
	}
	return results, nil
}

func searchBrave(ctx context.Context, client *http.Client, key, query string, limit int) ([]webSearchResult, error) {
	u, err := url.Parse(braveEndpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("count", strconv.Itoa(limit))
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", key)
	var out struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := webSearchDo(client, req, "brave", &out); err != nil {
		return nil, err
	}
	results := make([]webSearchResult, 0, len(out.Web.Results))
	for _, r := range out.Web.Results {
		results = append(results, webSearchResult{Title: stripTags(r.Title), URL: r.URL, Snippet: stripTags(r.Description)})
	}
	return results, nil
}

func searchFirecrawl(ctx context.Context, client *http.Client, key, query string, limit int) ([]webSearchResult, error) {
	req, err := jsonPost(ctx, firecrawlEndpoint, map[string]any{"query": query, "limit": limit})
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	var out struct {
		Data []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"data"`
	}
	if err := webSearchDo(client, req, "firecrawl", &out); err != nil {
		return nil, err
	}
	results := make([]webSearchResult, 0, len(out.Data))
	for _, r := range out.Data {
		results = append(results, webSearchResult{Title: r.Title, URL: r.URL, Snippet: r.Description})
	}
	return results, nil
}

// stripTags removes the <strong> highlighting some APIs put in snippets.
func stripTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>' && inTag:
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return b.String()
}
