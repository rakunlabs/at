package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/rakunlabs/at/internal/service"
)

type executionProviderMetadata interface {
	ExecutionProviderDefaultModel(context.Context, string) (string, error)
}
type executionProviderCacheKey struct {
	s                   *Server
	workspace, provider string
}
type executionProviderCacheEntry struct {
	digest   [32]byte
	provider service.LLMProvider
}

var executionProviders = struct {
	sync.Mutex
	entries map[executionProviderCacheKey]executionProviderCacheEntry
}{entries: map[executionProviderCacheKey]executionProviderCacheEntry{}}

func (s *Server) runtimeProviderLookup(ctx context.Context, key string) (service.LLMProvider, string, error) {
	info, err := s.getExecutionProviderInfo(ctx, key)
	if err != nil {
		return nil, "", err
	}
	return info.provider, info.defaultModel, nil
}

// getExecutionProviderInfo never selects a globally named credential before
// workspace/model authorization. The cache is workspace+immutable provider ID,
// and the current configuration fingerprint is checked on every model call.
func (s *Server) getExecutionProviderInfo(ctx context.Context, key string) (ProviderInfo, error) {
	store, scoped := s.store.(service.WorkspaceProviderStorer)
	if !scoped {
		info, ok := s.getProviderInfo(key)
		if !ok {
			return ProviderInfo{}, fmt.Errorf("provider %q not found", key)
		}
		return info, nil // explicit compatibility/test stores retain their registry
	}
	metadata, ok := s.store.(executionProviderMetadata)
	if !ok {
		return ProviderInfo{}, service.ErrExecutionDenied
	}
	model, err := metadata.ExecutionProviderDefaultModel(ctx, key)
	if err != nil {
		return ProviderInfo{}, err
	}
	provider := service.ScopedExecutionProviderWithResolver(key, func(ctx context.Context, model string) (service.LLMProvider, error) {
		if model == "" {
			return nil, fmt.Errorf("runtime model must be explicit")
		}
		record, err := store.ResolveWorkspaceProviderForUse(ctx, key, model)
		if err != nil {
			return nil, err
		}
		if record == nil || s.providerFactory == nil {
			return nil, service.ErrExecutionDenied
		}
		_, _, ok := service.ExecutionFromContext(ctx)
		if !ok {
			return nil, service.ErrExecutionDenied
		}
		data, err := json.Marshal(record.Config)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(data)
		// Authorization above is per calling workspace. The transport belongs to
		// the provider's owner: shared providers must reuse one OAuth token source
		// rather than racing the same rotating refresh token across workspaces.
		cacheKey := executionProviderCacheKey{s: s, workspace: record.WorkspaceID, provider: record.ID}
		executionProviders.Lock()
		defer executionProviders.Unlock()
		if entry, ok := executionProviders.entries[cacheKey]; ok && entry.digest == digest {
			return entry.provider, nil
		}
		created, err := s.providerFactory(record.Config)
		if err != nil {
			return nil, err
		}
		s.wireClaudeOAuthCallback(record.Key, created, record.WorkspaceID)
		if len(executionProviders.entries) >= 512 {
			for old := range executionProviders.entries {
				delete(executionProviders.entries, old)
				break
			}
		}
		executionProviders.entries[cacheKey] = executionProviderCacheEntry{digest: digest, provider: created}
		return created, nil
	})
	return ProviderInfo{provider: provider, defaultModel: model}, nil
}
