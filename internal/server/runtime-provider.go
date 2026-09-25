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
		var route *service.ProviderRoute
		var err error
		if routes, ok := s.store.(service.ProviderRouteStorer); ok {
			route, err = routes.ResolveWorkspaceProviderRoute(ctx, key, model)
		} else {
			var record *service.ProviderRecord
			record, err = store.ResolveWorkspaceProviderForUse(ctx, key, model)
			if record != nil {
				route = &service.ProviderRoute{Record: *record, ActualModel: model}
			}
		}
		if err != nil {
			return nil, err
		}
		if route == nil || s.providerFactory == nil {
			return nil, service.ErrExecutionDenied
		}
		_, _, ok := service.ExecutionFromContext(ctx)
		if !ok {
			return nil, service.ErrExecutionDenied
		}
		created, err := s.cachedWorkspaceProvider(&route.Record)
		if err != nil {
			return nil, err
		}
		return s.providerForRoute(route, created, ""), nil
	})
	return ProviderInfo{provider: provider, defaultModel: model}, nil
}

// Callers must admit the record before using this cache. Discovery and model
// execution share the owning workspace's transport, including pending OAuth
// persistence retries; no key-only global provider lookup is permitted here.
func (s *Server) cachedWorkspaceProvider(record *service.ProviderRecord) (service.LLMProvider, error) {
	if s.providerFactory == nil {
		return nil, fmt.Errorf("provider factory unavailable")
	}
	data, err := json.Marshal([]any{record.Key, record.Config})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
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
	authReference := record.Key
	if record.Reference != "" {
		authReference = record.Reference
	}
	s.wireClaudeOAuthCallback(authReference, created, record.WorkspaceID)
	s.wireChatGPTOAuthCallback(authReference, created, record.WorkspaceID)
	if len(executionProviders.entries) >= 512 {
		for old := range executionProviders.entries {
			delete(executionProviders.entries, old)
			break
		}
	}
	executionProviders.entries[cacheKey] = executionProviderCacheEntry{digest: digest, provider: created}
	return created, nil
}
