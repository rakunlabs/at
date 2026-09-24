package server

import (
	"bytes"
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/loopgov"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func TestRuntimeProviderWorkspaceCacheAndModelGrant(t *testing.T) {
	store := postgrestest.New(t, bytes.Repeat([]byte{1}, 32))
	admin, err := store.CreateAuthUser(t.Context(), service.AuthUser{Username: "provider-admin", Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	member, err := store.CreateAuthUser(t.Context(), service.AuthUser{Username: "provider-member"}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []*service.AuthUser{admin, member} {
		if err := store.CreateAuthSession(t.Context(), service.AuthSession{Hash: u.ID, UserID: u.ID, Version: u.SessionVersion, Transport: "web", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	var calls atomic.Int32
	s := &Server{store: store, loopGov: loopgov.New(loopgov.Config{WorkspaceRoot: t.TempDir()}, nil), providerFactory: func(c config.LLMConfig) (service.LLMProvider, error) {
		calls.Add(1)
		return &fakeObsProvider{responses: []*service.LLMResponse{{Content: c.APIKey, Finished: true}}}, nil
	}}
	actor := func(workspace, user string) context.Context {
		t.Helper()
		p, _, err := store.ResolveWorkspaceAccess(t.Context(), workspace, user, user)
		if err != nil {
			t.Fatal(err)
		}
		return service.WithAccessPrincipal(t.Context(), p)
	}
	var contexts []context.Context
	var workspaceIDs []string
	for _, name := range []string{"one", "two"} {
		workspace, err := store.CreateWorkspace(service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: admin.ID}), name, admin.ID)
		if err != nil {
			t.Fatal(err)
		}
		ctx := actor(workspace.ID, admin.ID)
		workspace.ExecutionEnabled = true
		if _, err := store.UpdateWorkspace(ctx, *workspace); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateProvider(ctx, service.ProviderRecord{Key: "same", Config: config.LLMConfig{Type: "openai", Model: "m1", APIKey: name}}); err != nil {
			t.Fatal(err)
		}
		bound, err := s.bindRuntimePrincipal(ctx, "test")
		if err != nil {
			t.Fatal(err)
		}
		contexts = append(contexts, bound)
		workspaceIDs = append(workspaceIDs, workspace.ID)
	}
	for i, ctx := range contexts {
		provider, model, err := s.runtimeProviderLookup(ctx, "same")
		if err != nil {
			t.Fatal(err)
		}
		resp, err := provider.Chat(ctx, model, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Content != []string{"one", "two"}[i] {
			t.Fatalf("globally named provider leaked across workspaces: %q", resp.Content)
		}
	}
	global := actor("legacy-default", admin.ID)
	shared, err := store.CreateProvider(global, service.ProviderRecord{Key: "shared", Config: config.LLMConfig{Type: "openai", Model: "m1", APIKey: "shared"}})
	if err != nil {
		t.Fatal(err)
	}
	workspaceAdmin := actor(workspaceIDs[0], admin.ID)
	if err := store.SetWorkspaceMember(workspaceAdmin, service.WorkspaceMembership{WorkspaceID: workspaceIDs[0], UserID: member.ID, Role: "member", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWorkspaceProviderGrant(workspaceAdmin, service.WorkspaceProviderGrant{ProviderID: shared.ID, ModelPatterns: []string{"m1"}}); err != nil {
		t.Fatal(err)
	}
	memberCtx, err := s.bindRuntimePrincipal(actor(workspaceIDs[0], member.ID), "test")
	if err != nil {
		t.Fatal(err)
	}
	provider, _, err := s.runtimeProviderLookup(memberCtx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	if _, err := provider.Chat(memberCtx, "m2", nil, nil, nil); err == nil {
		t.Fatal("unauthorized model admitted")
	}
	if calls.Load() != before {
		t.Fatal("unauthorized model constructed a credential-bearing provider")
	}
	if _, err := provider.Chat(memberCtx, "m1", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteWorkspaceProviderGrant(workspaceAdmin, shared.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Chat(memberCtx, "m1", nil, nil, nil); err == nil {
		t.Fatal("cached provider survived grant revocation")
	}
}

func TestRuntimeProviderPersonalIdentityCache(t *testing.T) {
	store := postgrestest.New(t, bytes.Repeat([]byte{2}, 32))
	admin, err := store.CreateAuthUser(t.Context(), service.AuthUser{Username: "personal-cache-admin", Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	alice, err := store.CreateAuthUser(t.Context(), service.AuthUser{Username: "personal-cache-alice"}, false)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := store.CreateAuthUser(t.Context(), service.AuthUser{Username: "personal-cache-bob"}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []*service.AuthUser{admin, alice, bob} {
		if err := store.CreateAuthSession(t.Context(), service.AuthSession{Hash: user.ID, UserID: user.ID, Version: user.SessionVersion, Transport: "web", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	workspace, err := store.CreateWorkspace(service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: admin.ID}), "personal cache", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	adminPrincipal, _, err := store.ResolveWorkspaceAccess(t.Context(), workspace.ID, admin.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	adminCtx := service.WithAccessPrincipal(t.Context(), adminPrincipal)
	workspace.ExecutionEnabled = true
	if _, err := store.UpdateWorkspace(adminCtx, *workspace); err != nil {
		t.Fatal(err)
	}
	for _, user := range []*service.AuthUser{alice, bob} {
		if err := store.SetWorkspaceMember(adminCtx, service.WorkspaceMembership{WorkspaceID: workspace.ID, UserID: user.ID, Role: "member", Status: "active"}); err != nil {
			t.Fatal(err)
		}
	}
	actor := func(user *service.AuthUser) context.Context {
		t.Helper()
		principal, _, err := store.ResolveWorkspaceAccess(t.Context(), workspace.ID, user.ID, user.ID)
		if err != nil {
			t.Fatal(err)
		}
		return service.WithAccessPrincipal(t.Context(), principal)
	}
	aliceCtx, bobCtx := actor(alice), actor(bob)
	aliceProvider, err := store.CreatePersonalProvider(aliceCtx, service.ProviderRecord{Key: "same", Config: config.LLMConfig{Type: "openai", Model: "m", APIKey: "alice"}})
	if err != nil {
		t.Fatal(err)
	}
	bobProvider, err := store.CreatePersonalProvider(bobCtx, service.ProviderRecord{Key: "same", Config: config.LLMConfig{Type: "openai", Model: "m", APIKey: "bob"}})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	s := &Server{store: store, loopGov: loopgov.New(loopgov.Config{WorkspaceRoot: t.TempDir()}, nil), providerFactory: func(c config.LLMConfig) (service.LLMProvider, error) {
		calls.Add(1)
		return &fakeObsProvider{responses: []*service.LLMResponse{{Content: c.APIKey, Finished: true}}}, nil
	}}
	aliceRuntime, err := s.bindRuntimePrincipal(aliceCtx, "session")
	if err != nil {
		t.Fatal(err)
	}
	bobRuntime, err := s.bindRuntimePrincipal(bobCtx, "session")
	if err != nil {
		t.Fatal(err)
	}
	chat := func(ctx context.Context, reference string) string {
		t.Helper()
		provider, model, err := s.runtimeProviderLookup(ctx, reference)
		if err != nil {
			t.Fatal(err)
		}
		response, err := provider.Chat(ctx, model, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return response.Content
	}
	if got := chat(aliceRuntime, aliceProvider.Reference); got != "alice" {
		t.Fatalf("Alice credential = %q", got)
	}
	if got := chat(bobRuntime, bobProvider.Reference); got != "bob" {
		t.Fatalf("Bob credential = %q", got)
	}
	if _, _, err := s.runtimeProviderLookup(bobRuntime, aliceProvider.Reference); err == nil {
		t.Fatal("Bob resolved Alice's same-named personal provider")
	}
	aliceProvider.Config.APIKey = "alice-rotated"
	if _, err := store.UpdatePersonalProvider(aliceCtx, aliceProvider.ID, *aliceProvider); err != nil {
		t.Fatal(err)
	}
	if got := chat(aliceRuntime, aliceProvider.Reference); got != "alice-rotated" {
		t.Fatalf("stale personal cache credential = %q", got)
	}
	if calls.Load() != 3 {
		t.Fatalf("provider constructions = %d, want 3", calls.Load())
	}
}
