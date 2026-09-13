package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/antropic"
)

func TestClaudeOAuthRotationPersistsWithoutBrowserContext(t *testing.T) {
	f := newMachineFixture(t)
	record, err := f.store.CreateProvider(f.ctx, service.ProviderRecord{Key: "claude", Config: config.LLMConfig{Type: "anthropic", AuthType: "claude-code", Model: "model", APIKey: "old-access", RefreshToken: "old-refresh"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	callback := f.s.claudeOAuthRefreshCallback(record.Key, record.WorkspaceID)
	if err := callback(ctx, "old-refresh", "new-access", "new-refresh", expiry); err != nil {
		t.Fatal(err)
	}
	if err := callback(ctx, "old-refresh", "new-access", "new-refresh", expiry); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	saved, err := f.store.GetProvider(f.ctx, record.Key)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Config.RefreshToken != "new-refresh" || saved.Config.APIKey != "new-access" || saved.Config.TokenExpiresAt != expiry.Format(time.RFC3339) || saved.Config.Model != "model" {
		t.Fatal("rotation did not preserve configuration and token expiry")
	}
	// A recreated source (as after restart) uses the persisted access token
	// rather than attempting to reuse the consumed refresh token.
	source := antropic.NewOAuthTokenSource(saved.Config.APIKey, saved.Config.RefreshToken, expiry, nil, nil)
	if token, err := source.Token(t.Context()); err != nil || token != "new-access" {
		t.Fatalf("restart token %q: %v", token, err)
	}
	if err := callback(ctx, "old-refresh", "stale-access", "stale-refresh", expiry); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("stale refresh overwrote new credentials: %v", err)
	}
	if err := f.s.claudeOAuthRefreshCallback(record.Key, "other-workspace")(ctx, "new-refresh", "bad", "bad", expiry); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("rotation escaped provider workspace: %v", err)
	}
}
