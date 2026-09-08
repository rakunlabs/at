package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

func TestPersonalChatOwnershipAndPagination(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := t.Context()
	if _, err := p.CreatePersonalConversation(ctx, service.PersonalConversation{}); !errors.Is(err, service.ErrPersonalChatNotFound) {
		t.Fatal(err)
	}
	a, err := p.CreatePersonalConversation(ctx, service.PersonalConversation{OwnerUserID: "admin-a", Title: "A", ProviderKey: "p", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.CreatePersonalConversation(ctx, service.PersonalConversation{OwnerUserID: "admin-b", Title: "B", ProviderKey: "p", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	items, err := p.ListPersonalConversations(ctx, "admin-a", "", 1)
	if err != nil || len(items) != 1 || items[0].ID != a.ID {
		t.Fatalf("owner before pagination: %+v %v", items, err)
	}
	items, err = p.ListPersonalConversations(ctx, "admin-a", a.ID, 1)
	if err != nil || len(items) != 0 {
		t.Fatal(items, err)
	}
	turn, err := p.BeginPersonalTurn(ctx, "admin-a", a.ID, "req", "hello")
	if err != nil {
		t.Fatal(err)
	}
	for name, fn := range map[string]func() error{
		"get": func() error { _, e := p.GetPersonalConversation(ctx, "admin-b", a.ID); return e },
		"patch": func() error {
			_, e := p.PatchPersonalConversation(ctx, "admin-b", a.ID, map[string]string{"title": "stolen"})
			return e
		},
		"delete":        func() error { return p.DeletePersonalConversation(ctx, "admin-b", a.ID) },
		"list messages": func() error { _, e := p.ListPersonalMessages(ctx, "admin-b", a.ID, "", 50); return e },
		"get message":   func() error { _, e := p.GetPersonalMessage(ctx, "admin-b", a.ID, turn.Assistant.ID); return e },
		"wrong parent":  func() error { _, e := p.GetPersonalMessage(ctx, "admin-b", b.ID, turn.Assistant.ID); return e },
		"cancel":        func() error { _, e := p.CancelPersonalTurn(ctx, "admin-b", a.ID, turn.Assistant.ID); return e },
		"begin":         func() error { _, e := p.BeginPersonalTurn(ctx, "admin-b", a.ID, "req", "hello"); return e },
		"worker": func() error {
			_, e := p.CheckpointPersonalTurn(ctx, "admin-b", a.ID, turn.LeaseToken, turn.Assistant)
			return e
		},
		"empty owner": func() error { _, e := p.GetPersonalConversation(ctx, "", a.ID); return e },
	} {
		t.Run(name, func(t *testing.T) {
			if err := fn(); !errors.Is(err, service.ErrPersonalChatNotFound) {
				t.Fatal(err)
			}
		})
	}
	if err := p.DeletePersonalConversation(ctx, "admin-a", a.ID); !errors.Is(err, service.ErrPersonalChatConflict) {
		t.Fatal(err)
	}
	if _, err := p.PatchPersonalConversation(ctx, "admin-a", a.ID, map[string]string{"title": "busy"}); !errors.Is(err, service.ErrPersonalChatConflict) {
		t.Fatal(err)
	}
	if _, err := p.CancelPersonalTurn(ctx, "admin-a", a.ID, turn.User.ID); !errors.Is(err, service.ErrPersonalChatNotFound) {
		t.Fatal(err)
	}
	if _, err := p.CancelPersonalTurn(ctx, "admin-a", a.ID, turn.Assistant.ID); err != nil {
		t.Fatal(err)
	}
	a, err = p.PatchPersonalConversation(ctx, "admin-a", a.ID, map[string]string{"title": "updated", "system_prompt": "be brief"})
	if err != nil || a.Title != "updated" || a.SystemPrompt != "be brief" {
		t.Fatal(a, err)
	}
	if err := p.DeletePersonalConversation(ctx, "admin-a", a.ID); err != nil {
		t.Fatal(err)
	}
	n, err := p.goqu.From(p.tablePersonalMessages).CountContext(ctx)
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestPersonalChatSequenceClockSkew(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := t.Context()
	c, err := p.CreatePersonalConversation(ctx, service.PersonalConversation{OwnerUserID: "a", Title: "chat", ProviderKey: "p", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	previous, err := p.BeginPersonalTurn(ctx, "a", c.ID, "previous", "old question")
	if err != nil {
		t.Fatal(err)
	}
	previous.Assistant.Content = "old answer"
	previous.Assistant.Status = "completed"
	if _, err := p.CheckpointPersonalTurn(ctx, "a", c.ID, previous.LeaseToken, previous.Assistant); err != nil {
		t.Fatal(err)
	}
	// Simulate an earlier admission on a replica with a clock far in the future.
	for i, m := range []service.PersonalChatMessage{previous.User, previous.Assistant} {
		oldID := m.ID
		m.ID = fmt.Sprintf("7ZZZZZZZZZZZZZZZZZZZZZZZZ%d", i)
		m.CreatedAt = time.Now().Add(24 * time.Hour)
		data, _ := json.Marshal(m)
		if _, err := p.goqu.Update(p.tablePersonalMessages).Set(goqu.Record{"id": m.ID, "data": string(data)}).Where(goqu.Ex{"id": oldID}).Executor().ExecContext(ctx); err != nil {
			t.Fatal(err)
		}
	}
	current, err := p.BeginPersonalTurn(ctx, "a", c.ID, "current", "current question")
	if err != nil {
		t.Fatal(err)
	}
	if current.User.Sequence != 3 || current.Assistant.Sequence != 4 {
		t.Fatal(current)
	}
	page, err := p.ListPersonalMessages(ctx, "a", c.ID, "", 2)
	if err != nil || len(page) != 2 || page[0].ID != current.Assistant.ID || page[1].ID != current.User.ID {
		t.Fatal(page, err)
	}
	older, err := p.ListPersonalMessages(ctx, "a", c.ID, page[1].ID, 2)
	if err != nil || len(older) != 2 || older[0].Sequence != 2 || older[1].Sequence != 1 || older[0].ID < current.User.ID {
		t.Fatal(older, err)
	}
	end, err := p.ListPersonalMessages(ctx, "a", c.ID, older[1].ID, 2)
	if err != nil || len(end) != 0 {
		t.Fatal(end, err)
	}
	all, err := p.ListPersonalMessages(ctx, "a", c.ID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	var prompt []string
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Status == "completed" {
			prompt = append(prompt, all[i].Content)
		}
	}
	if strings.Join(prompt, "|") != "old question|old answer|current question" {
		t.Fatal(prompt)
	}
	for _, owner := range []string{"a", "other"} {
		if _, err := p.ListPersonalMessages(ctx, owner, c.ID, "foreign-cursor", 2); !errors.Is(err, service.ErrPersonalChatNotFound) {
			t.Fatal(err)
		}
	}
	// Worker writes cannot replace the database-owned sequence.
	current.Assistant.Sequence = 900
	m, err := p.CheckpointPersonalTurn(ctx, "a", c.ID, current.LeaseToken, current.Assistant)
	if err != nil || m.Sequence != 4 {
		t.Fatal(m, err)
	}
	replay, err := p.BeginPersonalTurn(ctx, "a", c.ID, "current", "current question")
	if err != nil || !replay.Replay || replay.User.Sequence != 3 || replay.Assistant.Sequence != 4 {
		t.Fatal(replay, err)
	}
	m, err = p.CancelPersonalTurn(ctx, "a", c.ID, current.Assistant.ID)
	if err != nil || m.Sequence != 4 {
		t.Fatal(m, err)
	}
}

func TestPersonalChatSequenceMigrationFrom29(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := t.Context()
	prefix := strings.TrimSuffix(fmt.Sprint(p.tableConversations.GetTable()), "personal_conversations") + "legacy_"
	for _, version := range []string{"29_personal_chat.sql", "30_personal_chat_sequence.sql"} {
		body, err := migrationFS.ReadFile("migrations/" + version)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.db.ExecContext(ctx, strings.ReplaceAll(string(body), "${TABLE_PREFIX}", prefix)); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(version, "29_") {
			if _, err := p.goqu.Insert(prefix + "personal_conversations").Rows(goqu.Record{"id": "chat", "owner_user_id": "a", "title": "legacy", "provider_key": "p", "model": "m"}).Executor().ExecContext(ctx); err != nil {
				t.Fatal(err)
			}
			for _, role := range []string{"user", "assistant"} {
				id := "1"
				if role == "assistant" {
					id = "2"
				}
				// Deliberately omit sequence, as persisted by v29.
				data := fmt.Sprintf(`{"id":%q,"conversation_id":"chat","request_id":"old","role":%q,"status":"completed","content":"legacy"}`, id, role)
				if _, err := p.goqu.Insert(prefix + "personal_messages").Rows(goqu.Record{"id": id, "conversation_id": "chat", "request_id": "old", "request_hash": "hash", "role": role, "status": "completed", "data": data}).Executor().ExecContext(ctx); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	other := &Postgres{db: p.db, goqu: p.goqu, tableConversations: goqu.T(prefix + "personal_conversations"), tablePersonalMessages: goqu.T(prefix + "personal_messages")}
	page, err := other.ListPersonalMessages(ctx, "a", "chat", "", 10)
	if err != nil || len(page) != 2 || page[0].Sequence != 2 || page[1].Sequence != 1 {
		t.Fatal(page, err)
	}
	var data string
	if _, err := p.goqu.From(other.tablePersonalMessages).Select("data").Where(goqu.Ex{"id": "2"}).ScanValContext(ctx, &data); err != nil {
		t.Fatal(err)
	}
	var m service.PersonalChatMessage
	if err := json.Unmarshal([]byte(data), &m); err != nil || m.Sequence != 2 {
		t.Fatal(m, err)
	}
	turn, err := other.BeginPersonalTurn(ctx, "a", "chat", "next", "new")
	if err != nil || turn.User.Sequence != 3 || turn.Assistant.Sequence != 4 {
		t.Fatal(turn, err)
	}
}

func TestPersonalChatAdmissionReplayAndRecovery(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := t.Context()
	c, err := p.CreatePersonalConversation(ctx, service.PersonalConversation{OwnerUserID: "a", Title: "A", ProviderKey: "p", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	var winner *service.PersonalChatTurn
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			turn, err := p.BeginPersonalTurn(ctx, "a", c.ID, fmt.Sprint(i), "text")
			if err == nil {
				winner = turn
				wins.Add(1)
			} else if !errors.Is(err, service.ErrPersonalChatConflict) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("winners=%d", wins.Load())
	}
	n, err := p.goqu.From(p.tablePersonalMessages).CountContext(ctx)
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
	for range 8 {
		wg.Go(func() {
			replay, err := p.BeginPersonalTurn(ctx, "a", c.ID, winner.User.RequestID, "text")
			if err != nil || !replay.Replay || replay.Assistant.ID != winner.Assistant.ID || replay.LeaseToken != "" {
				t.Errorf("replay=%+v err=%v", replay, err)
			}
		})
	}
	wg.Wait()
	if _, err := p.BeginPersonalTurn(ctx, "a", c.ID, winner.User.RequestID, "changed"); !errors.Is(err, service.ErrPersonalChatConflict) {
		t.Fatal(err)
	}
	winner.Assistant.Status = "streaming"
	winner.Assistant.Content = "partial"
	if _, err := p.CheckpointPersonalTurn(ctx, "a", c.ID, winner.LeaseToken, winner.Assistant); err != nil {
		t.Fatal(err)
	}
	// A second store facade represents a different replica/process.
	other := &Postgres{db: p.db, goqu: p.goqu, tableConversations: p.tableConversations, tablePersonalMessages: p.tablePersonalMessages}
	if _, err := p.goqu.Update(p.tablePersonalMessages).Set(goqu.Record{"lease_until": goqu.L("clock_timestamp() - interval '1 second'")}).Where(goqu.Ex{"id": winner.Assistant.ID}).Executor().ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	stale, err := other.GetPersonalMessage(ctx, "a", c.ID, winner.Assistant.ID)
	if err != nil || stale.Status != "failed" || stale.Error != "generation_interrupted" || stale.Content != "partial" {
		t.Fatal(stale, err)
	}
	next, err := other.BeginPersonalTurn(ctx, "a", c.ID, "next", "new")
	if err != nil {
		t.Fatal(err)
	}
	winner.Assistant.Status = "completed"
	winner.Assistant.Content = "late write"
	stale, err = p.CheckpointPersonalTurn(ctx, "a", c.ID, winner.LeaseToken, winner.Assistant)
	if err != nil || stale.Content != "partial" || stale.Status != "failed" {
		t.Fatal(stale, err)
	}
	cancelled, err := other.CancelPersonalTurn(ctx, "a", c.ID, next.Assistant.ID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatal(cancelled, err)
	}
	next.Assistant.Status = "completed"
	next.Assistant.Content = "late"
	cancelled, err = p.CheckpointPersonalTurn(ctx, "a", c.ID, next.LeaseToken, next.Assistant)
	if err != nil || cancelled.Status != "cancelled" || cancelled.Content != "" {
		t.Fatal(cancelled, err)
	}
	replay, err := p.BeginPersonalTurn(ctx, "a", c.ID, "next", "new")
	if err != nil || !replay.Replay || replay.Assistant.Status != "cancelled" {
		t.Fatal(replay, err)
	}
	last, err := p.BeginPersonalTurn(ctx, "a", c.ID, "deadline", "new")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.goqu.Update(p.tablePersonalMessages).Set(goqu.Record{"deadline": goqu.L("clock_timestamp() - interval '1 second'")}).Where(goqu.Ex{"id": last.Assistant.ID}).Executor().ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	last.Assistant.Status = "completed"
	stale, err = p.CheckpointPersonalTurn(ctx, "a", c.ID, last.LeaseToken, last.Assistant)
	if err != nil || stale.Status != "failed" {
		t.Fatal(stale, err)
	}
}

func TestPersonalChatCancelMutationRace(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := t.Context()
	c, err := p.CreatePersonalConversation(ctx, service.PersonalConversation{OwnerUserID: "a", Title: "A", ProviderKey: "p", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 15 {
		turn, err := p.BeginPersonalTurn(ctx, "a", c.ID, fmt.Sprint(i), "text")
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Go(func() {
			turn.Assistant.Content = "partial"
			turn.Assistant.Status = "streaming"
			if _, err := p.CheckpointPersonalTurn(ctx, "a", c.ID, turn.LeaseToken, turn.Assistant); err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() {
			if _, err := p.CancelPersonalTurn(ctx, "a", c.ID, turn.Assistant.ID); err != nil {
				t.Error(err)
			}
		})
		wg.Wait()
		m, err := p.GetPersonalMessage(ctx, "a", c.ID, turn.Assistant.ID)
		if err != nil || m.Status != "cancelled" {
			t.Fatal(m, err)
		}
	}
}
