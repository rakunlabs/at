package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Model Pricing Tool Executors ───
//
// These mirror the /api/v1/model-pricing* handlers so an MCP client can keep
// the effective price table current without the Pricing page. Prices are USD
// per one million tokens.

func (s *Server) execModelPricingList(ctx context.Context, args map[string]any) (string, error) {
	if s.agentBudgetStore == nil {
		return "", fmt.Errorf("pricing store not configured")
	}
	items, err := s.agentBudgetStore.ListModelPricing(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to list model pricing: %w", err)
	}

	providerKey := strings.TrimSpace(stringArg(args, "provider_key"))
	modelFilter := strings.ToLower(strings.TrimSpace(stringArg(args, "model")))
	out := make([]service.ModelPricing, 0, len(items))
	for _, item := range items {
		if providerKey != "" && item.ProviderKey != providerKey {
			continue
		}
		if modelFilter != "" && !strings.Contains(strings.ToLower(item.Model), modelFilter) {
			continue
		}
		out = append(out, item)
	}

	data, err := json.MarshalIndent(map[string]any{"items": out, "total": len(out), "unit": "USD per 1M tokens"}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal model pricing: %w", err)
	}
	return string(data), nil
}

// execModelPricingSet stores a manual price, exactly like the Pricing page's
// editor. Omitted prices keep the stored value, so one rate can be changed
// without restating the others.
func (s *Server) execModelPricingSet(ctx context.Context, args map[string]any) (string, error) {
	if s.agentBudgetStore == nil {
		return "", fmt.Errorf("pricing store not configured")
	}
	providerKey := strings.TrimSpace(stringArg(args, "provider_key"))
	model := strings.TrimSpace(stringArg(args, "model"))
	if providerKey == "" {
		return "", fmt.Errorf("provider_key is required")
	}
	if model == "" {
		return "", fmt.Errorf("model is required")
	}

	items, err := s.agentBudgetStore.ListModelPricing(ctx)
	if err != nil {
		return "", fmt.Errorf("read current model pricing: %w", err)
	}
	pricing := service.ModelPricing{ProviderKey: providerKey, Model: model}
	for _, item := range items {
		if item.ProviderKey == providerKey && item.Model == model {
			pricing = item
			break
		}
	}

	changed := false
	for _, field := range []struct {
		key    string
		target *float64
	}{
		{"input", &pricing.PromptPricePer1M},
		{"output", &pricing.CompletionPricePer1M},
		{"cache_read", &pricing.CacheReadPricePer1M},
		{"cache_write", &pricing.CacheWritePricePer1M},
	} {
		raw, ok := args[field.key]
		if !ok || raw == nil {
			continue
		}
		v, ok := raw.(float64)
		if !ok {
			return "", fmt.Errorf("%s must be a number", field.key)
		}
		if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return "", fmt.Errorf("%s must be a non-negative finite number", field.key)
		}
		*field.target = v
		changed = true
	}
	if !changed {
		return "", fmt.Errorf("set at least one of input, output, cache_read or cache_write")
	}

	// Source fields are left empty so the store keeps the synced reference
	// prices; a later model_pricing_reset restores them.
	pricing.ID = ""
	pricing.Source = ""
	pricing.LastSyncedAt = ""
	pricing.ManualOverride = true
	if err := s.agentBudgetStore.SetModelPricing(ctx, pricing); err != nil {
		return "", fmt.Errorf("set model pricing: %w", err)
	}

	data, err := json.MarshalIndent(map[string]any{
		"provider_key":    providerKey,
		"model":           model,
		"input":           pricing.PromptPricePer1M,
		"output":          pricing.CompletionPricePer1M,
		"cache_read":      pricing.CacheReadPricePer1M,
		"cache_write":     pricing.CacheWritePricePer1M,
		"manual_override": true,
		"unit":            "USD per 1M tokens",
	}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal model pricing: %w", err)
	}
	return string(data), nil
}

// modelPricingByKey resolves a stored row by ID or by provider_key + model.
func (s *Server) modelPricingByKey(ctx context.Context, args map[string]any) (service.ModelPricing, error) {
	id := strings.TrimSpace(stringArg(args, "id"))
	providerKey := strings.TrimSpace(stringArg(args, "provider_key"))
	model := strings.TrimSpace(stringArg(args, "model"))
	if id == "" && (providerKey == "" || model == "") {
		return service.ModelPricing{}, fmt.Errorf("id, or provider_key and model, is required")
	}
	items, err := s.agentBudgetStore.ListModelPricing(ctx)
	if err != nil {
		return service.ModelPricing{}, fmt.Errorf("read current model pricing: %w", err)
	}
	for _, item := range items {
		if (id != "" && item.ID == id) || (id == "" && item.ProviderKey == providerKey && item.Model == model) {
			return item, nil
		}
	}
	if id != "" {
		return service.ModelPricing{}, fmt.Errorf("model pricing %q not found", id)
	}
	return service.ModelPricing{}, fmt.Errorf("no pricing stored for %s/%s", providerKey, model)
}

