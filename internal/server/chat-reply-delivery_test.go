package server

import (
	"context"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

type replySessionStore struct {
	service.ChatSessionStorer
	sessions map[string]*service.ChatSession
}

func (m *replySessionStore) GetChatSession(_ context.Context, id string) (*service.ChatSession, error) {
	return m.sessions[id], nil
}

func TestDeliverChatReplyToPlatform(t *testing.T) {
	s := &Server{chatSessionStore: &replySessionStore{sessions: map[string]*service.ChatSession{
		"web":  {ID: "web"},
		"tg":   {ID: "tg", Config: service.ChatSessionConfig{Platform: "telegram", PlatformChannelID: "42", BotConfigID: "bot1"}},
		"task": {ID: "task", Config: service.ChatSessionConfig{Platform: "telegram", PlatformChannelID: "42"}},
		"bad":  {ID: "bad", Config: service.ChatSessionConfig{Platform: "telegram", PlatformChannelID: "x", BotConfigID: "bot1"}},
	}}}
	for _, id := range []string{"web", "task", "missing"} {
		if err := s.deliverChatReplyToPlatform(t.Context(), id, "hi"); err != nil {
			t.Fatalf("%s: non-platform session must be a no-op: %v", id, err)
		}
	}
	if err := s.deliverChatReplyToPlatform(t.Context(), "bad", "hi"); err == nil {
		t.Fatal("invalid chat id accepted")
	}
	err := s.deliverChatReplyToPlatform(t.Context(), "tg", "hi")
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("stopped bot must report that nothing was sent: %v", err)
	}
}
