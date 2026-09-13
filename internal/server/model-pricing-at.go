package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/rakunlabs/at/pricing"
)

const atPricingURL = pricing.URL
const atPricingMaxBytes = 8 << 20

func fetchATModelPricing(ctx context.Context) ([]modelPricingSourceItem, error) {
	return fetchATModelPricingURL(ctx, &http.Client{Timeout: 20 * time.Second}, atPricingURL)
}

func fetchATModelPricingURL(ctx context.Context, client *http.Client, catalogURL string) ([]modelPricingSourceItem, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, catalogURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create AT pricing request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch AT pricing catalog: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch AT pricing catalog: status %d; ensure pricing/index.json is published on GitHub main", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, atPricingMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read AT pricing catalog: %w", err)
	}
	if len(data) > atPricingMaxBytes {
		return nil, fmt.Errorf("AT pricing catalog exceeds %d bytes", atPricingMaxBytes)
	}
	return parseATModelPricing(bytes.NewReader(data))
}

func parseATModelPricing(r io.Reader) ([]modelPricingSourceItem, error) {
	c, err := pricing.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("parse AT pricing catalog: %w", err)
	}
	var items []modelPricingSourceItem
	for _, p := range c.Providers {
		for _, m := range p.Models {
			if !m.Complete() {
				continue
			}
			items = append(items, modelPricingSourceItem{
				Provider: p.Provider, Model: m.Model, URL: m.SourceURL,
				ExactPricing: true, ManualOnly: m.ManualOnly, Aliases: m.Aliases,
				Notes: m.Notes, VerifiedAt: m.VerifiedAt,
				PromptPricePer1M: *m.Input, CompletionPricePer1M: *m.Output,
				CacheReadPricePer1M: *m.CacheRead, CacheWritePricePer1M: *m.CacheWrite,
			})
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("AT pricing catalog contains no complete prices")
	}
	return items, nil
}

func matchATModelPricing(catalog []modelPricingSourceItem, providerType, model string) (modelPricingSourceMatch, string, float64, bool) {
	// The transport type is not always the billing vendor. Only openai (the
	// generic compatible adapter) can fall back to a unique cross-vendor ID.
	provider := strings.ToLower(providerType)
	switch provider {
	case "antropic":
		provider = "anthropic"
	case "gemini":
		provider = "google"
	case "vertex", "vertex-gemini":
		provider = "google-vertex"
	case "x.ai":
		provider = "xai"
	}
	var matches []modelPricingSourceItem
	for _, item := range catalog {
		if item.ManualOnly || (item.Model != model && !slices.Contains(item.Aliases, model)) {
			continue
		}
		if item.Provider == provider {
			return sourceItemMatch(item), "provider_model", 1, true
		}
		if provider == "openai" {
			matches = append(matches, item)
		}
	}
	if len(matches) == 1 {
		return sourceItemMatch(matches[0]), "model", 0.8, true
	}
	return modelPricingSourceMatch{}, "", 0, false
}
