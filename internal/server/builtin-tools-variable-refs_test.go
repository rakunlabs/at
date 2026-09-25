package server

import (
	"net/url"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
)

func TestResolveHTTPHeaderVariableReference(t *testing.T) {
	store := newFakeVariableStore()
	store.vars["secret"] = &service.Variable{
		ID: "secret", Key: "github_token", Value: "token-value", Secret: true,
		AllowedTools: []string{service.VariableUseToolHTTPRequest}, AllowedHosts: []string{"api.github.test"},
	}
	s := &Server{variableStore: store}
	target, err := url.Parse("https://api.github.test/issues")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := s.resolveHTTPHeaderValue(executiontest.Context(t), target, map[string]any{
		"$ref": "variable://github_token", "prefix": "Bearer ",
	})
	if err != nil {
		t.Fatalf("resolveHTTPHeaderValue: %v", err)
	}
	if resolved.Value != "Bearer token-value" || resolved.Secret != "token-value" {
		t.Fatalf("resolved = %#v", resolved)
	}
	if got := redactResolvedSecrets("echo token-value", []string{resolved.Secret}); got != "echo ***" {
		t.Fatalf("redacted = %q", got)
	}
}

func TestResolveHTTPHeaderVariableReferenceRejectsUnsafeUse(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		variable service.Variable
		want     string
	}{
		{name: "plain HTTP", target: "http://api.example.test", variable: service.Variable{Secret: true, AllowedTools: []string{"http_request"}, AllowedHosts: []string{"api.example.test"}}, want: "HTTPS"},
		{name: "host denied", target: "https://evil.example.test", variable: service.Variable{Secret: true, AllowedTools: []string{"http_request"}, AllowedHosts: []string{"api.example.test"}}, want: "not allowed"},
		{name: "tool denied", target: "https://api.example.test", variable: service.Variable{Secret: true, AllowedHosts: []string{"api.example.test"}}, want: "not allowed"},
		{name: "not secret", target: "https://api.example.test", variable: service.Variable{AllowedTools: []string{"http_request"}, AllowedHosts: []string{"api.example.test"}}, want: "not secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeVariableStore()
			tt.variable.ID, tt.variable.Key, tt.variable.Value = "v", "token", "value"
			store.vars["v"] = &tt.variable
			target, err := url.Parse(tt.target)
			if err != nil {
				t.Fatal(err)
			}
			_, err = (&Server{variableStore: store}).resolveHTTPHeaderValue(executiontest.Context(t), target, map[string]any{"$ref": "variable://token"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}
