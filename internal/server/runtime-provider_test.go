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
