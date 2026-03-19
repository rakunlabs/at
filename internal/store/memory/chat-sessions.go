package memory

import (
	"context"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ─── Chat Session CRUD ───

func (m *Memory) ListChatSessions(_ context.Context, q *query.Query) (*service.ListResult[service.ChatSession], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.ChatSession, 0, len(m.chatSessions))
	for _, s := range m.chatSessions {
		result = append(result, s)
	}

	slices.SortFunc(result, func(a, b service.ChatSession) int {
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

func (m *Memory) GetChatSession(_ context.Context, id string) (*service.ChatSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.chatSessions[id]
	if !ok {
		return nil, nil
	}

	return &s, nil
}

func (m *Memory) GetChatSessionByTaskID(_ context.Context, taskID string) (*service.ChatSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, s := range m.chatSessions {
		if s.TaskID == taskID && s.TaskID != "" {
			return &s, nil
		}
	}

	return nil, nil
}

func (m *Memory) GetChatSessionByPlatform(_ context.Context, platform, platformUserID, platformChannelID string) (*service.ChatSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, s := range m.chatSessions {
		if s.Config.Platform == platform && s.Config.PlatformUserID == platformUserID && s.Config.PlatformChannelID == platformChannelID {
			return &s, nil
		}
	}

	return nil, nil
}

func (m *Memory) CreateChatSession(_ context.Context, session service.ChatSession) (*service.ChatSession, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.ChatSession{
		ID:             id,
		AgentID:        session.AgentID,
		TaskID:         session.TaskID,
		OrganizationID: session.OrganizationID,
		Name:           session.Name,
		Config:         session.Config,
		CreatedAt:      now,
		UpdatedAt:      now,
		CreatedBy:      session.CreatedBy,
		UpdatedBy:      session.UpdatedBy,
	}

	m.mu.Lock()
	m.chatSessions[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateChatSession(_ context.Context, id string, session service.ChatSession) (*service.ChatSession, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.chatSessions[id]
	if !ok {
		return nil, nil
	}

	existing.Name = session.Name
	existing.Config = session.Config
	existing.UpdatedAt = now
	existing.UpdatedBy = session.UpdatedBy
	if session.AgentID != "" {
		existing.AgentID = session.AgentID
	}
	if session.TaskID != "" {
		existing.TaskID = session.TaskID
	}
	if session.OrganizationID != "" {
		existing.OrganizationID = session.OrganizationID
	}

	m.chatSessions[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteChatSession(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.chatSessions, id)
	// Also delete messages for this session.
	delete(m.chatMessages, id)
	m.mu.Unlock()

	return nil
}

func (m *Memory) ListChatMessages(_ context.Context, sessionID string) ([]service.ChatMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	msgs := m.chatMessages[sessionID]
	result := make([]service.ChatMessage, len(msgs))
	copy(result, msgs)

	return result, nil
}

func (m *Memory) CreateChatMessage(_ context.Context, msg service.ChatMessage) (*service.ChatMessage, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.ChatMessage{
		ID:        id,
		SessionID: msg.SessionID,
		Role:      msg.Role,
		Data:      msg.Data,
		CreatedAt: now,
	}

	m.mu.Lock()
	m.chatMessages[msg.SessionID] = append(m.chatMessages[msg.SessionID], rec)
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) CreateChatMessages(_ context.Context, msgs []service.ChatMessage) error {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, msg := range msgs {
		id := ulid.Make().String()
		rec := service.ChatMessage{
			ID:        id,
			SessionID: msg.SessionID,
			Role:      msg.Role,
			Data:      msg.Data,
			CreatedAt: now,
		}
		m.chatMessages[msg.SessionID] = append(m.chatMessages[msg.SessionID], rec)
	}

	return nil
}

func (m *Memory) DeleteChatMessages(_ context.Context, sessionID string) error {
	m.mu.Lock()
	delete(m.chatMessages, sessionID)
	m.mu.Unlock()
	return nil
}
