package memory

import (
	"context"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/worldline-go/types"
)

// ─── Token Usage CRUD ───

func (m *Memory) RecordUsage(_ context.Context, tokenID, model string, usage service.Usage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	models, ok := m.tokenUsage[tokenID]
	if !ok {
		models = make(map[string]*service.TokenUsage)
		m.tokenUsage[tokenID] = models
	}

	existing, ok := models[model]
	if !ok {
		existing = &service.TokenUsage{
			TokenID: tokenID,
			Model:   model,
		}
		models[model] = existing
	}

	existing.PromptTokens += int64(usage.PromptTokens)
	existing.CompletionTokens += int64(usage.CompletionTokens)
	existing.TotalTokens += int64(usage.TotalTokens)
	existing.RequestCount++
	existing.LastRequestAt = types.NewTime(time.Now().UTC())

	return nil
}

func (m *Memory) GetTokenUsage(_ context.Context, tokenID string) ([]service.TokenUsage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	models, ok := m.tokenUsage[tokenID]
	if !ok {
		return nil, nil
	}

	result := make([]service.TokenUsage, 0, len(models))
	for _, u := range models {
		result = append(result, *u)
	}

	return result, nil
}

func (m *Memory) GetTokenTotalUsage(_ context.Context, tokenID string) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	models, ok := m.tokenUsage[tokenID]
	if !ok {
		return 0, nil
	}

	var total int64
	for _, u := range models {
		total += u.TotalTokens
	}

	return total, nil
}

func (m *Memory) ResetTokenUsage(_ context.Context, tokenID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.tokenUsage, tokenID)

	// Update last_reset_at on the token.
	t, ok := m.tokens[tokenID]
	if ok {
		t.LastResetAt = types.NewNull(types.NewTime(time.Now().UTC()))
		m.tokens[tokenID] = t
	}

	return nil
}
