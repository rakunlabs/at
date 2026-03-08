package memory

import (
	"context"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
)

func (m *Memory) ListCommentsByTask(_ context.Context, taskID string) ([]service.IssueComment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.IssueComment
	for _, c := range m.issueComments {
		if c.TaskID == taskID {
			result = append(result, c)
		}
	}

	// Sort by created_at ascending (oldest first — chronological).
	slices.SortFunc(result, func(a, b service.IssueComment) int {
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

func (m *Memory) GetComment(_ context.Context, id string) (*service.IssueComment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.issueComments[id]
	if !ok {
		return nil, nil
	}

	return &c, nil
}

func (m *Memory) CreateComment(_ context.Context, comment service.IssueComment) (*service.IssueComment, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.IssueComment{
		ID:         id,
		TaskID:     comment.TaskID,
		AuthorType: comment.AuthorType,
		AuthorID:   comment.AuthorID,
		Body:       comment.Body,
		ParentID:   comment.ParentID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	m.mu.Lock()
	m.issueComments[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateComment(_ context.Context, id string, comment service.IssueComment) (*service.IssueComment, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.issueComments[id]
	if !ok {
		return nil, nil
	}

	existing.Body = comment.Body
	existing.UpdatedAt = now
	m.issueComments[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteComment(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.issueComments, id)
	m.mu.Unlock()

	return nil
}
