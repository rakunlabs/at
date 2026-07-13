package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ─── Phase 2 Focused Tests ───
//
// These tests cover the security-sensitive invariants the Phase 2
// executors carry over from the HTTP handlers:
//
//   - Connection redaction policy (list always redacts; get respects
//     `reveal`)
//   - Provider api_key/refresh_token preservation on update
//   - API token plaintext returned exactly once on create
//   - Variable upsert-by-key semantics (mirrors HTTP)
//   - Bot create rejects the redaction placeholder
//
// The executors reuse the same store interfaces the HTTP handlers do,
// so the underlying CRUD is already validated by the store-level
// tests in internal/store/postgres. Here we only verify the executor
// glue: argument decoding, redaction, dispatch routing.

// ─── Variables ───

type fakeVariableStore struct {
	vars        map[string]*service.Variable
	created     []service.Variable
	updated     []service.Variable
	deleted     []string
	getByKeyHit map[string]bool
}

func newFakeVariableStore() *fakeVariableStore {
	return &fakeVariableStore{vars: map[string]*service.Variable{}, getByKeyHit: map[string]bool{}}
}

func (f *fakeVariableStore) ListVariables(_ context.Context, _ *query.Query) (*service.ListResult[service.Variable], error) {
	out := make([]service.Variable, 0, len(f.vars))
	for _, v := range f.vars {
		out = append(out, *v)
	}
	return &service.ListResult[service.Variable]{Data: out, Meta: service.ListMeta{Total: uint64(len(out))}}, nil
}
func (f *fakeVariableStore) GetVariable(_ context.Context, id string) (*service.Variable, error) {
	if v, ok := f.vars[id]; ok {
		return v, nil
	}
	return nil, nil
}
func (f *fakeVariableStore) GetVariableByKey(_ context.Context, key string) (*service.Variable, error) {
	f.getByKeyHit[key] = true
	for _, v := range f.vars {
		if v.Key == key {
			return v, nil
		}
	}
	return nil, nil
}
func (f *fakeVariableStore) CreateVariable(_ context.Context, v service.Variable) (*service.Variable, error) {
	f.created = append(f.created, v)
	if v.ID == "" {
		v.ID = "var-" + v.Key
	}
	stored := v
	f.vars[v.ID] = &stored
	return &stored, nil
}
func (f *fakeVariableStore) UpdateVariable(_ context.Context, id string, v service.Variable) (*service.Variable, error) {
	if _, ok := f.vars[id]; !ok {
		return nil, nil
	}
	v.ID = id
	f.updated = append(f.updated, v)
	stored := v
	f.vars[id] = &stored
	return &stored, nil
}
func (f *fakeVariableStore) DeleteVariable(_ context.Context, id string) error {
	delete(f.vars, id)
	f.deleted = append(f.deleted, id)
	return nil
}

