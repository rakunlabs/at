package server

import (
	"context"
	"errors"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestAgentMCPToolsClients(t *testing.T) {
	ctx := context.Background()
	s := &Server{}

	t.Run("client tools are advertised and dispatched", func(t *testing.T) {
		first := &fakeRuntimeMCPClient{tools: []service.Tool{{Name: "search"}}, callErr: errors.New("down")}
		second := &fakeRuntimeMCPClient{tools: []service.Tool{{Name: "search"}, {Name: "fetch"}}, callResult: "ok"}
		m := s.newAgentMCPTools("test", nil)
		m.addClient(ctx, first, "first")
		m.addClient(ctx, second, "second")

		if got := len(m.Tools()); got != 3 {
			t.Fatalf("Tools() = %d, want 3", got)
		}
		if !m.Owns("fetch") || m.Owns("missing") {
			t.Fatal("ownership does not follow advertised tools")
		}
		if _, ok := m.SetName("fetch"); ok {
			t.Fatal("client tools must not report an MCP set")
		}
		got, err := m.Call(ctx, "search", nil, 0)
		if err != nil || got != "ok" {
			t.Fatalf("Call() = %q, %v; want the next client's answer", got, err)
		}
		if _, err := m.Call(ctx, "missing", nil, 0); err == nil {
			t.Fatal("expected error for an unowned tool")
		}

		m.Close()
		if first.closed != 1 || second.closed != 1 {
			t.Fatalf("Close() closed %d/%d clients, want 1/1", first.closed, second.closed)
		}
	})

	t.Run("refused tools are neither advertised nor owned", func(t *testing.T) {
		client := &fakeRuntimeMCPClient{tools: []service.Tool{{Name: "read_file"}, {Name: "search"}}}
		m := s.newAgentMCPTools("test", func(tool service.Tool) bool { return tool.Name != "read_file" })
		m.addClient(ctx, client, "c")
		defer m.Close()

		if len(m.Tools()) != 1 || m.Tools()[0].Name != "search" {
			t.Fatalf("Tools() = %#v, want only search", m.Tools())
		}
		if m.Owns("read_file") {
			t.Fatal("a refused tool must not be dispatched by MCP")
		}
	})

	t.Run("list failure keeps the client for closing", func(t *testing.T) {
		client := &fakeRuntimeMCPClient{listErr: errors.New("boom")}
		m := s.newAgentMCPTools("test", nil)
		m.addClient(ctx, client, "c")
		if len(m.Tools()) != 0 {
			t.Fatal("no tools expected")
		}
		m.Close()
		if client.closed != 1 {
			t.Fatal("client must still be closed")
		}
	})

	t.Run("nil receiver", func(t *testing.T) {
		var m *agentMCPTools
		if m.Owns("x") {
			t.Fatal("nil toolset owns nothing")
		}
		m.Close()
	})
}
