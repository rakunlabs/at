package memory

import (
	"context"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/worldline-go/types"
)

func (m *Memory) GetRAGState(_ context.Context, key string) (*service.RAGState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, ok := m.ragStates[key]
	if !ok {
		return nil, nil
	}

	return &state, nil
}

func (m *Memory) SetRAGState(_ context.Context, key string, value string) error {
	now := types.NewTime(time.Now().UTC())

	m.mu.Lock()
	defer m.mu.Unlock()

	state := service.RAGState{
		Key:       key,
		Value:     value,
		UpdatedAt: now,
	}

	m.ragStates[key] = state

	return nil
}
