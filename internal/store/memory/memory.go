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
	mu               sync.RWMutex
	providers        map[string]service.ProviderRecord    // key -> record
	tokens           map[string]service.APIToken          // id -> token
	tokensByHash     map[string]string                    // hash -> id
	workflows        map[string]service.Workflow          // id -> workflow
	workflowVersions map[string][]service.WorkflowVersion // workflow_id -> versions (sorted by version desc)
	triggers         map[string]service.Trigger           // id -> trigger
	skills           map[string]service.Skill             // id -> skill
	variables        map[string]service.Variable          // id -> variable
	nodeConfigs      map[string]service.NodeConfig        // id -> node config
}

func New() *Memory {
	slog.Info("using in-memory store (data will not persist across restarts)")

	return &Memory{
		providers:        make(map[string]service.ProviderRecord),
		tokens:           make(map[string]service.APIToken),
		tokensByHash:     make(map[string]string),
		workflows:        make(map[string]service.Workflow),
		workflowVersions: make(map[string][]service.WorkflowVersion),
		triggers:         make(map[string]service.Trigger),
		skills:           make(map[string]service.Skill),
		variables:        make(map[string]service.Variable),
		nodeConfigs:      make(map[string]service.NodeConfig),
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

func (m *Memory) CreateProvider(_ context.Context, record service.ProviderRecord) (*service.ProviderRecord, error) {
	// Round-trip through JSON to match DB behavior (normalize zero values).
	raw, err := json.Marshal(record.Config)
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
		Key:       record.Key,
		Config:    normalized,
		CreatedAt: now,
		UpdatedAt: now,
		CreatedBy: record.CreatedBy,
		UpdatedBy: record.UpdatedBy,
	}

	m.mu.Lock()
	m.providers[record.Key] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateProvider(_ context.Context, key string, record service.ProviderRecord) (*service.ProviderRecord, error) {
	raw, err := json.Marshal(record.Config)
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
	existing.UpdatedBy = record.UpdatedBy
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
	existing.AllowedWebhooks = token.AllowedWebhooks
	existing.ExpiresAt = token.ExpiresAt
	existing.UpdatedBy = token.UpdatedBy
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
		CreatedBy:   w.CreatedBy,
		UpdatedBy:   w.UpdatedBy,
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
	existing.UpdatedBy = w.UpdatedBy
	m.workflows[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteWorkflow(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.workflows, id)
	delete(m.workflowVersions, id)
	m.mu.Unlock()

	return nil
}

// ─── Workflow Version CRUD ───

func (m *Memory) ListWorkflowVersions(_ context.Context, workflowID string) ([]service.WorkflowVersion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	versions := m.workflowVersions[workflowID]
	// Return a copy sorted by version desc (already stored in desc order).
	result := make([]service.WorkflowVersion, len(versions))
	copy(result, versions)

	return result, nil
}

func (m *Memory) GetWorkflowVersion(_ context.Context, workflowID string, version int) (*service.WorkflowVersion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, v := range m.workflowVersions[workflowID] {
		if v.Version == version {
			return &v, nil
		}
	}

	return nil, nil
}

func (m *Memory) CreateWorkflowVersion(_ context.Context, v service.WorkflowVersion) (*service.WorkflowVersion, error) {
	// Round-trip through JSON to normalize the graph.
	raw, err := json.Marshal(v.Graph)
	if err != nil {
		return nil, fmt.Errorf("marshal graph: %w", err)
	}
	var normalized service.WorkflowGraph
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, fmt.Errorf("unmarshal graph: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Compute next version.
	versions := m.workflowVersions[v.WorkflowID]
	maxVersion := 0
	for _, existing := range versions {
		if existing.Version > maxVersion {
			maxVersion = existing.Version
		}
	}
	nextVersion := maxVersion + 1

	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.WorkflowVersion{
		ID:          id,
		WorkflowID:  v.WorkflowID,
		Version:     nextVersion,
		Name:        v.Name,
		Description: v.Description,
		Graph:       normalized,
		CreatedAt:   now,
		CreatedBy:   v.CreatedBy,
	}

	// Prepend (we store desc order).
	m.workflowVersions[v.WorkflowID] = append([]service.WorkflowVersion{rec}, versions...)

	return &rec, nil
}

func (m *Memory) SetActiveVersion(_ context.Context, workflowID string, version int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	wf, ok := m.workflows[workflowID]
	if !ok {
		return fmt.Errorf("workflow %q not found", workflowID)
	}

	wf.ActiveVersion = &version
	m.workflows[workflowID] = wf

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

func (m *Memory) GetTriggerByAlias(_ context.Context, alias string) (*service.Trigger, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, t := range m.triggers {
		if t.Alias == alias {
			return &t, nil
		}
	}

	return nil, nil
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
		Alias:      t.Alias,
		Public:     t.Public,
		Enabled:    t.Enabled,
		CreatedAt:  now,
		UpdatedAt:  now,
		CreatedBy:  t.CreatedBy,
		UpdatedBy:  t.UpdatedBy,
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
	existing.Alias = t.Alias
	existing.Public = t.Public
	existing.Enabled = t.Enabled
	existing.UpdatedAt = now
	existing.UpdatedBy = t.UpdatedBy
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

// ─── Skill CRUD ───

func (m *Memory) ListSkills(_ context.Context) ([]service.Skill, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.Skill, 0, len(m.skills))
	for _, sk := range m.skills {
		result = append(result, sk)
	}

	slices.SortFunc(result, func(a, b service.Skill) int {
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

func (m *Memory) GetSkill(_ context.Context, id string) (*service.Skill, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sk, ok := m.skills[id]
	if !ok {
		return nil, nil
	}

	return &sk, nil
}

func (m *Memory) GetSkillByName(_ context.Context, name string) (*service.Skill, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, sk := range m.skills {
		if sk.Name == name {
			return &sk, nil
		}
	}

	return nil, nil
}

func (m *Memory) CreateSkill(_ context.Context, sk service.Skill) (*service.Skill, error) {
	// Round-trip through JSON to normalize.
	raw, err := json.Marshal(sk.Tools)
	if err != nil {
		return nil, fmt.Errorf("marshal tools: %w", err)
	}
	var normalized []service.Tool
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, fmt.Errorf("unmarshal tools: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.Skill{
		ID:           id,
		Name:         sk.Name,
		Description:  sk.Description,
		SystemPrompt: sk.SystemPrompt,
		Tools:        normalized,
		CreatedAt:    now,
		UpdatedAt:    now,
		CreatedBy:    sk.CreatedBy,
		UpdatedBy:    sk.UpdatedBy,
	}

	m.mu.Lock()
	m.skills[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateSkill(_ context.Context, id string, sk service.Skill) (*service.Skill, error) {
	raw, err := json.Marshal(sk.Tools)
	if err != nil {
		return nil, fmt.Errorf("marshal tools: %w", err)
	}
	var normalized []service.Tool
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, fmt.Errorf("unmarshal tools: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.skills[id]
	if !ok {
		return nil, nil
	}

	existing.Name = sk.Name
	existing.Description = sk.Description
	existing.SystemPrompt = sk.SystemPrompt
	existing.Tools = normalized
	existing.UpdatedAt = now
	existing.UpdatedBy = sk.UpdatedBy
	m.skills[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteSkill(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.skills, id)
	m.mu.Unlock()

	return nil
}

// ─── Variable CRUD ───

func (m *Memory) ListVariables(_ context.Context) ([]service.Variable, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.Variable, 0, len(m.variables))
	for _, v := range m.variables {
		result = append(result, v)
	}

	slices.SortFunc(result, func(a, b service.Variable) int {
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

func (m *Memory) GetVariable(_ context.Context, id string) (*service.Variable, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	v, ok := m.variables[id]
	if !ok {
		return nil, nil
	}

	return &v, nil
}

func (m *Memory) GetVariableByKey(_ context.Context, key string) (*service.Variable, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, v := range m.variables {
		if v.Key == key {
			return &v, nil
		}
	}

	return nil, nil
}

func (m *Memory) CreateVariable(_ context.Context, v service.Variable) (*service.Variable, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.Variable{
		ID:          id,
		Key:         v.Key,
		Value:       v.Value,
		Description: v.Description,
		Secret:      v.Secret,
		CreatedAt:   now,
		UpdatedAt:   now,
		CreatedBy:   v.CreatedBy,
		UpdatedBy:   v.UpdatedBy,
	}

	m.mu.Lock()
	m.variables[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateVariable(_ context.Context, id string, v service.Variable) (*service.Variable, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.variables[id]
	if !ok {
		return nil, nil
	}

	existing.Key = v.Key
	existing.Value = v.Value
	existing.Description = v.Description
	existing.Secret = v.Secret
	existing.UpdatedAt = now
	existing.UpdatedBy = v.UpdatedBy
	m.variables[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteVariable(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.variables, id)
	m.mu.Unlock()

	return nil
}

// ─── Node Config CRUD ───

func (m *Memory) ListNodeConfigs(_ context.Context) ([]service.NodeConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.NodeConfig, 0, len(m.nodeConfigs))
	for _, nc := range m.nodeConfigs {
		result = append(result, nc)
	}

	slices.SortFunc(result, func(a, b service.NodeConfig) int {
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

func (m *Memory) ListNodeConfigsByType(_ context.Context, configType string) ([]service.NodeConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.NodeConfig
	for _, nc := range m.nodeConfigs {
		if nc.Type == configType {
			result = append(result, nc)
		}
	}

	slices.SortFunc(result, func(a, b service.NodeConfig) int {
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

func (m *Memory) GetNodeConfig(_ context.Context, id string) (*service.NodeConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	nc, ok := m.nodeConfigs[id]
	if !ok {
		return nil, nil
	}

	return &nc, nil
}

func (m *Memory) CreateNodeConfig(_ context.Context, nc service.NodeConfig) (*service.NodeConfig, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.NodeConfig{
		ID:        id,
		Name:      nc.Name,
		Type:      nc.Type,
		Data:      nc.Data,
		CreatedAt: now,
		UpdatedAt: now,
		CreatedBy: nc.CreatedBy,
		UpdatedBy: nc.UpdatedBy,
	}

	m.mu.Lock()
	m.nodeConfigs[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateNodeConfig(_ context.Context, id string, nc service.NodeConfig) (*service.NodeConfig, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.nodeConfigs[id]
	if !ok {
		return nil, nil
	}

	existing.Name = nc.Name
	existing.Type = nc.Type
	existing.Data = nc.Data
	existing.UpdatedAt = now
	existing.UpdatedBy = nc.UpdatedBy
	m.nodeConfigs[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteNodeConfig(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.nodeConfigs, id)
	m.mu.Unlock()

	return nil
}
