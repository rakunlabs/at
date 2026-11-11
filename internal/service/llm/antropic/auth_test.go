package antropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestBuildAuthURLMatchesCurrentClaudeCode(t *testing.T) {
	got := BuildAuthURL("challenge", "state")
	if !strings.HasPrefix(got, "https://claude.com/cai/oauth/authorize?") {
		t.Fatalf("authorization URL = %q", got)
	}
	for _, want := range []string{"org%3Acreate_api_key", "user%3Aplugins", "user%3Ainference"} {
		if !strings.Contains(got, want) {
			t.Errorf("authorization URL missing %q: %s", want, got)
		}
	}
}

func TestExchangeAuthCodeUsesJSONAndValidatesState(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: oauthRefreshTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q", got)
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["code"] != "code" || payload["state"] != "state" || payload["code_verifier"] != "verifier" {
			t.Fatalf("unexpected exchange payload: %#v", payload)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"access_token":"access","refresh_token":"refresh","expires_in":3600}`))}, nil
	})}

	resp, err := ExchangeAuthCode(context.Background(), "code#state", "state", "verifier", ClaudeManualURI, client)
	if err != nil || resp.AccessToken != "access" {
		t.Fatalf("ExchangeAuthCode() = %#v, %v", resp, err)
	}
	if _, err := ExchangeAuthCode(context.Background(), "code#wrong", "state", "verifier", ClaudeManualURI, client); err == nil {
		t.Fatal("expected state mismatch")
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestClaudeCodeVersionOverride(t *testing.T) {
	t.Setenv("ANTHROPIC_CLI_VERSION", "9.9.9")
	if got := claudeCodeVersion(); got != "9.9.9" {
		t.Fatalf("claudeCodeVersion() = %q", got)
	}
}

func TestAdoptRequiredClaudeCodeVersion(t *testing.T) {
	t.Cleanup(func() { learnedClaudeCodeVersion.Store("") })
	learnedClaudeCodeVersion.Store("")

	if adoptRequiredClaudeCodeVersion("model: claude-x not found") {
		t.Fatal("unrelated error adopted a version")
	}
	if adoptRequiredClaudeCodeVersion("Claude Code 2.1.280 does not support this model; version 2.1.100 or newer is required.") {
		t.Fatal("older required version must not be adopted")
	}
	msg := "Claude Code 2.1.322 does not support this model; version 2.1.400 or newer is required. Run 'claude update'"
	if !adoptRequiredClaudeCodeVersion(msg) {
		t.Fatal("newer required version was not adopted")
	}
	if got := claudeCodeVersion(); got != "2.1.400" {
		t.Fatalf("claudeCodeVersion() = %q, want 2.1.400", got)
	}
	if adoptRequiredClaudeCodeVersion(msg) {
		t.Fatal("same version adopted twice; a retry loop would not terminate")
	}
}

func TestCompareCLIVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"2.1.290", "2.1.280", 1},
		{"2.1.280", "2.1.290", -1},
		{"2.1.9", "2.1.10", -1},
		{"2.1", "2.1.0", 0},
	}
	for _, tt := range tests {
		if got := compareCLIVersions(tt.a, tt.b); got != tt.want {
			t.Errorf("compareCLIVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestOAuthBetaHeaderMatchesCurrentClaudeCode(t *testing.T) {
	got := strings.Split(oauthBetaHeader("claude-sonnet-4-6", nil), ",")
	want := []string{
		"claude-code-20250219",
		"interleaved-thinking-2025-05-14",
		"thinking-token-count-2026-05-13",
		"oauth-2025-04-20",
		"prompt-caching-scope-2026-01-05",
		"context-management-2025-06-27",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("OAuth betas = %v, want %v", got, want)
	}
	haiku := oauthBetaHeader("claude-haiku-4-5", nil)
	for _, excluded := range []string{"claude-code-20250219", "interleaved-thinking-2025-05-14", "thinking-token-count-2026-05-13"} {
		if strings.Contains(haiku, excluded) {
			t.Errorf("Haiku betas unexpectedly contain %q: %s", excluded, haiku)
		}
	}
}
