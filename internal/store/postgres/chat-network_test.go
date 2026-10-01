package postgres

import (
	"errors"
	"sync"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestPlaygroundAppendNetworkRetry(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := t.Context()
	c := playgroundConversation(t, p, "owner")
	input := []service.PlaygroundMessage{{ClientID: "stable", Role: "user", Data: map[string]any{"content": "question", "number": 1}}}
	first, err := p.AppendPlaygroundMessages(ctx, "owner", c.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a committed append whose HTTP response was lost, including
	// simultaneous retries arriving on different database connections.
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			retry, err := p.AppendPlaygroundMessages(ctx, "owner", c.ID, input)
			if err != nil || len(retry) != 1 || retry[0].ID != first[0].ID {
				t.Errorf("retry: %+v %v", retry, err)
			}
		})
	}
	wg.Wait()
	all, err := p.ListPlaygroundMessages(ctx, "owner", c.ID, "", 200)
	if err != nil || len(all) != 1 {
		t.Fatalf("duplicate append: %+v %v", all, err)
	}
	input[0].Data["content"] = "different"
	if _, err := p.AppendPlaygroundMessages(ctx, "owner", c.ID, input); !errors.Is(err, service.ErrPlaygroundConflict) {
		t.Fatalf("changed payload admitted: %v", err)
	}
	if _, err := p.AppendPlaygroundMessages(ctx, "foreign", c.ID, input); !errors.Is(err, service.ErrPlaygroundNotFound) {
		t.Fatalf("foreign retry admitted: %v", err)
	}
	// Distinct entries in a batch preserve order, replayed entries don't
	// allocate new sequence numbers, and repeated IDs within a batch dedupe.
	input[0].Data["content"] = "question"
	next := service.PlaygroundMessage{ClientID: "next", Role: "assistant", Data: map[string]any{"content": "answer"}}
	batch, err := p.AppendPlaygroundMessages(ctx, "owner", c.ID, append(input, next, next))
	if err != nil || len(batch) != 3 || batch[0].Sequence != 1 || batch[1].Sequence != 2 || batch[2].ID != batch[1].ID {
		t.Fatalf("batch: %+v %v", batch, err)
	}
	refs, err := p.GetPlaygroundMessageReferences(ctx, "owner", c.ID, []string{batch[1].ID, first[0].ID})
	if err != nil || refs[0].ID != batch[1].ID || refs[1].ID != first[0].ID {
		t.Fatalf("references: %+v %v", refs, err)
	}
	if _, err := p.GetPlaygroundMessageReferences(ctx, "foreign", c.ID, []string{first[0].ID}); !errors.Is(err, service.ErrPlaygroundNotFound) {
		t.Fatal(err)
	}
	if err := p.TruncatePlaygroundMessages(ctx, "owner", c.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetPlaygroundMessageReferences(ctx, "owner", c.ID, []string{batch[1].ID}); !errors.Is(err, service.ErrPlaygroundConflict) {
		t.Fatalf("stale reference admitted: %v", err)
	}
}

func TestChatMessagesIncrementalPolling(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := t.Context()
	session, err := p.CreateChatSession(ctx, service.ChatSession{AgentID: "a", Name: "network"})
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if _, err := p.CreateChatMessage(ctx, service.ChatMessage{SessionID: session.ID, Role: "user", Data: service.ChatMessageData{Content: "hello"}}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := p.ListChatMessages(ctx, session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	page, err := p.ListChatMessagesAfter(ctx, session.ID, all[1].ID, 2)
	if err != nil || len(page) != 2 || page[0].ID != all[2].ID || page[1].ID != all[3].ID {
		t.Fatalf("page: %+v %v", page, err)
	}
	empty, err := p.ListChatMessagesAfter(ctx, session.ID, all[4].ID, 50)
	if err != nil || len(empty) != 0 {
		t.Fatalf("unchanged: %+v %v", empty, err)
	}
	if _, err := p.ListChatMessagesAfter(ctx, "another-session", all[0].ID, 50); !errors.Is(err, service.ErrChatCursorExpired) {
		t.Fatalf("foreign anchor: %v", err)
	}
	if err := p.DeleteChatMessages(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ListChatMessagesAfter(ctx, session.ID, all[4].ID, 50); !errors.Is(err, service.ErrChatCursorExpired) {
		t.Fatalf("cleared anchor: %v", err)
	}
}
