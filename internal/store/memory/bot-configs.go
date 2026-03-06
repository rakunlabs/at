package memory

import (
	"context"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ─── Bot Config CRUD ───

func (m *Memory) ListBotConfigs(_ context.Context, q *query.Query) (*service.ListResult[service.BotConfig], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.BotConfig, 0, len(m.botConfigs))
	for _, b := range m.botConfigs {
		result = append(result, b)
	}

	slices.SortFunc(result, func(a, b service.BotConfig) int {
		if a.CreatedAt < b.CreatedAt {
			return -1
		}
		if a.CreatedAt > b.CreatedAt {
			return 1
		}
		return 0
	})

	return paginate(result, q), nil
}

func (m *Memory) GetBotConfig(_ context.Context, id string) (*service.BotConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	b, ok := m.botConfigs[id]
	if !ok {
		return nil, nil
	}

	return &b, nil
}

func (m *Memory) CreateBotConfig(_ context.Context, bot service.BotConfig) (*service.BotConfig, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	if bot.ChannelAgents == nil {
		bot.ChannelAgents = map[string]string{}
	}

	rec := service.BotConfig{
		ID:             id,
		Platform:       bot.Platform,
		Name:           bot.Name,
		Token:          bot.Token,
		DefaultAgentID: bot.DefaultAgentID,
		ChannelAgents:  bot.ChannelAgents,
		Enabled:        bot.Enabled,
		CreatedAt:      now,
		UpdatedAt:      now,
		CreatedBy:      bot.CreatedBy,
		UpdatedBy:      bot.UpdatedBy,
	}

	m.mu.Lock()
	m.botConfigs[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateBotConfig(_ context.Context, id string, bot service.BotConfig) (*service.BotConfig, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	if bot.ChannelAgents == nil {
		bot.ChannelAgents = map[string]string{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.botConfigs[id]
	if !ok {
		return nil, nil
	}

	existing.Platform = bot.Platform
	existing.Name = bot.Name
	existing.Token = bot.Token
	existing.DefaultAgentID = bot.DefaultAgentID
	existing.ChannelAgents = bot.ChannelAgents
	existing.Enabled = bot.Enabled
	existing.UpdatedAt = now
	existing.UpdatedBy = bot.UpdatedBy
	m.botConfigs[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteBotConfig(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.botConfigs, id)
	m.mu.Unlock()

	return nil
}
