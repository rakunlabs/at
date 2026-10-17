package server

import (
	"context"
	"encoding/json"
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
	// pricingKey is the key the provider's prices are filed under: the
	// personal reference for a personal provider, so a personal provider
	// named like an installation one is never priced as that one.
	pricingKey       string
	pricingWorkspace string
}

func (s *Server) providerForRoute(route *service.ProviderRoute, provider service.LLMProvider, fallbackUserID string) service.LLMProvider {
	if route == nil || provider == nil {
		return provider
	}
	pricingKey := route.Record.Key
	if route.Record.Reference != "" {
		pricingKey = route.Record.Reference
	} else if route.Record.OwnerUserID != "" && route.Record.ID != "" {
		pricingKey = service.PersonalProviderReference(route.Record.ID)
	}
	return &budgetedProvider{server: s, inner: provider, providerID: route.Record.ID, providerKey: route.Record.Key, actualModel: route.ActualModel, virtualProviderID: route.VirtualProviderID, fallbackUserID: fallbackUserID, pricingKey: pricingKey, pricingWorkspace: route.Record.WorkspaceID}
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
	workspaceID := p.pricingWorkspace
	if workspaceID == "" {
		workspaceID = pricingWorkspaceID(ctx)
	}
	pricing, err := p.server.modelPricingFor(ctx, workspaceID, p.pricingKey)
	if err != nil {
		return nil
	}
	value, ok := estimateUsageCost(pricing, p.pricingKey, model, p.pricingKey+"/"+model, usage)
	if !ok {
		return nil
	}
	return &value
}

type providerCallReservation struct {
	budget *service.ProviderBudgetReservation
	quota  *service.ProviderQuotaReservation
}

func (p *budgetedProvider) reserve(ctx context.Context, model string, usage service.Usage) (*providerCallReservation, error) {
	result := &providerCallReservation{}
	if quotas, ok := p.server.store.(service.ProviderQuotaStorer); ok && p.providerID != "" {
		var err error
		result.quota, err = quotas.ReserveProviderQuota(ctx, p.providerID, model, int64(usage.PromptTokens)+int64(usage.CompletionTokens))
		if err != nil {
			return nil, err
		}
	}
	store, ok := p.server.store.(service.ProviderBudgetStorer)
	if !ok || p.providerID == "" {
		return result, nil
	}
	estimate := p.estimate(ctx, model, usage)
	reservation, err := store.ReserveProviderBudget(ctx, p.resources(), providerBudgetUserID(ctx, p.fallbackUserID), estimate)
	if err != nil {
		p.settleQuota(ctx, result.quota, 0, false)
		switch {
		case errors.Is(err, service.ErrProviderBudgetExceeded):
			return nil, fmt.Errorf("%w: the provider's spending limit has been reached", err)
		case errors.Is(err, service.ErrProviderUserLimit):
			return nil, fmt.Errorf("%w: your allowance for this provider has been used", err)
		case errors.Is(err, service.ErrProviderUserBlocked):
			return nil, fmt.Errorf("%w: access was disabled by the provider owner", err)
		case errors.Is(err, service.ErrProviderPricingRequired):
			return nil, fmt.Errorf("%w: configure pricing for %s/%s before using its enforced budget", err, p.pricingKey, model)
		default:
			return nil, fmt.Errorf("reserve provider budget: %w", err)
		}
	}
	result.budget = reservation
	return result, nil
}

func (p *budgetedProvider) settleQuota(ctx context.Context, quota *service.ProviderQuotaReservation, tokens int64, attempted bool) {
	if quota == nil || quota.ID == "" {
		return
	}
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if store, ok := p.server.store.(service.ProviderQuotaStorer); ok {
		if err := store.SettleProviderQuota(settleCtx, quota.ID, tokens, attempted); err != nil {
			slog.Error("settle provider quota failed", "provider", p.providerKey, "error", err)
		}
	}
}

