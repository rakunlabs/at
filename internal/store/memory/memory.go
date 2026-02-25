package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/worldline-go/types"
)

// Memory is an in-memory implementation of the store interfaces.
// Data does not survive process restarts.
type Memory struct {
	mu           sync.RWMutex
	providers    map[string]service.ProviderRecord // key -> record
	tokens       map[string]service.APIToken       // id -> token
	tokensByHash map[string]string                 // hash -> id
	workflows    map[string]service.Workflow       // id -> workflow
	triggers     map[string]service.Trigger        // id -> trigger
}

func New() *Memory {
	slog.Info("using in-memory store (data will not persist across restarts)")

	return &Memory{
		providers:    make(map[string]service.ProviderRecord),
		tokens:       make(map[string]service.APIToken),
		tokensByHash: make(map[string]string),
		workflows:    make(map[string]service.Workflow),
		triggers:     make(map[string]service.Trigger),
	}
}

func (m *Memory) Close() {}

// ─── Provider CRUD ───

func (m *Memory) ListProviders(_ context.Context) ([]service.ProviderRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.ProviderRecord, 0, len(m.providers))
	for _, rec := range m.providers {
		result = append(result, rec)
	}

	slices.SortFunc(result, func(a, b service.ProviderRecord) int {
		if a.Key < b.Key {
			return -1
		}
		if a.Key > b.Key {
			return 1
		}
		return 0
	})

	return result, nil
}

func (m *Memory) GetProvider(_ context.Context, key string) (*service.ProviderRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rec, ok := m.providers[key]
	if !ok {
		return nil, nil
	}

	return &rec, nil
}

func (m *Memory) CreateProvider(_ context.Context, key string, cfg config.LLMConfig) (*service.ProviderRecord, error) {
	// Round-trip through JSON to match DB behavior (normalize zero values).
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	var normalized config.LLMConfig
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.ProviderRecord{
		ID:        id,
		Key:       key,
		Config:    normalized,
		CreatedAt: now,
		UpdatedAt: now,
	}

	m.mu.Lock()
	m.providers[key] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateProvider(_ context.Context, key string, cfg config.LLMConfig) (*service.ProviderRecord, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	var normalized config.LLMConfig
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.providers[key]
	if !ok {
		return nil, nil
	}

	existing.Config = normalized
	existing.UpdatedAt = now
	m.providers[key] = existing

	return &existing, nil
}

func (m *Memory) DeleteProvider(_ context.Context, key string) error {
	m.mu.Lock()
	delete(m.providers, key)
	m.mu.Unlock()

	return nil
}

// ─── API Token CRUD ───

func (m *Memory) ListAPITokens(_ context.Context) ([]service.APIToken, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.APIToken, 0, len(m.tokens))
	for _, t := range m.tokens {
		result = append(result, t)
	}

	// Sort by created_at descending (newest first), matching DB behavior.
	slices.SortFunc(result, func(a, b service.APIToken) int {
		ta := a.CreatedAt.Time
		tb := b.CreatedAt.Time
		if ta.After(tb) {
			return -1
		}
		if ta.Before(tb) {
			return 1
		}
		return 0
	})

	return result, nil
}

func (m *Memory) GetAPITokenByHash(_ context.Context, hash string) (*service.APIToken, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	id, ok := m.tokensByHash[hash]
	if !ok {
		return nil, nil
	}

	t, ok := m.tokens[id]
	if !ok {
		return nil, nil
	}

	return &t, nil
}

func (m *Memory) CreateAPIToken(_ context.Context, token service.APIToken, tokenHash string) (*service.APIToken, error) {
	id := ulid.Make().String()
	now := types.NewTime(time.Now().UTC())

	token.ID = id
	token.CreatedAt = now

	m.mu.Lock()
	m.tokens[id] = token
	m.tokensByHash[tokenHash] = id
	m.mu.Unlock()

	return &token, nil
}

