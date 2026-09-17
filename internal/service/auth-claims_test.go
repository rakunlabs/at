package service

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestValidateClaimPath(t *testing.T) {
	for _, tt := range []struct {
		name, path string
		invalid    bool
	}{
		{name: "flat", path: "roles"},
		{name: "keycloak realm", path: "realm_access.roles"},
		{name: "keycloak clients", path: "resource_access.*.roles"},
		{name: "namespaced", path: "https://at.example/roles"},
		{name: "empty", path: "", invalid: true},
		{name: "empty segment", path: "realm_access..roles", invalid: true},
		{name: "trailing dot", path: "realm_access.", invalid: true},
		{name: "spaced", path: "realm access.roles", invalid: true},
		{name: "padded", path: " roles", invalid: true},
		{name: "too deep", path: "a.b.c.d.e.f.g.h.i", invalid: true},
		{name: "too long", path: strings.Repeat("a", 257), invalid: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateClaimPath(tt.path); (err != nil) != tt.invalid {
				t.Fatalf("path %q: %v", tt.path, err)
			}
		})
	}
	if err := ValidateClaimPaths([]string{"roles", "roles"}); err == nil {
		t.Fatal("duplicate paths accepted")
	}
	many := make([]string, AuthClaimPathsMax+1)
	for i := range many {
		many[i] = strings.Repeat("a", i+1)
	}
	if err := ValidateClaimPaths(many); err == nil {
		t.Fatal("unbounded path set accepted")
	}
}

func TestHarvestClaimValues(t *testing.T) {
	// Real Keycloak shape: realm roles plus one role set per client.
	raw := `{
		"realm_access": {"roles": ["at-admin", "offline_access"]},
		"resource_access": {"at": {"roles": ["editor"]}, "other": {"roles": ["viewer", "editor"]}},
		"groups": "platform  writers",
		"scope": "openid profile",
		"tenant": {"id": 7, "flags": [true, "kept"]},
		"nested": {"deep": {"a": {"b": {"c": {"d": {"e": {"f": ["too-deep"]}}}}}}}
	}`
	var claims map[string]any
	if err := json.Unmarshal([]byte(raw), &claims); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name  string
		paths []string
		want  []string
	}{
		{name: "realm roles", paths: []string{"realm_access.roles"}, want: []string{"at-admin", "offline_access"}},
		{name: "client wildcard", paths: []string{"resource_access.*.roles"}, want: []string{"editor", "viewer"}},
		{name: "space delimited string", paths: []string{"groups"}, want: []string{"platform", "writers"}},
		{name: "union deduplicates", paths: []string{"realm_access.roles", "resource_access.at.roles"}, want: []string{"at-admin", "offline_access", "editor"}},
		{name: "non-string leaves skipped", paths: []string{"tenant.id"}, want: nil},
		{name: "mixed array keeps strings", paths: []string{"tenant.flags"}, want: []string{"kept"}},
		{name: "missing path", paths: []string{"realm_access.groups"}, want: nil},
		{name: "literal segment never indexes an array", paths: []string{"tenant.flags.0"}, want: nil},
		{name: "invalid path ignored", paths: []string{"realm_access..roles"}, want: nil},
		{name: "maximum depth reachable", paths: []string{"nested.deep.a.b.c.d.e.f"}, want: []string{"too-deep"}},
		{name: "beyond maximum depth rejected", paths: []string{"nested.deep.a.b.c.d.e.f.g"}, want: nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := HarvestClaimValues(claims, tt.paths)
			if tt.name == "client wildcard" {
				// Map iteration order is unspecified; compare as a set.
				slices.Sort(got)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("harvest %v = %v, want %v", tt.paths, got, tt.want)
			}
		})
	}
}

func TestHarvestClaimValuesBounded(t *testing.T) {
	values := make([]any, 0, 4096)
	for i := range 4096 {
		values = append(values, strings.Repeat("r", 64)+string(rune('a'+i%26))+string(rune('a'+i/26%26)))
	}
	claims := map[string]any{"roles": values}
	got := HarvestClaimValues(claims, []string{"roles"})
	if len(got) == 0 || len(got) > AuthClaimValuesMax {
		t.Fatalf("value count %d", len(got))
	}
	total := 0
	for _, value := range got {
		total += len(value)
	}
	if total > authClaimBytesMax {
		t.Fatalf("harvested %d bytes", total)
	}
	long := strings.Repeat("x", authClaimValueBytesMax+1)
	if got := HarvestClaimValues(map[string]any{"roles": []any{long}}, []string{"roles"}); got != nil {
		t.Fatalf("oversized value kept: %v", got)
	}
	merged := MergeClaimValues([]string{"a", "a", ""}, []string{"b", long})
	if !slices.Equal(merged, []string{"a", "b"}) {
		t.Fatalf("merge = %v", merged)
	}
	// Reported roles are never truncated: they already worked without paths.
	reported := make([]string, 0, 256)
	for i := range 256 {
		reported = append(reported, strings.Repeat("r", 64)+string(rune('a'+i%26))+string(rune('a'+i/26%26)))
	}
	if merged := MergeClaimValues(reported, nil); len(merged) != len(reported) {
		t.Fatalf("reported roles truncated to %d", len(merged))
	}
	if MergeClaimValues(nil, nil) != nil {
		t.Fatal("empty merge allocated a value")
	}
}
