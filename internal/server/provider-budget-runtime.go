package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/common"
)

type budgetedProvider struct {
	server            *Server
	inner             service.LLMProvider
	providerID        string
	providerKey       string
	actualModel       string
	virtualProviderID string
	fallbackUserID    string
}

func (s *Server) providerForRoute(route *service.ProviderRoute, provider service.LLMProvider, fallbackUserID string) service.LLMProvider {
	if route == nil || provider == nil {
		return provider
	}
	return &budgetedProvider{server: s, inner: provider, providerID: route.Record.ID, providerKey: route.Record.Key, actualModel: route.ActualModel, virtualProviderID: route.VirtualProviderID, fallbackUserID: fallbackUserID}
}

func providerBudgetUserID(ctx context.Context, fallback string) string {
	if a, ok := service.AccessPrincipalFromContext(ctx); ok && a.UserID != "" {
		return a.UserID
	}
	if actor, ok := ctx.Value(userUsageKey{}).(userUsageAttribution); ok && actor.userID != "" {
		return actor.userID
	}
	return fallback
}

func (p *budgetedProvider) resources() []service.BudgetResource {
	resources := []service.BudgetResource{{Kind: service.BudgetResourceProvider, ID: p.providerID}}
	if p.virtualProviderID != "" {
		resources = append(resources, service.BudgetResource{Kind: service.BudgetResourceVirtualProvider, ID: p.virtualProviderID})
	}
	return resources
}

func (p *budgetedProvider) estimate(ctx context.Context, model string, usage service.Usage) *float64 {
	if p.server.agentBudgetStore == nil {
		return nil
	}
	pricing, err := p.server.agentBudgetStore.ListModelPricing(ctx)
	if err != nil {
		return nil
	}
	value, ok := estimateUsageCost(pricing, p.providerKey, model, p.providerKey+"/"+model, usage)
	if !ok {
		return nil
	}
	return &value
}

func (p *budgetedProvider) reserve(ctx context.Context, model string, usage service.Usage) (*service.ProviderBudgetReservation, error) {
	store, ok := p.server.store.(service.ProviderBudgetStorer)
	if !ok || p.providerID == "" {
		return nil, nil
	}
	estimate := p.estimate(ctx, model, usage)
	reservation, err := store.ReserveProviderBudget(ctx, p.resources(), providerBudgetUserID(ctx, p.fallbackUserID), estimate)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrProviderBudgetExceeded):
			return nil, fmt.Errorf("%w: the provider's spending limit has been reached", err)
		case errors.Is(err, service.ErrProviderUserLimit):
			return nil, fmt.Errorf("%w: your allowance for this provider has been used", err)
		case errors.Is(err, service.ErrProviderUserBlocked):
			return nil, fmt.Errorf("%w: access was disabled by the provider owner", err)
		case errors.Is(err, service.ErrProviderPricingRequired):
			return nil, fmt.Errorf("%w: configure pricing for %s/%s before using its enforced budget", err, p.providerKey, model)
		default:
			return nil, fmt.Errorf("reserve provider budget: %w", err)
		}
	}
	return reservation, nil
}

func (p *budgetedProvider) settle(ctx context.Context, reservation *service.ProviderBudgetReservation, model string, usage service.Usage) {
	if reservation == nil {
		return
	}
	actual := 0.0
	if cost := p.estimate(context.WithoutCancel(ctx), model, usage); cost != nil {
		actual = *cost
	}
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if store, ok := p.server.store.(service.ProviderBudgetStorer); ok {
		if err := store.SettleProviderBudget(settleCtx, reservation.ID, actual); err != nil {
			slog.Error("settle provider budget failed", "provider", p.providerKey, "reservation_id", reservation.ID, "error", err)
		}
	}
}

func (p *budgetedProvider) resolvedModel(requested string) string {
	if p.actualModel != "" {
		return p.actualModel
	}
	return requested
}