// TestDispatch_VariableCreate_UpsertsByKey verifies the upsert
// behaviour: calling variable_create twice with the same key updates
// the existing record instead of erroring on duplicate. This mirrors
// CreateVariableAPI; without it, idempotent skill-install scripts
// would fail on re-run.
func TestDispatch_VariableCreate_UpsertsByKey(t *testing.T) {
	store := newFakeVariableStore()
	s := &Server{variableStore: store}
	ctx := context.Background()

	if _, err := s.dispatchBuiltinTool(ctx, "variable_create", map[string]any{
		"key":   "OPENAI_KEY",
		"value": "sk-first",
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if len(store.created) != 1 || store.created[0].Value != "sk-first" {
		t.Fatalf("first call should have created: %+v", store.created)
	}

	// Second call with same key should UPDATE, not create.
	if _, err := s.dispatchBuiltinTool(ctx, "variable_create", map[string]any{
		"key":    "OPENAI_KEY",
		"value":  "sk-second",
		"secret": true,
	}); err != nil {
		t.Fatalf("second create: %v", err)
	}
	if len(store.created) != 1 {
		t.Errorf("second call should not have created a duplicate, got %d created", len(store.created))
	}
	if len(store.updated) != 1 {
		t.Errorf("second call should have updated, got %d updates", len(store.updated))
	}
	if store.updated[0].Value != "sk-second" {
		t.Errorf("update value = %q, want %q", store.updated[0].Value, "sk-second")
	}
	if !store.updated[0].Secret {
		t.Error("secret flag should propagate to update")
	}
}

// TestDispatch_VariableList_RedactsSecrets verifies that secrets are
// redacted in list output. Single-record Get returns the plaintext —
// the LLM has to ask for a specific record by ID to see a secret,
// which leaves an audit trail.
func TestDispatch_VariableList_RedactsSecrets(t *testing.T) {
	store := newFakeVariableStore()
	store.vars["v1"] = &service.Variable{ID: "v1", Key: "PUBLIC", Value: "hello"}
	store.vars["v2"] = &service.Variable{ID: "v2", Key: "SECRET", Value: "supersecret", Secret: true}
	s := &Server{variableStore: store}

	out, err := s.dispatchBuiltinTool(context.Background(), "variable_list", nil)
	if err != nil {
		t.Fatalf("variable_list: %v", err)
	}
	if !strings.Contains(out, `"value": "hello"`) {
		t.Error("non-secret value should appear in plaintext")
	}
	if strings.Contains(out, "supersecret") {
		t.Error("secret value should not appear in list response")
	}
	if !strings.Contains(out, `"value": "***"`) {
		t.Error("secret should be redacted to ***")
	}

	// Get should return the unredacted value.
	getOut, err := s.dispatchBuiltinTool(context.Background(), "variable_get", map[string]any{"id": "v2"})
	if err != nil {
		t.Fatalf("variable_get: %v", err)
	}
	if !strings.Contains(getOut, "supersecret") {
		t.Error("variable_get should return unredacted value")
	}
}

// ─── Connections ───

type fakeConnectionStore struct {
	conns   map[string]*service.Connection
	created []service.Connection
}

func newFakeConnectionStore() *fakeConnectionStore {
	return &fakeConnectionStore{conns: map[string]*service.Connection{}}
}

func (f *fakeConnectionStore) ListConnections(_ context.Context, _ *query.Query) (*service.ListResult[service.Connection], error) {
	out := make([]service.Connection, 0, len(f.conns))
	for _, c := range f.conns {
		out = append(out, *c)
	}
	return &service.ListResult[service.Connection]{Data: out}, nil
}
func (f *fakeConnectionStore) ListConnectionsByProvider(_ context.Context, provider string) ([]service.Connection, error) {
	out := []service.Connection{}
	for _, c := range f.conns {
		if c.Provider == provider {
			out = append(out, *c)
		}
	}
	return out, nil
}
func (f *fakeConnectionStore) GetConnection(_ context.Context, id string) (*service.Connection, error) {
	if c, ok := f.conns[id]; ok {
		return c, nil
	}
	return nil, nil
}
func (f *fakeConnectionStore) GetConnectionByName(_ context.Context, provider, name string) (*service.Connection, error) {
	for _, c := range f.conns {
		if c.Provider == provider && c.Name == name {
			return c, nil
		}
	}
	return nil, nil
}
func (f *fakeConnectionStore) CreateConnection(_ context.Context, c service.Connection) (*service.Connection, error) {
	f.created = append(f.created, c)
	if c.ID == "" {
		c.ID = "conn-" + c.Provider + "-" + c.Name
	}
	stored := c
	f.conns[c.ID] = &stored
	return &stored, nil
}
func (f *fakeConnectionStore) UpdateConnection(_ context.Context, id string, c service.Connection) (*service.Connection, error) {
	if _, ok := f.conns[id]; !ok {
		return nil, nil
	}
	c.ID = id
	stored := c
	f.conns[id] = &stored
	return &stored, nil
}
func (f *fakeConnectionStore) DeleteConnection(_ context.Context, id string) error {
	delete(f.conns, id)
	return nil
}

// TestDispatch_ConnectionGet_RedactionPolicy is the headline security
// test: the redaction defaults must NOT leak. List always redacts;
// Get redacts unless reveal=true.
func TestDispatch_ConnectionGet_RedactionPolicy(t *testing.T) {
	store := newFakeConnectionStore()
	store.conns["c1"] = &service.Connection{
		ID:       "c1",
		Provider: "youtube",
		Name:     "Main Channel",
		Credentials: service.ConnectionCredentials{
			ClientID:     "client-123",
			ClientSecret: "secret-xyz-must-not-leak",
			RefreshToken: "rt-abc-must-not-leak",
			APIKey:       "ak-def-must-not-leak",
			Extra:        map[string]string{"webhook_url": "https://hook.example/secret"},
		},
	}
	s := &Server{connectionStore: store}

	// Default Get: redacted.
	defaultOut, err := s.dispatchBuiltinTool(context.Background(), "connection_get", map[string]any{"id": "c1"})
	if err != nil {
		t.Fatalf("connection_get default: %v", err)
	}
	for _, leak := range []string{"secret-xyz-must-not-leak", "rt-abc-must-not-leak", "ak-def-must-not-leak", "https://hook.example/secret"} {
		if strings.Contains(defaultOut, leak) {
			t.Errorf("default connection_get leaked secret %q", leak)
		}
	}
	// client_id is not a secret and should always be visible.
	if !strings.Contains(defaultOut, "client-123") {
		t.Error("client_id (non-secret) should be visible in default response")
	}
	// `*_set` markers must be present so the agent knows secrets exist.
	for _, marker := range []string{`"client_secret_set": true`, `"refresh_token_set": true`, `"api_key_set": true`} {
		if !strings.Contains(defaultOut, marker) {
			t.Errorf("expected redaction marker %q in default response", marker)
		}
	}

	// Reveal: plaintext.
	revealOut, err := s.dispatchBuiltinTool(context.Background(), "connection_get", map[string]any{
		"id":     "c1",
		"reveal": true,
	})
	if err != nil {
		t.Fatalf("connection_get reveal: %v", err)
	}
	for _, want := range []string{"secret-xyz-must-not-leak", "rt-abc-must-not-leak", "ak-def-must-not-leak"} {
		if !strings.Contains(revealOut, want) {
			t.Errorf("reveal=true response missing expected secret %q", want)
		}
	}

	// List: always redacted, even though we don't pass reveal anywhere.
	listOut, err := s.dispatchBuiltinTool(context.Background(), "connection_list", nil)
	if err != nil {
		t.Fatalf("connection_list: %v", err)
	}
	for _, leak := range []string{"secret-xyz-must-not-leak", "rt-abc-must-not-leak", "ak-def-must-not-leak"} {
		if strings.Contains(listOut, leak) {
			t.Errorf("connection_list leaked secret %q (list MUST always redact)", leak)
		}
	}
}

// TestDispatch_ConnectionUpdate_PreservesSecrets exercises the rule
// from UpdateConnectionAPI: empty secret fields preserve the stored
// values, so an agent can fetch+rename without re-supplying tokens.
func TestDispatch_ConnectionUpdate_PreservesSecrets(t *testing.T) {
	store := newFakeConnectionStore()
	store.conns["c1"] = &service.Connection{
		ID:       "c1",
		Provider: "youtube",
		Name:     "Old Name",
		Credentials: service.ConnectionCredentials{
			ClientID:     "client-123",
			ClientSecret: "stored-secret",
			RefreshToken: "stored-rt",
		},
	}
	s := &Server{connectionStore: store}

	// Update with empty credentials, only changing name.
	if _, err := s.dispatchBuiltinTool(context.Background(), "connection_update", map[string]any{
		"id":   "c1",
		"name": "New Name",
		// Note: no `credentials` field — empty map. Should preserve.
	}); err != nil {
		t.Fatalf("connection_update: %v", err)
	}

	got := store.conns["c1"]
	if got.Name != "New Name" {
		t.Errorf("name not updated: %q", got.Name)
	}
	if got.Credentials.ClientSecret != "stored-secret" {
		t.Errorf("client_secret was clobbered: %q", got.Credentials.ClientSecret)
	}
	if got.Credentials.RefreshToken != "stored-rt" {
		t.Errorf("refresh_token was clobbered: %q", got.Credentials.RefreshToken)
	}
}

// ─── Providers ───

type fakeProviderStore struct {
	providers     map[string]*service.ProviderRecord
	created       []service.ProviderRecord
	updated       []service.ProviderRecord
	listCallCount int
}

func newFakeProviderStore() *fakeProviderStore {
	return &fakeProviderStore{providers: map[string]*service.ProviderRecord{}}
}

func (f *fakeProviderStore) ListProviders(_ context.Context, _ *query.Query) (*service.ListResult[service.ProviderRecord], error) {
	f.listCallCount++
	out := make([]service.ProviderRecord, 0, len(f.providers))
	for _, p := range f.providers {
		out = append(out, *p)
	}
	return &service.ListResult[service.ProviderRecord]{Data: out, Meta: service.ListMeta{Total: uint64(len(out))}}, nil
}
func (f *fakeProviderStore) GetProvider(_ context.Context, key string) (*service.ProviderRecord, error) {
	if p, ok := f.providers[key]; ok {
		// Hand the caller a defensive copy so redactProviderRecord
		// can't mutate our store.
		copy := *p
		return &copy, nil
	}
	return nil, nil
}
func (f *fakeProviderStore) CreateProvider(_ context.Context, p service.ProviderRecord) (*service.ProviderRecord, error) {
	f.created = append(f.created, p)
	stored := p
	f.providers[p.Key] = &stored
	returned := p
	return &returned, nil
}
func (f *fakeProviderStore) UpdateProvider(_ context.Context, key string, p service.ProviderRecord) (*service.ProviderRecord, error) {
	if _, ok := f.providers[key]; !ok {
		return nil, nil
	}
	p.Key = key
	f.updated = append(f.updated, p)
	// Mirror real-store semantics: the stored record and the returned
	// record are independent values. Otherwise an executor that
	// mutates the response (e.g. via redactProviderRecord) would
	// silently corrupt the in-memory store and our test would fire
	// false positives.
	stored := p
	f.providers[key] = &stored
	returned := p
	return &returned, nil
}
func (f *fakeProviderStore) DeleteProvider(_ context.Context, key string) error {
	delete(f.providers, key)
	return nil
}

// TestDispatch_ProviderUpdate_PreservesAPIKey is the security-critical
// invariant: when provider_update arrives with api_key="" (because
// the LLM round-tripped a redacted provider_get response), the
// existing stored key must be preserved. Without this, every "edit
// the rate limit" call would silently nuke the API key.
func TestDispatch_ProviderUpdate_PreservesAPIKey(t *testing.T) {
	store := newFakeProviderStore()
	store.providers["openai-prod"] = &service.ProviderRecord{
		Key: "openai-prod",
	}
	store.providers["openai-prod"].Config.Type = "openai"
	store.providers["openai-prod"].Config.APIKey = "sk-real-key"
	store.providers["openai-prod"].Config.RefreshToken = "rt-real"

	s := &Server{}
	s.store = store

	// Update with empty api_key/refresh_token — should preserve existing.
	if _, err := s.dispatchBuiltinTool(context.Background(), "provider_update", map[string]any{
		"key": "openai-prod",
		"config": map[string]any{
			"type":  "openai",
			"model": "gpt-4o",
			// note: api_key and refresh_token deliberately omitted/empty
		},
	}); err != nil {
		t.Fatalf("provider_update: %v", err)
	}

	got := store.providers["openai-prod"]
	if got.Config.APIKey != "sk-real-key" {
		t.Errorf("api_key was clobbered to %q (must preserve when empty)", got.Config.APIKey)
	}
	if got.Config.RefreshToken != "rt-real" {
		t.Errorf("refresh_token was clobbered to %q (must preserve when empty)", got.Config.RefreshToken)
	}
	if got.Config.Model != "gpt-4o" {
		t.Errorf("non-secret field should be updated: model = %q", got.Config.Model)
	}
}

// TestDispatch_ProviderCreate_RejectsDuplicateKey mirrors the HTTP
// 409 behaviour. Without this check, two creates would silently
// overwrite each other and lose history.
func TestDispatch_ProviderCreate_RejectsDuplicateKey(t *testing.T) {
	store := newFakeProviderStore()
	store.providers["existing"] = &service.ProviderRecord{Key: "existing"}
	s := &Server{}
	s.store = store

	_, err := s.dispatchBuiltinTool(context.Background(), "provider_create", map[string]any{
		"key": "existing",
		"config": map[string]any{
			"type": "openai",
		},
	})
	if err == nil {
		t.Fatal("expected error on duplicate key, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// ─── API Tokens ───

type fakeAPITokenStore struct {
	tokens  map[string]*service.APIToken
	created []service.APIToken
	hashes  map[string]string // id → hash, captured on create
}

func newFakeAPITokenStore() *fakeAPITokenStore {
	return &fakeAPITokenStore{tokens: map[string]*service.APIToken{}, hashes: map[string]string{}}
}

func (f *fakeAPITokenStore) ListAPITokens(_ context.Context, _ *query.Query) (*service.ListResult[service.APIToken], error) {
	out := make([]service.APIToken, 0, len(f.tokens))
	for _, t := range f.tokens {
		out = append(out, *t)
	}
	return &service.ListResult[service.APIToken]{Data: out}, nil
}
func (f *fakeAPITokenStore) GetAPIToken(_ context.Context, id string) (*service.APIToken, error) {
	if t, ok := f.tokens[id]; ok {
		return t, nil
	}
	return nil, nil
}
func (f *fakeAPITokenStore) GetAPITokenByHash(_ context.Context, _ string) (*service.APIToken, error) {
	return nil, nil
}
func (f *fakeAPITokenStore) CreateAPIToken(_ context.Context, t service.APIToken, hash string) (*service.APIToken, error) {
	if t.ID == "" {
		t.ID = "tok-" + t.Name
	}
	f.created = append(f.created, t)
	f.hashes[t.ID] = hash
	stored := t
	f.tokens[t.ID] = &stored
	return &stored, nil
}
func (f *fakeAPITokenStore) UpdateAPIToken(_ context.Context, id string, t service.APIToken) (*service.APIToken, error) {
	if _, ok := f.tokens[id]; !ok {
		return nil, nil
	}
	t.ID = id
	stored := t
	f.tokens[id] = &stored
	return &stored, nil
}
func (f *fakeAPITokenStore) DeleteAPIToken(_ context.Context, id string) error {
	delete(f.tokens, id)
	return nil
}
func (f *fakeAPITokenStore) UpdateLastUsed(_ context.Context, _ string) error { return nil }

// TestDispatch_APITokenCreate_ReturnsPlaintextOnce verifies the
// "shown only once" contract: the create response contains the full
// `at_<64hex>` token, but the stored record only has the prefix and
// a hash of the plaintext.
func TestDispatch_APITokenCreate_ReturnsPlaintextOnce(t *testing.T) {
	store := newFakeAPITokenStore()
	s := &Server{tokenStore: store}

	out, err := s.dispatchBuiltinTool(context.Background(), "apitoken_create", map[string]any{
		"name": "ci-bot",
	})
	if err != nil {
		t.Fatalf("apitoken_create: %v", err)
	}

	var resp struct {
		Token string           `json:"token"`
		Info  service.APIToken `json:"info"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}

	if !strings.HasPrefix(resp.Token, "at_") {
		t.Errorf("token should start with at_, got %q", resp.Token)
	}
	if len(resp.Token) != 67 { // "at_" + 64 hex chars
		t.Errorf("token length = %d, want 67", len(resp.Token))
	}
	if resp.Info.TokenPrefix != resp.Token[:8] {
		t.Errorf("token_prefix = %q, want first 8 chars of token (%q)", resp.Info.TokenPrefix, resp.Token[:8])
	}

	// Stored record should have the prefix but NOT the plaintext.
	if len(store.created) != 1 {
		t.Fatalf("expected 1 created token, got %d", len(store.created))
	}
	stored := store.created[0]
	if stored.TokenPrefix == "" {
		t.Error("stored record missing token_prefix")
	}
	// The stored hash must NOT equal the plaintext, otherwise we've
	// regressed to plaintext storage.
	hashStored := store.hashes[resp.Info.ID]
	if hashStored == resp.Token {
		t.Fatal("stored hash equals plaintext token — security regression: token is being stored as plaintext")
	}
	if len(hashStored) != 64 { // sha256 hex
		t.Errorf("hash length = %d, want 64 (sha256 hex)", len(hashStored))
	}
}

// TestDispatch_APITokenCreate_TwoCallsProduceDifferentTokens guards
// against a degenerate randomness regression where rand.Read returns
// the same bytes on every call.
func TestDispatch_APITokenCreate_TwoCallsProduceDifferentTokens(t *testing.T) {
	s := &Server{tokenStore: newFakeAPITokenStore()}
	out1, err := s.dispatchBuiltinTool(context.Background(), "apitoken_create", map[string]any{"name": "a"})
	if err != nil {
		t.Fatalf("create 1: %v", err)
	}
	out2, err := s.dispatchBuiltinTool(context.Background(), "apitoken_create", map[string]any{"name": "b"})
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}
	var r1, r2 struct {
		Token string `json:"token"`
	}
	json.Unmarshal([]byte(out1), &r1)
	json.Unmarshal([]byte(out2), &r2)
	if r1.Token == r2.Token {
		t.Fatal("two consecutive token creations produced the same plaintext — randomness is broken")
	}
}

// ─── Bots ───

// TestDispatch_BotCreate_RejectsRedactedToken guards against the
// "round-trip a bot_get into bot_create" footgun. If we accepted the
// "***redacted***" placeholder, the new bot would store an unusable
// token and silently never poll.
func TestDispatch_BotCreate_RejectsRedactedToken(t *testing.T) {
	s := &Server{botConfigStore: nil} // store check happens BEFORE token check
	// We need a real-ish store to get past the nil guard. A lightweight
	// in-memory mock would work but for this single check we can rely
	// on the order of validation: store-nil → "store not configured".
	// To exercise the redacted-token branch specifically we need a
	// non-nil store.
	type minimalBotStore struct{ service.BotConfigStorer }
	s.botConfigStore = minimalBotStore{}

	_, err := s.dispatchBuiltinTool(context.Background(), "bot_create", map[string]any{
		"platform": "telegram",
		"token":    botTokenRedacted, // the placeholder
	})
	if err == nil {
		t.Fatal("expected error when token is the redaction placeholder")
	}
	if !strings.Contains(err.Error(), "redaction placeholder") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ─── Generic helpers ───

// TestOptionalInt64_AcceptsJSONNumbers covers the float64-from-JSON
// case, which is what real MCP arguments arrive as.
func TestOptionalInt64_AcceptsJSONNumbers(t *testing.T) {
	args := map[string]any{"a": float64(42), "b": int64(7), "c": int(3), "d": "string", "e": nil}
	if v := optionalInt64(args, "a"); v == nil || *v != 42 {
		t.Errorf("float64 case: got %v", v)
	}
	if v := optionalInt64(args, "b"); v == nil || *v != 7 {
		t.Errorf("int64 case: got %v", v)
	}
	if v := optionalInt64(args, "c"); v == nil || *v != 3 {
		t.Errorf("int case: got %v", v)
	}
	if v := optionalInt64(args, "d"); v != nil {
		t.Errorf("string should yield nil, got %v", v)
	}
	if v := optionalInt64(args, "e"); v != nil {
		t.Errorf("nil should yield nil, got %v", v)
	}
	if v := optionalInt64(args, "missing"); v != nil {
		t.Errorf("missing should yield nil, got %v", v)
	}
}
