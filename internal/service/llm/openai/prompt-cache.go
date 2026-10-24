package openai

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// promptCacheRouting says how an OpenAI-compatible host accepts a cache
// routing key. Hosts route a conversation to the server holding its cached
// prefix only when told which conversation a request belongs to; without it
// consecutive turns land on cold servers and are billed uncached.
type promptCacheRouting int

const (
	promptCacheNone   promptCacheRouting = iota
	promptCacheBody                      // prompt_cache_key in the request body
	promptCacheHeader                    // x-grok-conv-id header
)

// Only hosts documented to accept the key get one. Strict OpenAI-compatible
// servers reject unknown fields with 400/422 (Groq, Cerebras, NVIDIA NIM,
// Azure AI Foundry all refuse prompt_cache_key), so this is an allowlist.
func promptCacheRoutingFor(baseURL string) promptCacheRouting {
	u, err := url.Parse(baseURL)
	if err != nil {
		return promptCacheNone
	}
	switch strings.ToLower(u.Hostname()) {
	case "api.openai.com", "api.mistral.ai", "inference.do-ai.run":
		return promptCacheBody
	case "api.x.ai":
		// xAI's Chat Completions takes the key as a header; the body field is
		// documented only for its Responses API.
		return promptCacheHeader
	default:
		return promptCacheNone
	}
}

// applyPromptCacheKey adds the conversation identity as a cache routing key
// for hosts that accept one. An explicit prompt_cache_key (extra_body) wins.
func applyPromptCacheKey(ctx context.Context, baseURL string, reqBody map[string]any, header http.Header) {
	routing := promptCacheRoutingFor(baseURL)
	if routing == promptCacheNone {
		return
	}
	if key, _ := reqBody["prompt_cache_key"].(string); key != "" {
		return
	}
	key := service.UpstreamSessionFromContext(ctx)
	if key == "" {
		return
	}
	switch routing {
	case promptCacheBody:
		reqBody["prompt_cache_key"] = key
	case promptCacheHeader:
		if header.Get("x-grok-conv-id") == "" {
			header.Set("x-grok-conv-id", key)
		}
	}
}