func (p *budgetedProvider) Chat(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (*service.LLMResponse, error) {
	model = p.resolvedModel(model)
	maxOutput := 32000
	if opts != nil && opts.MaxTokens != nil && *opts.MaxTokens >= 0 {
		maxOutput = *opts.MaxTokens
	} else if opts != nil && opts.MaxCompletionTokens != nil && *opts.MaxCompletionTokens >= 0 {
		maxOutput = *opts.MaxCompletionTokens
	}
	estimated := service.Usage{PromptTokens: common.EstimateInputTokens("", messages, tools), CompletionTokens: maxOutput}
	reservation, err := p.reserve(ctx, model, estimated)
	if err != nil {
		return nil, err
	}
	resp, err := p.inner.Chat(ctx, model, messages, tools, opts)
	if err != nil {
		p.settle(ctx, reservation, model, service.Usage{})
		return nil, err
	}
	p.settle(ctx, reservation, model, resp.Usage)
	return resp, nil
}

func (p *budgetedProvider) ChatStream(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (<-chan service.StreamChunk, http.Header, error) {
	streamer, ok := p.inner.(service.LLMStreamProvider)
	if !ok {
		return nil, nil, service.ErrUnsupportedOperation
	}
	model = p.resolvedModel(model)
	maxOutput := 32000
	if opts != nil && opts.MaxTokens != nil && *opts.MaxTokens >= 0 {
		maxOutput = *opts.MaxTokens
	} else if opts != nil && opts.MaxCompletionTokens != nil && *opts.MaxCompletionTokens >= 0 {
		maxOutput = *opts.MaxCompletionTokens
	}
	estimated := service.Usage{PromptTokens: common.EstimateInputTokens("", messages, tools), CompletionTokens: maxOutput}
	reservation, err := p.reserve(ctx, model, estimated)
	if err != nil {
		return nil, nil, err
	}
	upstream, headers, err := streamer.ChatStream(ctx, model, messages, tools, opts)
	if err != nil {
		p.settle(ctx, reservation, model, service.Usage{})
		return nil, nil, err
	}
	out := make(chan service.StreamChunk)
	go func() {
		defer close(out)
		var usage service.Usage
		// A stream that never reports usage still consumed its input; charge
		// the input estimate rather than nothing so it cannot bypass the budget.
		finish := func() {
			if usage.PromptTokens == 0 && usage.CompletionTokens == 0 {
				usage = service.Usage{PromptTokens: estimated.PromptTokens}
			}
			p.settle(ctx, reservation, model, usage)
		}
		for chunk := range upstream {
			if chunk.Usage != nil {
				usage = *chunk.Usage
			}
			select {
			case out <- chunk:
			case <-ctx.Done():
				finish()
				return
			}
		}
		finish()
	}()
	return out, headers, nil
}

func (p *budgetedProvider) Proxy(w http.ResponseWriter, r *http.Request, path string) error {
	streamer, ok := p.inner.(service.LLMStreamProvider)
	if !ok {
		return service.ErrUnsupportedOperation
	}
	reservation, err := p.reserve(r.Context(), p.actualModel, service.Usage{})
	if err != nil {
		return err
	}
	// Settle from the usage the adapter parsed out of the relayed body; the
	// gateway's own observer still runs, so passthrough metering is unchanged.
	var usage service.Usage
	ctx := r.Context()
	outer, hasOuter := service.ProxyObserverFromContext(ctx)
	ctx = service.ContextWithProxyObserver(ctx, func(obs service.ProxyObservation) {
		usage = obs.Usage
		if hasOuter {
			outer(obs)
		}
	})
	err = streamer.Proxy(w, r.WithContext(ctx), path)
	p.settle(ctx, reservation, p.actualModel, usage)
	return err
}

func (p *budgetedProvider) CreateEmbedding(ctx context.Context, req service.EmbeddingRequest) (*service.EmbeddingResponse, error) {
	inner, ok := p.inner.(service.EmbeddingProvider)
	if !ok {
		return nil, service.ErrUnsupportedOperation
	}
	req.Model = p.resolvedModel(req.Model)
	input := 0
	for _, text := range req.Input {
		input += max(1, len(text)/4)
	}
	reservation, err := p.reserve(ctx, req.Model, service.Usage{PromptTokens: input})
	if err != nil {
		return nil, err
	}
	resp, err := inner.CreateEmbedding(ctx, req)
	if err != nil {
		p.settle(ctx, reservation, req.Model, service.Usage{})
		return nil, err
	}
	p.settle(ctx, reservation, req.Model, resp.Usage)
	return resp, nil
}

func (p *budgetedProvider) GenerateImage(ctx context.Context, req service.ImageGenerateRequest) (*service.ImageResponse, error) {
	inner, ok := p.inner.(service.ImageProvider)
	if !ok {
		return nil, service.ErrUnsupportedOperation
	}
	req.Model = p.resolvedModel(req.Model)
	reservation, err := p.reserve(ctx, req.Model, service.Usage{PromptTokens: max(1, len(req.Prompt)/4)})
	if err != nil {
		return nil, err
	}
	resp, err := inner.GenerateImage(ctx, req)
	if err != nil {
		p.settle(ctx, reservation, req.Model, service.Usage{})
		return nil, err
	}
	p.settle(ctx, reservation, req.Model, resp.Usage)
	return resp, nil
}

func (p *budgetedProvider) GenerateAudio(ctx context.Context, req service.AudioGenerateRequest) (*service.AudioResponse, error) {
	inner, ok := p.inner.(service.AudioProvider)
	if !ok {
		return nil, service.ErrUnsupportedOperation
	}
	req.Model = p.resolvedModel(req.Model)
	reservation, err := p.reserve(ctx, req.Model, service.Usage{PromptTokens: max(1, len(req.Input)/4)})
	if err != nil {
		return nil, err
	}
	resp, err := inner.GenerateAudio(ctx, req)
	p.settle(ctx, reservation, req.Model, service.Usage{})
	return resp, err
}

func (p *budgetedProvider) TranscribeAudio(ctx context.Context, req service.AudioTranscribeRequest) (*service.AudioTranscribeResponse, error) {
	inner, ok := p.inner.(service.AudioProvider)
	if !ok {
		return nil, service.ErrUnsupportedOperation
	}
	req.Model = p.resolvedModel(req.Model)
	reservation, err := p.reserve(ctx, req.Model, service.Usage{})
	if err != nil {
		return nil, err
	}
	resp, err := inner.TranscribeAudio(ctx, req)
	p.settle(ctx, reservation, req.Model, service.Usage{})
	return resp, err
}

func (p *budgetedProvider) Moderate(ctx context.Context, req service.ModerationRequest) (*service.ModerationResponse, error) {
	inner, ok := p.inner.(service.ModerationProvider)
	if !ok {
		return nil, service.ErrUnsupportedOperation
	}
	req.Model = p.resolvedModel(req.Model)
	input := 0
	for _, text := range req.Input {
		input += max(1, len(text)/4)
	}
	reservation, err := p.reserve(ctx, req.Model, service.Usage{PromptTokens: input})
	if err != nil {
		return nil, err
	}
	resp, err := inner.Moderate(ctx, req)
	p.settle(ctx, reservation, req.Model, service.Usage{})
	return resp, err
}

func (p *budgetedProvider) Rerank(ctx context.Context, req service.RerankRequest) (*service.RerankResponse, error) {
	inner, ok := p.inner.(service.RerankProvider)
	if !ok {
		return nil, service.ErrUnsupportedOperation
	}
	req.Model = p.resolvedModel(req.Model)
	input := len(req.Query)
	for _, document := range req.Documents {
		input += len(document)
	}
	reservation, err := p.reserve(ctx, req.Model, service.Usage{PromptTokens: max(1, input/4)})
	if err != nil {
		return nil, err
	}
	resp, err := inner.Rerank(ctx, req)
	if err != nil {
		p.settle(ctx, reservation, req.Model, service.Usage{})
		return nil, err
	}
	p.settle(ctx, reservation, req.Model, resp.Usage)
	return resp, nil
}
