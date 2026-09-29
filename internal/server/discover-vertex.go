package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service/llm/gcp"
	"github.com/rakunlabs/at/internal/service/llm/openai"
)

// vertexDiscoveryMaxPages bounds the Model Garden walk. The catalog is a few
// hundred entries today; the bound only guards against a cursor that never ends.
const vertexDiscoveryMaxPages = 50

// vertexPublisherModel is the subset of a Model Garden PublisherModel read here.
type vertexPublisherModel struct {
	Name string `json:"name"` // publishers/google/models/<id>
}

// listVertexPublisherModels lists Google's publisher models from Model Garden
// (GET /v1beta1/publishers/google/models) with the provider's own credentials:
// the stored service-account key, or the server's ADC when there is none.
// Vertex has no per-project model listing for first-party models, so this is
// the catalog both `vertex` and `vertex-gemini` can call.
func listVertexPublisherModels(ctx context.Context, cfg config.LLMConfig) ([]string, error) {
	exchangeClient, err := openai.ProxyHTTPClient(cfg.Proxy, cfg.InsecureSkipVerify)
	if err != nil {
		return nil, fmt.Errorf("proxy client for the Google token exchange: %w", err)
	}
	ts, err := gcp.TokenSource(ctx, cfg.CredentialsJSON, exchangeClient)
	if err != nil {
		return nil, err
	}
	token, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("fetch Google access token: %w", err)
	}

	host, err := vertexDiscoveryHost(cfg)
	if err != nil {
		return nil, err
	}

	client, err := clientForConfig(cfg)
	if err != nil {
		return nil, err
	}

	var models []string
	seen := map[string]bool{}
	pageToken := ""
	for page := 0; page < vertexDiscoveryMaxPages; page++ {
		listURL, err := url.Parse(host + "/v1beta1/publishers/google/models")
		if err != nil {
			return nil, fmt.Errorf("invalid Vertex host: %w", err)
		}
		query := listURL.Query()
		query.Set("pageSize", "100")
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		listURL.RawQuery = query.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token.AccessToken)

		resp, err := client.HTTP.Do(req)
		if err != nil {
			return nil, fmt.Errorf("request failed: %w", err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("Vertex AI model list returned %d: %s", resp.StatusCode, truncate(string(body), 300))
		}

		var result struct {
			PublisherModels []vertexPublisherModel `json:"publisherModels"`
			NextPageToken   string                 `json:"nextPageToken"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}
		for _, m := range result.PublisherModels {
			id := m.Name
			if idx := strings.LastIndex(id, "/models/"); idx >= 0 {
				id = id[idx+len("/models/"):]
			}
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			models = append(models, id)
		}

		if result.NextPageToken == "" || result.NextPageToken == pageToken {
			return models, nil
		}
		pageToken = result.NextPageToken
	}

	return models, nil
}

// vertexDiscoveryHost is the scheme+host of the configured base URL, or the
// regional host derived from vertex_region when no base URL is set.
func vertexDiscoveryHost(cfg config.LLMConfig) (string, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return gcp.RegionalHost(cfg.ExtraHeaders["vertex_region"]), nil
	}
	parsed, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid base_url %q", cfg.BaseURL)
	}

	return parsed.Scheme + "://" + parsed.Host, nil
}

func isVertexEmbeddingModel(id string) bool {
	return strings.Contains(strings.ToLower(id), "embedding")
}

// discoverVertexModels returns the Gemini chat models Vertex serves.
// Model Garden also lists image, video, speech and partner models that neither
// Vertex adapter can call, so only the Gemini family is kept.
func discoverVertexModels(ctx context.Context, cfg config.LLMConfig) ([]string, error) {
	all, err := listVertexPublisherModels(ctx, cfg)
	if err != nil {
		return nil, err
	}
	var models []string
	for _, id := range all {
		lower := strings.ToLower(id)
		if strings.HasPrefix(lower, "gemini") && !isVertexEmbeddingModel(lower) {
			models = append(models, id)
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("Vertex AI returned no Gemini models; check that the Vertex AI API is enabled and the service account has the Vertex AI User role")
	}

	return models, nil
}

// discoverVertexEmbeddingModels returns the embedding models Vertex serves.
func discoverVertexEmbeddingModels(ctx context.Context, cfg config.LLMConfig) ([]string, error) {
	all, err := listVertexPublisherModels(ctx, cfg)
	if err != nil {
		return nil, err
	}
	var models []string
	for _, id := range all {
		if isVertexEmbeddingModel(id) {
			models = append(models, id)
		}
	}

	return models, nil
}
