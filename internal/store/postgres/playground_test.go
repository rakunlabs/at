package postgres

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

func playgroundConversation(t *testing.T, p *Postgres, owner string) *service.PlaygroundConversation {
	t.Helper()
	c, err := p.CreatePlaygroundConversation(t.Context(), service.PlaygroundConversation{
		OwnerUserID:  owner,
		Title:        "Playground",
		SystemPrompt: "be brief",
		ProviderKey:  "test",
		Model:        "text-model",
		Config:       map[string]any{"tools": []any{"bash_execute"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func playgroundAppend(t *testing.T, p *Postgres, owner, id string, roles ...string) []service.PlaygroundMessage {
	t.Helper()
	items := make([]service.PlaygroundMessage, 0, len(roles))
	for i, role := range roles {
		items = append(items, service.PlaygroundMessage{Role: role, ProviderKey: "test", Model: "text-model", Data: map[string]any{"content": fmt.Sprintf("m%d", i)}})
	}
	stored, err := p.AppendPlaygroundMessages(t.Context(), owner, id, items)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func TestPlaygroundConversationRoundTrip(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := t.Context()
	if _, err := p.CreatePlaygroundConversation(ctx, service.PlaygroundConversation{}); !errors.Is(err, service.ErrPlaygroundNotFound) {
		t.Fatal(err)
	}
	c := playgroundConversation(t, p, "user-a")
	if c.ID == "" || c.OwnerUserID != "user-a" || c.CreatedAt == "" || c.UpdatedAt == "" {
		t.Fatalf("create: %+v", c)
	}
	got, err := p.GetPlaygroundConversation(ctx, "user-a", c.ID)
	if err != nil || got.Title != "Playground" || got.SystemPrompt != "be brief" || got.Config["tools"] == nil {
		t.Fatalf("get: %+v %v", got, err)
	}
	patched, err := p.PatchPlaygroundConversation(ctx, "user-a", c.ID, map[string]any{"title": "Renamed", "config": map[string]any{"mcp": "https://example.test"}})
	if err != nil || patched.Title != "Renamed" || patched.SystemPrompt != "be brief" || patched.Config["mcp"] != "https://example.test" || patched.Config["tools"] != nil {
		t.Fatalf("patch: %+v %v", patched, err)
	}
	if _, err := p.PatchPlaygroundConversation(ctx, "user-a", c.ID, map[string]any{"owner_user_id": "stolen"}); !errors.Is(err, service.ErrPlaygroundConflict) {
		t.Fatal(err)
	}
	if _, err := p.PatchPlaygroundConversation(ctx, "user-a", c.ID, map[string]any{"title": 7}); !errors.Is(err, service.ErrPlaygroundConflict) {
		t.Fatal(err)
	}
}

func TestPlaygroundOwnerIsolation(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := t.Context()
	a := playgroundConversation(t, p, "user-a")
	b := playgroundConversation(t, p, "user-b")
	messages := playgroundAppend(t, p, "user-a", a.ID, "user", "assistant")

	for name, fn := range map[string]func() error{
		"get": func() error { _, e := p.GetPlaygroundConversation(ctx, "user-b", a.ID); return e },
		"patch": func() error {
			_, e := p.PatchPlaygroundConversation(ctx, "user-b", a.ID, map[string]any{"title": "x"})
			return e
		},
		"delete":        func() error { return p.DeletePlaygroundConversation(ctx, "user-b", a.ID) },
		"list messages": func() error { _, e := p.ListPlaygroundMessages(ctx, "user-b", a.ID, "", 50); return e },
		"append": func() error {
			_, e := p.AppendPlaygroundMessages(ctx, "user-b", a.ID, []service.PlaygroundMessage{{Role: "user"}})
			return e
		},
		"truncate": func() error { return p.TruncatePlaygroundMessages(ctx, "user-b", a.ID, 1) },
		"fork": func() error {
			_, e := p.ForkPlaygroundConversation(ctx, "user-b", a.ID, 1, "")
			return e
		},
		"empty owner":  func() error { _, e := p.GetPlaygroundConversation(ctx, "", a.ID); return e },
		"unknown id":   func() error { _, e := p.GetPlaygroundConversation(ctx, "user-a", "missing"); return e },
		"foreign list": func() error { _, e := p.ListPlaygroundConversations(ctx, "", "", 10); return e },
		"foreign cursor": func() error {
			_, e := p.ListPlaygroundConversations(ctx, "user-b", a.ID, 10)
			return e
		},
		"foreign message cursor": func() error {
			_, e := p.ListPlaygroundMessages(ctx, "user-a", a.ID, messages[0].ID+"x", 10)
			return e
		},
		"cross parent cursor": func() error {
			_, e := p.ListPlaygroundMessages(ctx, "user-b", b.ID, messages[0].ID, 10)
			return e
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := fn(); !errors.Is(err, service.ErrPlaygroundNotFound) {
				t.Fatalf("want ErrPlaygroundNotFound, got %v", err)
			}
		})
	}

	// user-b's own listing must not leak user-a's conversation.
	items, err := p.ListPlaygroundConversations(ctx, "user-b", "", 50)
	if err != nil || len(items) != 1 || items[0].ID != b.ID {
		t.Fatalf("owner listing leaked: %+v %v", items, err)
	}
}

func TestPlaygroundSequenceAssignment(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := t.Context()
	c := playgroundConversation(t, p, "user-a")

	first := playgroundAppend(t, p, "user-a", c.ID, "user", "assistant", "tool")
	for i, m := range first {
		if m.Sequence != int64(i+1) || m.ID == "" || m.ConversationID != c.ID {
			t.Fatalf("batch sequence: %+v", first)
		}
	}
	second := playgroundAppend(t, p, "user-a", c.ID, "user", "assistant")
	if second[0].Sequence != 4 || second[1].Sequence != 5 {
		t.Fatalf("continuation sequence: %+v", second)
	}

	all, err := p.ListPlaygroundMessages(ctx, "user-a", c.ID, "", 100)
	if err != nil || len(all) != 5 {
		t.Fatal(all, err)
	}
	for i, m := range all {
		if m.Sequence != int64(i+1) {
			t.Fatalf("chronological order broken: %+v", all)
		}
	}
	if all[0].Data["content"] != "m0" || all[2].Role != "tool" {
		t.Fatalf("payload not round-tripped: %+v", all)
	}
	// Cursor paging walks backwards but still returns chronological pages.
	page, err := p.ListPlaygroundMessages(ctx, "user-a", c.ID, all[2].ID, 2)
	if err != nil || len(page) != 2 || page[0].Sequence != 1 || page[1].Sequence != 2 {
		t.Fatalf("cursor page: %+v %v", page, err)
	}
	end, err := p.ListPlaygroundMessages(ctx, "user-a", c.ID, all[0].ID, 2)
	if err != nil || len(end) != 0 {
		t.Fatal(end, err)
	}
	if _, err := p.AppendPlaygroundMessages(ctx, "user-a", c.ID, []service.PlaygroundMessage{{Role: "system"}}); !errors.Is(err, service.ErrPlaygroundConflict) {
		t.Fatal(err)
	}
}

func TestPlaygroundConcurrentAppend(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := t.Context()
	c := playgroundConversation(t, p, "user-a")

	const writers, perWriter = 8, 3
	var wg sync.WaitGroup
	errs := make([]error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			items := make([]service.PlaygroundMessage, 0, perWriter)
			for j := range perWriter {
				items = append(items, service.PlaygroundMessage{Role: "user", Data: map[string]any{"writer": i, "index": j}})
			}
			_, errs[i] = p.AppendPlaygroundMessages(ctx, "user-a", c.ID, items)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}
	all, err := p.ListPlaygroundMessages(ctx, "user-a", c.ID, "", 1000)
	if err != nil || len(all) != writers*perWriter {
		t.Fatalf("lost concurrent appends: %d %v", len(all), err)
	}
	for i, m := range all {
		if m.Sequence != int64(i+1) {
			t.Fatalf("sequence %d is not gapless: %+v", i, all)
		}
	}
}

func TestPlaygroundTruncateAndCascade(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := t.Context()
	c := playgroundConversation(t, p, "user-a")
	playgroundAppend(t, p, "user-a", c.ID, "user", "assistant", "user", "assistant")

	if err := p.TruncatePlaygroundMessages(ctx, "user-a", c.ID, 0); !errors.Is(err, service.ErrPlaygroundConflict) {
		t.Fatal(err)
	}
	if err := p.TruncatePlaygroundMessages(ctx, "user-a", c.ID, 3); err != nil {
		t.Fatal(err)
	}
	kept, err := p.ListPlaygroundMessages(ctx, "user-a", c.ID, "", 100)
	if err != nil || len(kept) != 2 || kept[1].Sequence != 2 {
		t.Fatalf("truncate removed the wrong rows: %+v %v", kept, err)
	}
	// Re-appending after a rewind reuses the freed sequence numbers.
	next := playgroundAppend(t, p, "user-a", c.ID, "user")
	if next[0].Sequence != 3 {
		t.Fatalf("rewound sequence: %+v", next)
	}

	if err := p.DeletePlaygroundConversation(ctx, "user-a", c.ID); err != nil {
		t.Fatal(err)
	}
	n, err := p.goqu.From(p.tablePlaygroundMessages).Where(goqu.Ex{"conversation_id": c.ID}).CountContext(ctx)
	if err != nil || n != 0 {
		t.Fatalf("messages survived the cascade: %d %v", n, err)
	}
	if err := p.DeletePlaygroundConversation(ctx, "user-a", c.ID); !errors.Is(err, service.ErrPlaygroundNotFound) {
		t.Fatal(err)
	}
}

// playgroundUpdatedAt reads the raw timestamp, because the RFC3339 strings on
// the record round to whole seconds and would hide a spurious bump.
func playgroundUpdatedAt(t *testing.T, p *Postgres, id string) time.Time {
	t.Helper()
	var at time.Time
	found, err := p.goqu.From(p.tablePlaygroundChats).Select("updated_at").Where(goqu.Ex{"id": id}).ScanValContext(t.Context(), &at)
	if err != nil || !found {
		t.Fatalf("read updated_at: %v %v", found, err)
	}
	return at
}

func TestPlaygroundFork(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := t.Context()
	c := playgroundConversation(t, p, "user-a")
	playgroundAppend(t, p, "user-a", c.ID, "user", "assistant", "user", "assistant", "tool")
	sourceUpdatedAt := playgroundUpdatedAt(t, p, c.ID)

	forked, err := p.ForkPlaygroundConversation(ctx, "user-a", c.ID, 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if forked.ID == c.ID || forked.OwnerUserID != "user-a" {
		t.Fatalf("fork identity: %+v", forked)
	}
	if forked.ForkedFromID != c.ID || forked.ForkedFromSequence != 3 {
		t.Fatalf("fork lineage: %+v", forked)
	}
	// Settings ride along so the branch resumes with the same model wiring.
	if forked.SystemPrompt != "be brief" || forked.ProviderKey != "test" || forked.Model != "text-model" {
		t.Fatalf("fork settings: %+v", forked)
	}
	if tools, ok := forked.Config["tools"].([]any); !ok || len(tools) != 1 || tools[0] != "bash_execute" {
		t.Fatalf("fork config: %+v", forked.Config)
	}
	// An empty title is derived, never left blank.
	if forked.Title != "Playground (fork)" {
		t.Fatalf("derived title: %q", forked.Title)
	}

	copied, err := p.ListPlaygroundMessages(ctx, "user-a", forked.ID, "", 100)
	if err != nil || len(copied) != 3 {
		t.Fatalf("copied prefix: %d %v", len(copied), err)
	}
	original, err := p.ListPlaygroundMessages(ctx, "user-a", c.ID, "", 100)
	if err != nil || len(original) != 5 {
		t.Fatalf("source messages: %d %v", len(original), err)
	}
	for i, m := range copied {
		want := original[i]
		if m.Sequence != int64(i+1) {
			t.Fatalf("fork sequences are not gapless from 1: %+v", copied)
		}
		if m.ConversationID != forked.ID || m.ID == want.ID {
			t.Fatalf("copied row reuses source identity: %+v", m)
		}
		if m.Role != want.Role || m.ProviderKey != want.ProviderKey || m.Model != want.Model || m.Data["content"] != want.Data["content"] {
			t.Fatalf("copied payload diverged at %d: %+v vs %+v", i, m, want)
		}
	}

	// The source is untouched: same rows, same sequences, same updated_at.
	for i, m := range original {
		if m.Sequence != int64(i+1) {
			t.Fatalf("source resequenced: %+v", original)
		}
	}
	if got := playgroundUpdatedAt(t, p, c.ID); !got.Equal(sourceUpdatedAt) {
		t.Fatalf("fork bumped the source updated_at: %v -> %v", sourceUpdatedAt, got)
	}

	// Forking a fork records the intermediate parent, not the root.
	second, err := p.ForkPlaygroundConversation(ctx, "user-a", forked.ID, 2, "Branch B")
	if err != nil {
		t.Fatal(err)
	}
	if second.ForkedFromID != forked.ID || second.ForkedFromSequence != 2 || second.Title != "Branch B" {
		t.Fatalf("fork of a fork: %+v", second)
	}
	if again, err := p.ListPlaygroundMessages(ctx, "user-a", second.ID, "", 100); err != nil || len(again) != 2 || again[1].Sequence != 2 {
		t.Fatalf("second fork prefix: %+v %v", again, err)
	}

	for name, from := range map[string]int64{"past the end": 6, "zero": 0, "negative": -1} {
		t.Run(name, func(t *testing.T) {
			if _, err := p.ForkPlaygroundConversation(ctx, "user-a", c.ID, from, ""); !errors.Is(err, service.ErrPlaygroundConflict) {
				t.Fatalf("want ErrPlaygroundConflict, got %v", err)
			}
		})
	}

	// ON DELETE SET NULL: the fork outlives its source and only loses the
	// pointer back to it.
	if err := p.DeletePlaygroundConversation(ctx, "user-a", c.ID); err != nil {
		t.Fatal(err)
	}
	orphan, err := p.GetPlaygroundConversation(ctx, "user-a", forked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if orphan.ForkedFromID != "" || orphan.ForkedFromSequence != 3 {
		t.Fatalf("source delete did not null the lineage pointer: %+v", orphan)
	}
	if kept, err := p.ListPlaygroundMessages(ctx, "user-a", forked.ID, "", 100); err != nil || len(kept) != 3 {
		t.Fatalf("source delete cascaded into the fork: %+v %v", kept, err)
	}
}

func TestPlaygroundForkTitleDerivation(t *testing.T) {
	long := strings.Repeat("t", playgroundTitleMaxRunes+50)
	for name, tc := range map[string]struct{ explicit, source, want string }{
		"explicit wins":   {"Chosen", "Source", "Chosen"},
		"derived":         {"", "Source", "Source (fork)"},
		"untitled":        {"", "", playgroundUntitled + playgroundForkSuffix},
		"clamps derived":  {"", long, strings.Repeat("t", playgroundTitleMaxRunes-len(playgroundForkSuffix)) + playgroundForkSuffix},
		"clamps explicit": {long, "", strings.Repeat("t", playgroundTitleMaxRunes)},
	} {
		t.Run(name, func(t *testing.T) {
			if got := playgroundForkTitle(tc.explicit, tc.source); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestPlaygroundForkOwnerIsolation(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := t.Context()
	a := playgroundConversation(t, p, "user-a")
	playgroundAppend(t, p, "user-a", a.ID, "user", "assistant")

	if _, err := p.ForkPlaygroundConversation(ctx, "user-b", a.ID, 1, ""); !errors.Is(err, service.ErrPlaygroundNotFound) {
		t.Fatalf("want ErrPlaygroundNotFound, got %v", err)
	}
	// The rejected fork must not have left a conversation behind for user-b.
	if items, err := p.ListPlaygroundConversations(ctx, "user-b", "", 50); err != nil || len(items) != 0 {
		t.Fatalf("foreign fork leaked a conversation: %+v %v", items, err)
	}
	if kept, err := p.ListPlaygroundMessages(ctx, "user-a", a.ID, "", 50); err != nil || len(kept) != 2 {
		t.Fatalf("foreign fork damaged the source: %+v %v", kept, err)
	}
}

func TestPlaygroundForkPrefixCap(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := t.Context()
	c := playgroundConversation(t, p, "user-a")
	// One message past the cap is the smallest transcript that must be
	// refused; the cap itself must still succeed.
	roles := make([]string, service.PlaygroundForkMaxMessages+1)
	for i := range roles {
		roles[i] = "user"
	}
	playgroundAppend(t, p, "user-a", c.ID, roles...)

	if _, err := p.ForkPlaygroundConversation(ctx, "user-a", c.ID, service.PlaygroundForkMaxMessages+1, ""); !errors.Is(err, service.ErrPlaygroundTooLarge) {
		t.Fatalf("want ErrPlaygroundTooLarge, got %v", err)
	}
	forked, err := p.ForkPlaygroundConversation(ctx, "user-a", c.ID, service.PlaygroundForkMaxMessages, "")
	if err != nil {
		t.Fatal(err)
	}
	n, err := p.goqu.From(p.tablePlaygroundMessages).Where(goqu.Ex{"conversation_id": forked.ID}).CountContext(ctx)
	if err != nil || n != service.PlaygroundForkMaxMessages {
		t.Fatalf("cap boundary copied %d: %v", n, err)
	}
}

func TestPlaygroundConversationPagination(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := t.Context()
	var created []string
	for range 3 {
		created = append(created, playgroundConversation(t, p, "user-a").ID)
	}
	// Touching the oldest conversation moves it to the head of the list.
	if _, err := p.AppendPlaygroundMessages(ctx, "user-a", created[0], []service.PlaygroundMessage{{Role: "user"}}); err != nil {
		t.Fatal(err)
	}
	page, err := p.ListPlaygroundConversations(ctx, "user-a", "", 2)
	if err != nil || len(page) != 2 || page[0].ID != created[0] || page[1].ID != created[2] {
		t.Fatalf("recency order: %+v %v", page, err)
	}
	rest, err := p.ListPlaygroundConversations(ctx, "user-a", page[1].ID, 2)
	if err != nil || len(rest) != 1 || rest[0].ID != created[1] {
		t.Fatalf("cursor page: %+v %v", rest, err)
	}
	if items, err := p.ListPlaygroundConversations(ctx, "user-a", rest[0].ID, 2); err != nil || len(items) != 0 {
		t.Fatal(items, err)
	}
}
