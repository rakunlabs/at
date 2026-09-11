package server

import (
	"context"
	"io"
	"net/http/httptest"
	"testing"

	"net/http"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres"
)

// legacyScopedRequest builds a request that already carries installation scope,
// mirroring what the platform-only admission middleware installs in production.
// Handler tests call handlers directly, so without it the scoped stores would
// correctly refuse the request for having no workspace at all.
func legacyScopedRequest(method, target string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, target, body)
	return r.WithContext(service.WithLegacyWorkspaceAccess(r.Context()))
}

// legacyScopedContext is the non-HTTP equivalent used by direct store setup.
func legacyScopedContext() context.Context {
	return service.WithLegacyWorkspaceAccess(context.Background())
}

// seedTestProvider creates the provider an agent config references. Agent
// bindings are validated against real, in-scope providers now, so fixtures can
// no longer name a provider key that was never configured.
func seedTestProvider(t *testing.T, store *postgres.Postgres, key string) service.ProviderRecord {
	t.Helper()
	p, err := store.CreateProvider(legacyScopedContext(), service.ProviderRecord{
		Key:    key,
		Config: config.LLMConfig{Type: "openai", APIKey: "test-key"},
	})
	if err != nil {
		t.Fatalf("CreateProvider(%q): %v", key, err)
	}
	return *p
}

// seedTestSkill creates the skill an agent's skill reference points at.
func seedTestSkill(t *testing.T, store *postgres.Postgres, name string) service.Skill {
	t.Helper()
	sk, err := store.CreateSkill(legacyScopedContext(), service.Skill{Name: name})
	if err != nil {
		t.Fatalf("CreateSkill(%q): %v", name, err)
	}
	return *sk
}