func (m *Memory) UpdateAPIToken(_ context.Context, id string, token service.APIToken) (*service.APIToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.tokens[id]
	if !ok {
		return nil, fmt.Errorf("api_token %q not found", id)
	}

	existing.Name = token.Name
	existing.AllowedProviders = token.AllowedProviders
	existing.AllowedModels = token.AllowedModels
	existing.ExpiresAt = token.ExpiresAt
	m.tokens[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteAPIToken(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Remove hash index entry.
	for hash, tokenID := range m.tokensByHash {
		if tokenID == id {
			delete(m.tokensByHash, hash)
			break
		}
	}

	delete(m.tokens, id)

	return nil
}

func (m *Memory) UpdateLastUsed(_ context.Context, id string) error {
	now := types.NewTime(time.Now().UTC())

	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.tokens[id]
	if !ok {
		return nil
	}

	t.LastUsedAt = types.NewNull(now)
	m.tokens[id] = t

	return nil
}

// ─── Workflow CRUD ───

func (m *Memory) ListWorkflows(_ context.Context) ([]service.Workflow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.Workflow, 0, len(m.workflows))
	for _, w := range m.workflows {
		result = append(result, w)
	}

	slices.SortFunc(result, func(a, b service.Workflow) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})

	return result, nil
}

func (m *Memory) GetWorkflow(_ context.Context, id string) (*service.Workflow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	w, ok := m.workflows[id]
	if !ok {
		return nil, nil
	}

	return &w, nil
}

func (m *Memory) CreateWorkflow(_ context.Context, w service.Workflow) (*service.Workflow, error) {
	// Round-trip through JSON to normalize.
	raw, err := json.Marshal(w.Graph)
	if err != nil {
		return nil, fmt.Errorf("marshal graph: %w", err)
	}
	var normalized service.WorkflowGraph
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, fmt.Errorf("unmarshal graph: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.Workflow{
		ID:          id,
		Name:        w.Name,
		Description: w.Description,
		Graph:       normalized,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	m.mu.Lock()
	m.workflows[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateWorkflow(_ context.Context, id string, w service.Workflow) (*service.Workflow, error) {
	raw, err := json.Marshal(w.Graph)
	if err != nil {
		return nil, fmt.Errorf("marshal graph: %w", err)
	}
	var normalized service.WorkflowGraph
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, fmt.Errorf("unmarshal graph: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.workflows[id]
	if !ok {
		return nil, nil
	}

	existing.Name = w.Name
	existing.Description = w.Description
	existing.Graph = normalized
	existing.UpdatedAt = now
	m.workflows[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteWorkflow(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.workflows, id)
	m.mu.Unlock()

	return nil
}

// ─── Trigger CRUD ───

func (m *Memory) ListTriggers(_ context.Context, workflowID string) ([]service.Trigger, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.Trigger
	for _, t := range m.triggers {
		if t.WorkflowID == workflowID {
			result = append(result, t)
		}
	}

	slices.SortFunc(result, func(a, b service.Trigger) int {
		if a.CreatedAt < b.CreatedAt {
			return -1
		}
		if a.CreatedAt > b.CreatedAt {
			return 1
		}
		return 0
	})

	return result, nil
}

func (m *Memory) GetTrigger(_ context.Context, id string) (*service.Trigger, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, ok := m.triggers[id]
	if !ok {
		return nil, nil
	}

	return &t, nil
}

func (m *Memory) CreateTrigger(_ context.Context, t service.Trigger) (*service.Trigger, error) {
	// Round-trip through JSON to normalize config.
	raw, err := json.Marshal(t.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.Trigger{
		ID:         id,
		WorkflowID: t.WorkflowID,
		Type:       t.Type,
		Config:     normalized,
		Enabled:    t.Enabled,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	m.mu.Lock()
	m.triggers[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateTrigger(_ context.Context, id string, t service.Trigger) (*service.Trigger, error) {
	raw, err := json.Marshal(t.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.triggers[id]
	if !ok {
		return nil, nil
	}

	existing.Type = t.Type
	existing.Config = normalized
	existing.Enabled = t.Enabled
	existing.UpdatedAt = now
	m.triggers[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteTrigger(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.triggers, id)
	m.mu.Unlock()

	return nil
}

func (m *Memory) ListEnabledCronTriggers(_ context.Context) ([]service.Trigger, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.Trigger
	for _, t := range m.triggers {
		if t.Type == "cron" && t.Enabled {
			result = append(result, t)
		}
	}

	return result, nil
}
