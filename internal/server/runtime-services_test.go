package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/loopgov"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func TestRuntimeServiceBindingRenewalAcrossReplicas(t *testing.T) {
	store := postgrestest.New(t, nil)
	user, err := store.CreateAuthUser(t.Context(), service.AuthUser{Username: "service-owner", Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	s := &Server{store: store, loopGov: loopgov.New(loopgov.Config{WorkspaceRoot: root}, nil)}
	replica := &Server{store: store, loopGov: loopgov.New(loopgov.Config{WorkspaceRoot: root}, nil)}
	login := func(id string) context.Context {
		t.Helper()
		if err := store.CreateAuthSession(t.Context(), service.AuthSession{Hash: id, UserID: user.ID, Version: user.SessionVersion, Transport: "web", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		p, _, err := store.ResolveWorkspaceAccess(t.Context(), "legacy-default", user.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		ctx, err := s.bindRuntimePrincipal(service.WithAccessPrincipal(t.Context(), p), "binding-management")
		if err != nil {
			t.Fatal(err)
		}
		return ctx
	}
	native := login("first-browser-session")
	runAs, err := store.CreateAuthUser(t.Context(), service.AuthUser{Username: "service-member"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkspaceMember(native, service.WorkspaceMembership{WorkspaceID: "legacy-default", UserID: runAs.ID, Role: "member", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	bot, err := store.CreateBotConfig(native, service.BotConfig{Name: "service-test-bot", Platform: "telegram"})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := s.saveRuntimeBinding(native, "bot", bot.ID, false, runAs.ID)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Version != 1 || binding.Revoked {
		t.Fatalf("initial binding: %+v", binding)
	}
	boot := service.WithExecutionMaintenance(t.Context())
	if _, err := store.ListExecutionServiceBindings(t.Context(), "bot"); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatal("blank context enumerated installation service catalog")
	}
	if entries, err := store.ListExecutionServiceBindings(boot, "bot"); err != nil || len(entries) != 1 {
		t.Fatalf("boot catalog: %d %v", len(entries), err)
	}
	first, err := s.ResumeRuntimeSubject(boot, "bot", bot.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if service.HasExecutionMaintenance(first) {
		t.Fatal("workspace run inherited installation maintenance authority")
	}
	second, err := replica.ResumeRuntimeSubject(t.Context(), "bot", bot.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAuthSession(t.Context(), "first-browser-session"); err != nil {
		t.Fatal(err)
	}
	if _, err := replica.ResumeRuntimeSubject(t.Context(), "bot", bot.ID, nil); err != nil {
		t.Fatalf("service depended on expired originating browser: %v", err)
	}
	if err := service.CheckExecution(native, service.ExecutionAction{Kind: "resource", Name: "execution.run"}); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatal("deleted browser session retained authority")
	}
	current := login("renewal-browser-session")
	renewed, err := s.saveRuntimeBinding(current, "bot", bot.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.ID != binding.ID || renewed.Version != 2 {
		t.Fatalf("renewal did not fence old generation: %+v", renewed)
	}
	for _, old := range []context.Context{first, second} {
		if err := service.CheckExecution(old, service.ExecutionAction{Kind: "resource", Name: "execution.run"}); !errors.Is(err, service.ErrExecutionDenied) {
			t.Fatal("replica retained old service generation")
		}
	}
	active, err := replica.ResumeRuntimeSubject(t.Context(), "bot", bot.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.saveRuntimeBinding(current, "bot", bot.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := service.CheckExecution(active, service.ExecutionAction{Kind: "resource", Name: "execution.run"}); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatal("revoked service stayed active")
	}
	if _, err := replica.ResumeRuntimeSubject(t.Context(), "bot", bot.ID, nil); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatal("revoked service resumed")
	}
}
