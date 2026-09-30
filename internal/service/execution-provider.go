package service

import "context"

type executionProvider struct {
	provider LLMProvider
	key      string
	resolve  func(context.Context, string) (LLMProvider, error)
}

func ScopedExecutionProviderWithResolver(key string, resolve func(context.Context, string) (LLMProvider, error)) LLMProvider {
	return &executionProvider{key: key, resolve: resolve}
}

// ScopedExecutionProvider revalidates at each Chat, not just initial lookup.
// It intentionally exposes no native Proxy interface with arbitrary host paths.
func ScopedExecutionProvider(provider LLMProvider, key string) LLMProvider {
	return &executionProvider{provider: provider, key: key}
}

func (p *executionProvider) Chat(ctx context.Context, model string, messages []Message, tools []Tool, opts *ChatOptions) (*LLMResponse, error) {
	provider, err := p.current(ctx, model)
	if err != nil {
		return nil, err
	}
	return provider.Chat(ctx, model, messages, tools, opts)
}

func (p *executionProvider) current(ctx context.Context, model string) (LLMProvider, error) {
	if err := CheckExecution(ctx, ExecutionAction{Kind: "resource", Name: "providers.use", ResourceID: p.key, Model: model}); err != nil {
		return nil, err
	}
	if p.resolve != nil {
		return p.resolve(ctx, model)
	}
	if p.provider == nil {
		return nil, ErrExecutionDenied
	}
	return p.provider, nil
}

func (p *executionProvider) GenerateImage(ctx context.Context, req ImageGenerateRequest) (*ImageResponse, error) {
	current, err := p.current(ctx, req.Model)
	if err != nil {
		return nil, err
	}
	provider, ok := current.(ImageProvider)
	if !ok {
		return nil, ErrUnsupportedOperation
	}
	return provider.GenerateImage(ctx, req)
}
func (p *executionProvider) GenerateAudio(ctx context.Context, req AudioGenerateRequest) (*AudioResponse, error) {
	current, err := p.current(ctx, req.Model)
	if err != nil {
		return nil, err
	}
	provider, ok := current.(AudioProvider)
	if !ok {
		return nil, ErrUnsupportedOperation
	}
	return provider.GenerateAudio(ctx, req)
}
func (p *executionProvider) TranscribeAudio(ctx context.Context, req AudioTranscribeRequest) (*AudioTranscribeResponse, error) {
	current, err := p.current(ctx, req.Model)
	if err != nil {
		return nil, err
	}
	provider, ok := current.(AudioProvider)
	if !ok {
		return nil, ErrUnsupportedOperation
	}
	return provider.TranscribeAudio(ctx, req)
}
func (p *executionProvider) CreateEmbedding(ctx context.Context, req EmbeddingRequest) (*EmbeddingResponse, error) {
	current, err := p.current(ctx, req.Model)
	if err != nil {
		return nil, err
	}
	provider, ok := current.(EmbeddingProvider)
	if !ok {
		return nil, ErrUnsupportedOperation
	}
	return provider.CreateEmbedding(ctx, req)
}
func (p *executionProvider) Moderate(ctx context.Context, req ModerationRequest) (*ModerationResponse, error) {
	current, err := p.current(ctx, req.Model)
	if err != nil {
		return nil, err
	}
	provider, ok := current.(ModerationProvider)
	if !ok {
		return nil, ErrUnsupportedOperation
	}
	return provider.Moderate(ctx, req)
}
func (p *executionProvider) Rerank(ctx context.Context, req RerankRequest) (*RerankResponse, error) {
	current, err := p.current(ctx, req.Model)
	if err != nil {
		return nil, err
	}
	provider, ok := current.(RerankProvider)
	if !ok {
		return nil, ErrUnsupportedOperation
	}
	return provider.Rerank(ctx, req)
}
func (p *executionProvider) Decide(ctx context.Context, req DecisionRequest) (*DecisionResponse, error) {
	current, err := p.current(ctx, req.Model)
	if err != nil {
		return nil, err
	}
	provider, ok := current.(DecisionProvider)
	if !ok {
		return nil, ErrUnsupportedOperation
	}
	return provider.Decide(ctx, req)
}

// StreamChat opens a streaming completion through the same per-call admission
// as Chat. It deliberately is not ChatStream: satisfying LLMStreamProvider
// would also require Proxy, which scoped execution must never expose.
// ErrUnsupportedOperation means the caller should fall back to Chat.
func (p *executionProvider) StreamChat(ctx context.Context, model string, messages []Message, tools []Tool, opts *ChatOptions) (<-chan StreamChunk, error) {
	current, err := p.current(ctx, model)
	if err != nil {
		return nil, err
	}
	streamer, ok := current.(LLMStreamProvider)
	if !ok {
		return nil, ErrUnsupportedOperation
	}
	chunks, _, err := streamer.ChatStream(ctx, model, messages, tools, opts)
	return chunks, err
}

// ExecutionStreamer is implemented by scoped execution providers whose
// underlying adapter can stream.
type ExecutionStreamer interface {
	StreamChat(ctx context.Context, model string, messages []Message, tools []Tool, opts *ChatOptions) (<-chan StreamChunk, error)
}