func (s *Server) execModelPricingDelete(ctx context.Context, args map[string]any) (string, error) {
	if s.agentBudgetStore == nil {
		return "", fmt.Errorf("pricing store not configured")
	}
	item, err := s.modelPricingByKey(ctx, args)
	if err != nil {
		return "", err
	}
	if err := s.agentBudgetStore.DeleteModelPricing(ctx, item.ID); err != nil {
		return "", fmt.Errorf("delete model pricing: %w", err)
	}
	return fmt.Sprintf(`{"status":"deleted","id":%q,"provider_key":%q,"model":%q}`, item.ID, item.ProviderKey, item.Model), nil
}

func (s *Server) execModelPricingReset(ctx context.Context, args map[string]any) (string, error) {
	if s.agentBudgetStore == nil {
		return "", fmt.Errorf("pricing store not configured")
	}
	item, err := s.modelPricingByKey(ctx, args)
	if err != nil {
		return "", err
	}
	if item.Source == "" {
		return "", fmt.Errorf("%s/%s has no synced source price to reset to", item.ProviderKey, item.Model)
	}
	if err := s.agentBudgetStore.ResetModelPricingOverride(ctx, item.ID); err != nil {
		return "", fmt.Errorf("reset model pricing: %w", err)
	}
	return fmt.Sprintf(`{"status":"reset","id":%q,"provider_key":%q,"model":%q,"source":%q}`, item.ID, item.ProviderKey, item.Model, item.Source), nil
}

// execModelPricingSync previews a registered catalog source against the
// configured provider models and, with apply=true, stores the matches. It is
// the tool form of the Pricing page's Sync dialog.
func (s *Server) execModelPricingSync(ctx context.Context, args map[string]any) (string, error) {
	if s.agentBudgetStore == nil {
		return "", fmt.Errorf("pricing store not configured")
	}
	sourceName := strings.TrimSpace(stringArg(args, "source"))
	if sourceName == "" {
		sourceName = atPricingSource
	}
	source, ok := modelPricingSyncSourceByName(sourceName)
	if !ok {
		names := make([]string, 0, len(modelPricingSyncSources))
		for _, src := range modelPricingSyncSources {
			names = append(names, src.Source)
		}
		return "", fmt.Errorf("unsupported pricing source %q (supported: %s)", sourceName, strings.Join(names, ", "))
	}

	catalog, err := source.fetchCatalog(ctx)
	if err != nil {
		return "", fmt.Errorf("fetch pricing catalog: %w", err)
	}
	preview, err := s.buildCatalogPricingPreview(ctx, source.Source, catalog, nil)
	if err != nil {
		return "", fmt.Errorf("build pricing preview: %w", err)
	}

	providerKey := strings.TrimSpace(stringArg(args, "provider_key"))
	if providerKey != "" {
		filtered := preview[:0:0]
		for _, item := range preview {
			if item.ProviderKey == providerKey {
				filtered = append(filtered, item)
			}
		}
		preview = filtered
	}

	if !boolArg(args, "apply") {
		changes := make([]modelPricingSyncPreviewItem, 0, len(preview))
		for _, item := range preview {
			if item.Matched && item.Status != "current" {
				changes = append(changes, item)
			}
		}
		data, err := json.MarshalIndent(map[string]any{
			"source":    source.Source,
			"applied":   false,
			"changes":   changes,
			"total":     len(preview),
			"unmatched": countUnmatchedPricing(preview),
			"note":      "Dry run. Call again with apply=true to store matched prices; manual overrides are kept unless overwrite_overrides=true.",
		}, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal pricing preview: %w", err)
		}
		return string(data), nil
	}

	var selected map[string]struct{}
	if providerKey != "" {
		selected = make(map[string]struct{}, len(preview))
		for _, item := range preview {
			selected[item.ProviderKey+"\x00"+item.Model] = struct{}{}
		}
	}
	result := s.applyPricingSyncPreview(ctx, source.Source, preview, selected, boolArg(args, "overwrite_overrides"))
	data, err := json.MarshalIndent(map[string]any{
		"source":  source.Source,
		"applied": true,
		"result":  result,
	}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal pricing sync result: %w", err)
	}
	if len(result.Errors) > 0 && result.Applied == 0 {
		return "", fmt.Errorf("pricing sync failed: %s", strings.Join(result.Errors, "; "))
	}
	return string(data), nil
}

func countUnmatchedPricing(items []modelPricingSyncPreviewItem) int {
	n := 0
	for _, item := range items {
		if !item.Matched {
			n++
		}
	}
	return n
}
