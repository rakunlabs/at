package sqlite3

import (
	"context"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// TestCreateChatMessage_BumpsSessionUpdatedAt ensures inserting a new chat
// message updates the owning session's updated_at. The UI relies on this
// to surface recently-active sessions at the top of the sidebar.
func TestCreateChatMessage_BumpsSessionUpdatedAt(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, nil)

	// Create a session and capture its initial updated_at.
	session, err := store.CreateChatSession(ctx, service.ChatSession{
		AgentID:   "agent-xyz",
		Name:      "hello",
		CreatedBy: "tester",
		UpdatedBy: "tester",
	})
	if err != nil {
		t.Fatalf("CreateChatSession: %v", err)
	}
	initial := session.UpdatedAt
	if initial == "" {
		t.Fatalf("expected non-empty updated_at on new session")
	}

	// RFC3339 has second resolution. Sleep just over one second so the
	// post-insert updated_at can be strictly greater.
	time.Sleep(1100 * time.Millisecond)

	if _, err := store.CreateChatMessage(ctx, service.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Data:      service.ChatMessageData{Content: "hi"},
	}); err != nil {
		t.Fatalf("CreateChatMessage: %v", err)
	}

	refreshed, err := store.GetChatSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetChatSession: %v", err)
	}
	if refreshed == nil {
		t.Fatalf("session vanished after message insert")
	}
	if refreshed.UpdatedAt == initial {
		t.Errorf("session.updated_at was NOT bumped by CreateChatMessage: still %q", initial)
	}

	initialT, err1 := time.Parse(time.RFC3339, initial)
	laterT, err2 := time.Parse(time.RFC3339, refreshed.UpdatedAt)
	if err1 == nil && err2 == nil && !laterT.After(initialT) {
		t.Errorf("session.updated_at should be strictly after initial: initial=%s later=%s", initial, refreshed.UpdatedAt)
	}
}

// TestCreateChatMessages_BumpsSessionUpdatedAt verifies the bulk insert path
// also bumps updated_at exactly once per touched session.
func TestCreateChatMessages_BumpsSessionUpdatedAt(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, nil)

	s1, err := store.CreateChatSession(ctx, service.ChatSession{
		AgentID: "agent-1", Name: "s1", CreatedBy: "tester", UpdatedBy: "tester",
	})
	if err != nil {
		t.Fatalf("CreateChatSession s1: %v", err)
	}
	s2, err := store.CreateChatSession(ctx, service.ChatSession{
		AgentID: "agent-2", Name: "s2", CreatedBy: "tester", UpdatedBy: "tester",
	})
	if err != nil {
		t.Fatalf("CreateChatSession s2: %v", err)
	}

	initial1 := s1.UpdatedAt
	initial2 := s2.UpdatedAt

	time.Sleep(1100 * time.Millisecond)

	msgs := []service.ChatMessage{
		{SessionID: s1.ID, Role: "user", Data: service.ChatMessageData{Content: "a"}},
		{SessionID: s1.ID, Role: "assistant", Data: service.ChatMessageData{Content: "b"}},
		{SessionID: s2.ID, Role: "user", Data: service.ChatMessageData{Content: "c"}},
	}
	if err := store.CreateChatMessages(ctx, msgs); err != nil {
		t.Fatalf("CreateChatMessages: %v", err)
	}

	r1, err := store.GetChatSession(ctx, s1.ID)
	if err != nil {
		t.Fatalf("GetChatSession s1: %v", err)
	}
	r2, err := store.GetChatSession(ctx, s2.ID)
	if err != nil {
		t.Fatalf("GetChatSession s2: %v", err)
	}
	if r1.UpdatedAt == initial1 {
		t.Errorf("s1.updated_at not bumped after bulk insert")
	}
	if r2.UpdatedAt == initial2 {
		t.Errorf("s2.updated_at not bumped after bulk insert")
	}
}

// TestListChatMessages_LimitReturnsMostRecentInChronologicalOrder
// guards the loopgov-driven recency window behaviour: passing limit > 0
// must return the most recent N messages, but still in ascending
// chronological order so the agentic loop can resume seamlessly.
func TestListChatMessages_LimitReturnsMostRecentInChronologicalOrder(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, nil)

	session, err := store.CreateChatSession(ctx, service.ChatSession{
		AgentID: "a", Name: "x", CreatedBy: "tester", UpdatedBy: "tester",
	})
	if err != nil {
		t.Fatalf("CreateChatSession: %v", err)
	}

	// Insert 5 messages with deterministic content; sleep between
	// inserts so SQLite's RFC3339 created_at strictly orders them.
	for i := 0; i < 5; i++ {
		if _, err := store.CreateChatMessage(ctx, service.ChatMessage{
			SessionID: session.ID,
			Role:      "user",
			Data:      service.ChatMessageData{Content: []byte{'m', byte('0' + i)}},
		}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
		time.Sleep(1100 * time.Millisecond)
	}

	// limit 0 returns everything.
	all, err := store.ListChatMessages(ctx, session.ID, 0)
	if err != nil {
		t.Fatalf("ListChatMessages all: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("limit=0: got %d want 5", len(all))
	}

	// limit 3 returns the last three, in chronological order.
	last3, err := store.ListChatMessages(ctx, session.ID, 3)
	if err != nil {
		t.Fatalf("ListChatMessages limit=3: %v", err)
	}
	if len(last3) != 3 {
		t.Fatalf("limit=3: got %d want 3", len(last3))
	}
	// IDs should match the last three ULID-ordered IDs from `all`.
	for i := 0; i < 3; i++ {
		if last3[i].ID != all[2+i].ID {
			t.Errorf("limit=3 order mismatch at %d: got id %q want %q", i, last3[i].ID, all[2+i].ID)
		}
	}
}
