package server

import (
	"reflect"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
	"github.com/rakunlabs/at/internal/service/workflow"
)

func TestInlineToolVariableKeys(t *testing.T) {
	handler := `
var token = getVar('pexels_api_key');
API_KEY="${VAR_FAL_API_KEY:-${VAR_FAL_KEY:-}}"
var again = getVar("pexels_api_key");
`
	want := []string{"fal_api_key", "fal_key", "pexels_api_key"}
	if got := inlineToolVariableKeys(handler); !reflect.DeepEqual(got, want) {
		t.Fatalf("inlineToolVariableKeys() = %#v, want %#v", got, want)
	}
}

func TestInlineToolVariablesPrefersAgentConnection(t *testing.T) {
	variables := newFakeVariableStore()
	variables.vars["global-refresh"] = &service.Variable{ID: "global-refresh", Key: "youtube_refresh_token", Value: "stale-global"}
	connections := newFakeConnectionStore()
	connections.conns["agent-youtube"] = &service.Connection{
		ID:       "agent-youtube",
		Provider: "youtube",
		Credentials: service.ConnectionCredentials{
			RefreshToken: "fresh-agent",
		},
	}
	connections.conns["skill-youtube"] = &service.Connection{
		ID:       "skill-youtube",
		Provider: "youtube",
		Credentials: service.ConnectionCredentials{
			RefreshToken: "fresh-skill",
		},
	}

	s := &Server{variableStore: variables, connectionStore: connections}
	tool := service.MCPInlineTool{
		SourceSkillID: "youtube-skill-id",
		Handler:       `getVar('youtube_refresh_token')`,
	}

	tests := []struct {
		name   string
		skills map[string]map[string]string
		want   string
	}{
		{name: "agent default", want: "fresh-agent"},
		{name: "skill override", skills: map[string]map[string]string{"youtube-skill-id": {"youtube": "skill-youtube"}}, want: "fresh-skill"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := workflow.ContextWithAgentConnections(executiontest.Context(t), map[string]string{"youtube": "agent-youtube"}, tt.skills)
			lookup, _, err := s.inlineToolVariables(ctx, tool)
			if err != nil {
				t.Fatal(err)
			}
			got, err := lookup("youtube_refresh_token")
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("refresh token = %q, want %q", got, tt.want)
			}
		})
	}
	if variables.getByKeyHit["youtube_refresh_token"] {
		t.Fatal("bound connection unexpectedly fell back to the stale global variable")
	}
}
