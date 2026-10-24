package server

import (
	"context"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func TestElicitationBrokerOwnership(t *testing.T) {
	b := newElicitationBroker()
	ask := b.elicitor("ws", "alice", "chats")
	schema := map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}, "required": []any{"name"}}

	done := make(chan service.ElicitResult, 1)
	go func() {
		res, _ := ask(context.Background(), service.ElicitRequest{Server: "s", Message: "Name?", RequestedSchema: schema})
		done <- res
	}()
	var id string
	deadline := time.Now().Add(2 * time.Second)
	for id == "" && time.Now().Before(deadline) {
		items, _ := b.list("ws", "alice")
		if len(items) == 1 {
			id = items[0].ID
		}
		time.Sleep(5 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("question not listed")
	}
	if items, _ := b.list("ws", "bob"); len(items) != 0 {
		t.Fatal("another account sees the question")
	}
	if items, _ := b.list("other", "alice"); len(items) != 0 {
		t.Fatal("another workspace sees the question")
	}
	if found, _ := b.answer("ws", "bob", id, service.ElicitResult{Action: service.ElicitDecline}); found {
		t.Fatal("another account answered")
	}
	if found, err := b.answer("ws", "alice", id, service.ElicitResult{Action: service.ElicitAccept}); !found || err == nil {
		t.Fatalf("missing required field must be refused: %v %v", found, err)
	}
	if found, err := b.answer("ws", "alice", id, service.ElicitResult{Action: service.ElicitAccept, Content: map[string]any{"name": "ada"}}); !found || err != nil {
		t.Fatalf("answer: %v %v", found, err)
	}
	if res := <-done; res.Action != service.ElicitAccept || res.Content["name"] != "ada" {
		t.Fatalf("result = %+v", res)
	}
	if items, _ := b.list("ws", "alice"); len(items) != 0 {
		t.Fatal("answered question still listed")
	}

	// A cancelled run removes its question.
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _, _ = ask(ctx, service.ElicitRequest{RequestedSchema: schema}) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)
	if items, _ := b.list("ws", "alice"); len(items) != 0 {
		t.Fatal("cancelled question still listed")
	}
}
