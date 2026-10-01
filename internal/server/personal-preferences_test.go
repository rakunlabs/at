package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestPersonalPreferencesPrompt(t *testing.T) {
	prefs := []service.UserPreference{
		{Key: "timezone", Value: json.RawMessage(`"Europe/Istanbul"`)},
		{Key: "language", Value: json.RawMessage(`"tr"`)},
		{Key: "location", Value: json.RawMessage(`{"city":"Istanbul"}`)},
		{Key: "timezone", Secret: true, Value: json.RawMessage(`"secret"`)},
		{Key: "developer_home", Value: json.RawMessage(`{"enabled":true}`)},
		{Key: "playground_presets", Value: json.RawMessage(`[{"system_prompt":"private"}]`)},
		{Key: "local_mcp_servers", Secret: true, Value: json.RawMessage(`[{"token":"secret"}]`)},
		{Key: "future_app_setting", Value: json.RawMessage(`"private"`)},
	}
	personal := personalUserPreferences(prefs)
	if len(personal) != 3 || len(prefs) != 8 {
		t.Fatalf("unexpected filtered preferences: %+v", personal)
	}
	want := "User preferences:\n- timezone: \"Europe/Istanbul\"\n- language: \"tr\"\n- location: {\"city\":\"Istanbul\"}"
	if got := appendPersonalPreferencesPrompt("", prefs); got != want {
		t.Fatalf("prompt = %q, want %q", got, want)
	}
	if got := appendPersonalPreferencesPrompt("base", prefs); got != "base\n\n"+want {
		t.Fatalf("prompt = %q", got)
	}
	for _, hidden := range [][]service.UserPreference{nil, prefs[3:]} {
		if got := appendPersonalPreferencesPrompt("base", hidden); got != "base" {
			t.Fatalf("hidden preferences changed prompt: %q", got)
		}
	}
}

func TestChatSessionInjectsOnlyPersonalPreferences(t *testing.T) {
	provider := &fakeObsProvider{responses: []*service.LLMResponse{{Content: "done", Finished: true}}}
	s, _ := newObsTestServer(t, provider, &fakeLLMCallStore{}, map[string]*service.Agent{
		"a": {ID: "a", Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 2, SystemPrompt: "base"}},
	}, nil)
	s.chatSessionStore = &fakeChatSessionStore{session: service.ChatSession{ID: "session", AgentID: "a"}}
	prefs := &personalPreferenceStore{prefs: []service.UserPreference{
		{Key: "timezone", Value: json.RawMessage(`"Europe/Istanbul"`)},
		{Key: "language", Value: json.RawMessage(`"tr"`)},
		{Key: "location", Secret: true, Value: json.RawMessage(`"secret-location"`)},
		{Key: "developer_home", Value: json.RawMessage(`{"private":"app-settings"}`)},
	}}
	s.userPrefStore = prefs
	if err := s.RunAgenticLoop(s.ctx, "session", "hello", func(AgenticEvent) {}); err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 1 || len(provider.requests[0]) == 0 || provider.requests[0][0].Role != "system" {
		t.Fatalf("unexpected provider requests: %+v", provider.requests)
	}
	prompt, ok := provider.requests[0][0].Content.(string)
	if !ok || !strings.Contains(prompt, `- timezone: "Europe/Istanbul"`) || !strings.Contains(prompt, `- language: "tr"`) {
		t.Fatalf("missing personal preferences: %q", prompt)
	}
	for _, hidden := range []string{"secret-location", "developer_home", "app-settings"} {
		if strings.Contains(prompt, hidden) {
			t.Fatalf("hidden preference leaked into provider request: %s", hidden)
		}
	}
	if prefs.user == "" {
		t.Fatal("preferences loaded without a bound account")
	}
}
