package workflow

import (
	"context"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// TestTwoAgentsTwoConnections exercises the complete resolution flow that
// powers the multi-YouTube-account feature:
//
//  1. Two Connection rows exist for the same provider (youtube), holding
//     different refresh tokens.
//  2. Two agents each declare a different connection binding.
//  3. When each agent's tool handler calls getVar("youtube_refresh_token"),
//     the wrapped VarLookup must return the agent's own bound refresh token —
//     not the other agent's, and not the global fallback.
//
// The test uses real wrapping and resolver functions; only the connection
// lookup and base var lookup are in-memory stubs.
func TestTwoAgentsTwoConnections(t *testing.T) {
	connMain := &service.Connection{
		ID: "conn-main", Provider: "youtube", Name: "Main",
		Credentials: service.ConnectionCredentials{
			ClientID:     "main-client",
			ClientSecret: "main-secret",
			RefreshToken: "main-refresh",
		},
	}
	connClientB := &service.Connection{
		ID: "conn-client-b", Provider: "youtube", Name: "Client B",
		Credentials: service.ConnectionCredentials{
			ClientID:     "clientb-client",
			ClientSecret: "clientb-secret",
			RefreshToken: "clientb-refresh",
		},
	}
	store := map[string]*service.Connection{
		connMain.ID:    connMain,
		connClientB.ID: connClientB,
	}
	lookup := ConnectionLookup(func(_ context.Context, id string) (*service.Connection, error) {
		return store[id], nil
	})

	// Global variables act as the final fallback — populated as a sanity
	// check that unrelated keys still pass through.
	globals := map[string]string{
		"youtube_refresh_token": "GLOBAL-fallback",
		"unrelated_key":         "untouched",
	}
	baseLookup := VarLookup(func(key string) (string, error) {
		return globals[key], nil
	})

	// Simulate two agents running the same YouTube skill. Each has its own
	// agent-level binding to a different connection.
	agentA := map[string]string{"youtube": connMain.ID}
	agentB := map[string]string{"youtube": connClientB.ID}

	ctx := context.Background()

	bindingsA := ResolveAgentConnectionBindings(ctx, lookup, agentA, nil)
	bindingsB := ResolveAgentConnectionBindings(ctx, lookup, agentB, nil)

	lookupA := WrapVarLookupWithConnections(baseLookup, bindingsA)
	lookupB := WrapVarLookupWithConnections(baseLookup, bindingsB)

	// Agent A reads its own refresh token, not agent B's, not the global.
	gotA, _ := lookupA("youtube_refresh_token")
	if gotA != "main-refresh" {
		t.Errorf("agent A: got %q, want main-refresh", gotA)
	}
	// Agent B reads its own.
	gotB, _ := lookupB("youtube_refresh_token")
	if gotB != "clientb-refresh" {
		t.Errorf("agent B: got %q, want clientb-refresh", gotB)
	}
	// Client IDs also resolve from their respective connections.
	gotAID, _ := lookupA("youtube_client_id")
	if gotAID != "main-client" {
		t.Errorf("agent A client_id: got %q", gotAID)
	}
	gotBID, _ := lookupB("youtube_client_id")
	if gotBID != "clientb-client" {
		t.Errorf("agent B client_id: got %q", gotBID)
	}
	// Unrelated keys pass through unchanged.
	got, _ := lookupA("unrelated_key")
	if got != "untouched" {
		t.Errorf("passthrough: got %q", got)
	}
}

// TestSharedConnection covers the sharing case: two agents both bind to the
// SAME Connection row. Changing the connection's refresh token must be
// visible to both agents at the next resolution, without duplication.
func TestSharedConnection(t *testing.T) {
	shared := &service.Connection{
		ID: "conn-shared", Provider: "youtube", Name: "Shared",
		Credentials: service.ConnectionCredentials{
			RefreshToken: "rev-1",
		},
	}
	lookup := ConnectionLookup(func(_ context.Context, id string) (*service.Connection, error) {
		if id == shared.ID {
			return shared, nil
		}
		return nil, nil
	})

	baseLookup := VarLookup(func(_ string) (string, error) { return "", nil })

	// Both agents bind to the same connection.
	agentA := map[string]string{"youtube": shared.ID}
	agentB := map[string]string{"youtube": shared.ID}

	ctx := context.Background()
	lookupA := WrapVarLookupWithConnections(baseLookup,
		ResolveAgentConnectionBindings(ctx, lookup, agentA, nil))
	lookupB := WrapVarLookupWithConnections(baseLookup,
		ResolveAgentConnectionBindings(ctx, lookup, agentB, nil))

	gotA, _ := lookupA("youtube_refresh_token")
	gotB, _ := lookupB("youtube_refresh_token")
	if gotA != "rev-1" || gotB != "rev-1" {
		t.Errorf("initial: gotA=%q gotB=%q, both want rev-1", gotA, gotB)
	}

	// Simulate a refresh-token rotation on the connection row.
	shared.Credentials.RefreshToken = "rev-2"

	// Re-resolve bindings (in real code this happens per tool-call).
	lookupA = WrapVarLookupWithConnections(baseLookup,
		ResolveAgentConnectionBindings(ctx, lookup, agentA, nil))
	lookupB = WrapVarLookupWithConnections(baseLookup,
		ResolveAgentConnectionBindings(ctx, lookup, agentB, nil))
	gotA, _ = lookupA("youtube_refresh_token")
	gotB, _ = lookupB("youtube_refresh_token")
	if gotA != "rev-2" || gotB != "rev-2" {
		t.Errorf("after rotation: gotA=%q gotB=%q, both want rev-2", gotA, gotB)
	}
}

// TestPerSkillOverride: an agent binds to connection M by default but has a
// skill attachment that overrides the binding to connection B. Resolution
// within that skill's tools uses connection B; other tools use connection M.
func TestPerSkillOverride(t *testing.T) {
	connDefault := &service.Connection{
		ID: "conn-default", Provider: "youtube", Name: "Default",
		Credentials: service.ConnectionCredentials{RefreshToken: "default"},
	}
	connOverride := &service.Connection{
		ID: "conn-override", Provider: "youtube", Name: "Override",
		Credentials: service.ConnectionCredentials{RefreshToken: "override"},
	}
	store := map[string]*service.Connection{
		connDefault.ID:  connDefault,
		connOverride.ID: connOverride,
	}
	lookup := ConnectionLookup(func(_ context.Context, id string) (*service.Connection, error) {
		return store[id], nil
	})

	base := VarLookup(func(_ string) (string, error) { return "", nil })

	agentConns := map[string]string{"youtube": connDefault.ID}

	ctx := context.Background()

	// Tool owned by a skill with NO override:
	noOverride := WrapVarLookupWithConnections(base,
		ResolveAgentConnectionBindings(ctx, lookup, agentConns, nil))
	got, _ := noOverride("youtube_refresh_token")
	if got != "default" {
		t.Errorf("no override: got %q, want default", got)
	}

	// Tool owned by a skill WITH an override to connOverride:
	withOverride := WrapVarLookupWithConnections(base,
		ResolveAgentConnectionBindings(ctx, lookup, agentConns,
			map[string]string{"youtube": connOverride.ID}))
	got, _ = withOverride("youtube_refresh_token")
	if got != "override" {
		t.Errorf("with override: got %q, want override", got)
	}
}
