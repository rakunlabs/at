package server

import "testing"

func TestAgentPersonalRoutesAreCapabilityAdmitted(t *testing.T) {
	want := map[string]string{
		"GET /agents":                  "agents.read",
		"POST /agents":                 "agents.read",
		"POST /agents/import":          "agents.read",
		"POST /agents/import/preview":  "agents.read",
		"GET /agents/{id}":             "agents.read",
		"PUT /agents/{id}":             "agents.read",
		"DELETE /agents/{id}":          "agents.read",
		"POST /agents/{id}/publish":    "agents.write",
		"GET /agents/{id}/export":      "agents.read",
		"GET /agents/{id}/export-json": "agents.read",
	}
	for _, policy := range workspaceBusinessPolicies() {
		key := policy.Method + " " + policy.Pattern
		if capability, ok := want[key]; ok {
			if policy.Capability != capability {
				t.Fatalf("%s capability = %q, want %q", key, policy.Capability, capability)
			}
			delete(want, key)
		}
	}
	for route := range want {
		t.Errorf("missing workspace business policy for %s", route)
	}
}