func (p *budgetedProvider) settle(ctx context.Context, reservation *providerCallReservation, model string, usage service.Usage) {
	if reservation == nil {
		return
	}
	p.settleQuota(ctx, reservation.quota, int64(usage.PromptTokens)+int64(usage.CompletionTokens), true)
	if reservation.budget == nil {
		return
	}
	actual := 0.0
	if cost := p.estimate(context.WithoutCancel(ctx), model, usage); cost != nil {
		actual = *cost
	}
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if store, ok := p.server.store.(service.ProviderBudgetStorer); ok {
		if err := store.SettleProviderBudget(settleCtx, reservation.budget.ID, actual); err != nil {
			slog.Error("settle provider budget failed", "provider", p.providerKey, "reservation_id", reservation.budget.ID, "error", err)
		}
	}
}

func (p *budgetedProvider) resolvedModel(requested string) string {
	if p.actualModel != "" {
		return p.actualModel
	}
	return requested
}

// Providers disagree on the precedence of these two wire fields. Reserve the
// larger positive allowance so neither precedence can under-reserve. Zero is
// not a zero-output promise (several adapters use their default then).
func providerOutputAllowance(opts *service.ChatOptions) int {
	allowance := 0
	if opts != nil {
		if opts.MaxTokens != nil {
			allowance = max(allowance, *opts.MaxTokens)
		}
		if opts.MaxCompletionTokens != nil {
			allowance = max(allowance, *opts.MaxCompletionTokens)
		}
	}
	if allowance <= 0 {
		return 32000
	}
	return allowance
}

func (p *budgetedProvider) admitQuotaOverrides(ctx context.Context, r *providerCallReservation, model string, opts *service.ChatOptions) error {
	if r == nil || r.quota == nil || opts == nil || len(opts.ExtraBody) == 0 {
		return nil
	}
	_, changesModel := opts.ExtraBody["model"]
	if !r.quota.GuardOverrides || !changesModel && !r.quota.TokenLimited {
		return nil
	}
	p.settleQuota(ctx, r.quota, 0, false)
	r.quota = nil
	p.settle(ctx, r, model, service.Usage{})
	return fmt.Errorf("extra_body cannot change quota-sensitive model/input/output fields; omit extra_body when using token quotas: %w", service.ErrUnsupportedOperation)
}

func (p *budgetedProvider) Chat(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (*service.LLMResponse, error) {
	model = p.resolvedModel(model)
	maxOutput := providerOutputAllowance(opts)
	estimated := service.Usage{PromptTokens: common.EstimateInputTokens("", messages, tools), CompletionTokens: maxOutput}
	reservation, err := p.reserve(ctx, model, estimated)
	if err != nil {
		return nil, err
	}
	if err := p.admitQuotaOverrides(ctx, reservation, model, opts); err != nil {
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
	maxOutput := providerOutputAllowance(opts)
	estimated := service.Usage{PromptTokens: common.EstimateInputTokens("", messages, tools), CompletionTokens: maxOutput}
	reservation, err := p.reserve(ctx, model, estimated)
	if err != nil {
		return nil, nil, err
	}
	if err := p.admitQuotaOverrides(ctx, reservation, model, opts); err != nil {
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
				// Token quota and money budget have different fallback semantics:
				// quota keeps the full conservative reservation when usage is lost.
				if reservation != nil {
					p.settleQuota(ctx, reservation.quota, 0, true)
					reservation.quota = nil
				}
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

func (p *budgetedProvider) Decide(ctx context.Context, req service.DecisionRequest) (*service.DecisionResponse, error) {
	inner, ok := p.inner.(service.DecisionProvider)
	if !ok {
		return nil, service.ErrUnsupportedOperation
	}
	req.Model = p.resolvedModel(req.Model)
	input := 0
	if state, err := json.Marshal(req.State); err == nil {
		input += len(state)
	}
	if questions, err := json.Marshal(req.Questions); err == nil {
		input += len(questions)
	}
	reservation, err := p.reserve(ctx, req.Model, service.Usage{PromptTokens: max(1, input/4)})
	if err != nil {
		return nil, err
	}
	resp, err := inner.Decide(ctx, req)
	if err != nil {
		p.settle(ctx, reservation, req.Model, service.Usage{})
		return nil, err
	}
	p.settle(ctx, reservation, req.Model, resp.Usage)
	return resp, nil
}
