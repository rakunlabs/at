package workflow

import (
	"context"
	"fmt"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestResolveConnectionKey(t *testing.T) {
	tests := []struct {
		key          string
		wantProvider string
		wantSuffix   string
		wantOK       bool
	}{
		{"youtube_refresh_token", "youtube", "_refresh_token", true},
		{"youtube_client_id", "youtube", "_client_id", true},
		{"google_client_secret", "google", "_client_secret", true},
		{"openai_api_key", "openai", "_api_key", true},
		{"random_variable", "", "", false},
		{"_refresh_token", "", "", false},
		{"", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			gotP, gotS, gotOK := ResolveConnectionKey(tt.key)
			if gotP != tt.wantProvider || gotS != tt.wantSuffix || gotOK != tt.wantOK {
				t.Errorf("got (%q, %q, %v), want (%q, %q, %v)",
					gotP, gotS, gotOK, tt.wantProvider, tt.wantSuffix, tt.wantOK)
			}
		})
	}
}

func TestWrapVarLookupWithConnections_PrefersBinding(t *testing.T) {
	base := func(key string) (string, error) {
		if key == "youtube_refresh_token" {
			return "GLOBAL", nil
		}
		return "", fmt.Errorf("not found: %s", key)
	}
	bindings := ConnectionBindings{
		"youtube": &service.Connection{
			Provider: "youtube",
			Credentials: service.ConnectionCredentials{
				RefreshToken: "FROM-CONNECTION",
			},
		},
	}
	wrapped := WrapVarLookupWithConnections(base, bindings)
	got, err := wrapped("youtube_refresh_token")
	if err != nil {
		t.Fatalf("wrapped lookup: %v", err)
	}
	if got != "FROM-CONNECTION" {
		t.Errorf("got %q, want %q", got, "FROM-CONNECTION")
	}
}

func TestWrapVarLookupWithConnections_FallsBack(t *testing.T) {
	base := func(key string) (string, error) {
		switch key {
		case "youtube_refresh_token":
			return "GLOBAL", nil
		case "totally_unrelated":
			return "PASSTHROUGH", nil
		}
		return "", fmt.Errorf("not found: %s", key)
	}
	// Binding for google, not youtube — youtube should fall through to base.
	bindings := ConnectionBindings{
		"google": &service.Connection{
			Credentials: service.ConnectionCredentials{RefreshToken: "G"},
		},
	}
	wrapped := WrapVarLookupWithConnections(base, bindings)

	got, _ := wrapped("youtube_refresh_token")
	if got != "GLOBAL" {
		t.Errorf("youtube: got %q, want GLOBAL", got)
	}
	got, _ = wrapped("totally_unrelated")
	if got != "PASSTHROUGH" {
		t.Errorf("unrelated: got %q, want PASSTHROUGH", got)
	}
}

func TestWrapVarLookupWithConnections_EmptyCredentialFallsBack(t *testing.T) {
	// Connection is bound for youtube but has no refresh_token — the wrapper
	// must fall back to the base rather than returning an empty string.
	base := func(key string) (string, error) {
		if key == "youtube_refresh_token" {
			return "FALLBACK", nil
		}
		return "", nil
	}
	bindings := ConnectionBindings{
		"youtube": &service.Connection{
			Credentials: service.ConnectionCredentials{
				ClientID: "id-only",
			},
		},
	}
	wrapped := WrapVarLookupWithConnections(base, bindings)

	// refresh_token is not set on the connection -> fall through.
	got, _ := wrapped("youtube_refresh_token")
	if got != "FALLBACK" {
		t.Errorf("got %q, want FALLBACK", got)
	}
	// client_id IS set -> use the connection value.
	got, _ = wrapped("youtube_client_id")
	if got != "id-only" {
		t.Errorf("got %q, want id-only", got)
	}
}

func TestWrapVarListerWithConnections_Overlays(t *testing.T) {
	base := func() (map[string]string, error) {
		return map[string]string{
			"youtube_refresh_token": "GLOBAL",
			"other_key":             "untouched",
		}, nil
	}
	bindings := ConnectionBindings{
		"youtube": &service.Connection{
			Credentials: service.ConnectionCredentials{
				RefreshToken: "BOUND",
				ClientID:     "cid",
			},
		},
	}
	wrapped := WrapVarListerWithConnections(base, bindings)
	got, err := wrapped()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got["youtube_refresh_token"] != "BOUND" {
		t.Errorf("refresh_token overlay: got %q", got["youtube_refresh_token"])
	}
	if got["youtube_client_id"] != "cid" {
		t.Errorf("client_id overlay: got %q", got["youtube_client_id"])
	}
	if got["other_key"] != "untouched" {
		t.Errorf("passthrough: got %q", got["other_key"])
	}
}

func TestResolveAgentConnectionBindings_SkillOverride(t *testing.T) {
	agentConn := &service.Connection{ID: "conn-agent", Provider: "youtube",
		Credentials: service.ConnectionCredentials{RefreshToken: "agent-token"}}
	skillConn := &service.Connection{ID: "conn-skill", Provider: "youtube",
		Credentials: service.ConnectionCredentials{RefreshToken: "skill-token"}}

	store := map[string]*service.Connection{
		"conn-agent": agentConn,
		"conn-skill": skillConn,
	}
	lookup := ConnectionLookup(func(_ context.Context, id string) (*service.Connection, error) {
		return store[id], nil
	})

	// Skill override wins.
	bindings := ResolveAgentConnectionBindings(context.Background(), lookup,
		map[string]string{"youtube": "conn-agent"},
		map[string]string{"youtube": "conn-skill"},
	)
	if bindings["youtube"].ID != "conn-skill" {
		t.Errorf("override: got %q, want conn-skill", bindings["youtube"].ID)
	}

	// No skill override: agent-level binding applies.
	bindings = ResolveAgentConnectionBindings(context.Background(), lookup,
		map[string]string{"youtube": "conn-agent"},
		nil,
	)
	if bindings["youtube"].ID != "conn-agent" {
		t.Errorf("agent binding: got %q, want conn-agent", bindings["youtube"].ID)
	}

	// Unknown connection ID: silently skipped.
	bindings = ResolveAgentConnectionBindings(context.Background(), lookup,
		map[string]string{"youtube": "conn-missing"},
		nil,
	)
	if len(bindings) != 0 {
		t.Errorf("missing lookup: expected empty, got %v", bindings)
	}
}
