package server

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The sidebar and the router guards decide what to offer from two hardcoded
// lists in `_ui/src/lib/helper/navigation.ts`, because no API reports which
// routes are administration. That duplication is the thing that drifts: a route
// listed under `capabilityRoutes` whose API has no `BusinessRoutePolicy` falls
// through to `requireWorkspacePlatform(true)`, so a member was shown a link,
// clicked it, and got 403 from every request the page made — the capability the
// UI named had no bearing on the decision.
//
// These tests read that file and check each route against the real admission
// tables, so scoping an API backend-side (or adding a page) fails here until
// the UI is updated with it.

const uiNavigationFile = "../../_ui/src/lib/helper/navigation.ts"

// uiRouteProbe is a representative request for a UI route's API surface,
// relative to `/api/v1` the way `workspaceBusinessAuthentication` trims it.
type uiRouteProbe struct{ method, path string }

// capability routes whose API is admitted on capabilities somewhere other than
// `workspaceBusinessPolicies`, with the site that does it. Probing them against
// the business table would report a false positive.
var uiCapabilityElsewhere = map[string]string{
	"/files":                "registerRuntimeRoutes: /api/v1/files/* via workspaceAuthentication",
	"/studio":               "registerRuntimeRoutes: /api/v1/files/* via workspaceAuthentication",
	"/settings/execution":   "registerRuntimeRoutes: /api/v1/workspaces/{workspace}/execution-policy",
	"/settings/permissions": "workspaceRoutePolicies: /api/v1/permissions",
}

// platform routes whose surface is not registered on apiGroup at all (native
// auth administration), so there is no business policy to assert against.
var uiPlatformOutsideAPIGroup = []string{
	"/users", "/settings/users", "/settings/authentication", "/settings/system",
}

// Skills, Connections and MCP sets are probed with their *management* call, not
// their list. Their read routes are capability-admitted so the surfaces that are
// (the Playground's tool picker, the Agents editor) can enumerate them to
// configure an agent — but creating or editing one is still installation
// administration, which is what those pages are for. The probe therefore has to
// name what the page does, or this test would demand the page be shown to
// members who cannot use any of its controls.
var uiRouteProbes = map[string]uiRouteProbe{
	// Capability-admitted.
	"/providers":        {"GET", "/providers"},
	"/routing-profiles": {"GET", "/routing-profiles"},
	"/agents":           {"GET", "/agents"},
	"/sessions":         {"GET", "/chat/sessions"},
	"/workflows":        {"GET", "/workflows"},
	"/runs":             {"GET", "/runs"},
	"/bots":             {"GET", "/bots"},
	"/organizations":    {"GET", "/organizations"},
	"/tasks":            {"GET", "/tasks"},
	"/settings/tokens":  {"GET", "/api-tokens"},
	// Installation administration.
	"/terminal":          {"GET", "/terminals"},
	"/pricing":           {"GET", "/model-pricing"},
	"/settings/features": {"PUT", "/features"},
	"/settings/media":    {"GET", "/media/settings"},
	"/playground":        {"POST", "/chat/completions"},
	"/skills":            {"POST", "/skills"},
	"/marketplaces":      {"GET", "/marketplaces"},
	"/integrations":      {"GET", "/integration-packs"},
	"/variables":         {"GET", "/variables"},
	"/node-configs":      {"GET", "/node-configs"},
	"/webhooks":          {"GET", "/triggers"},
	"/crons":             {"GET", "/triggers"},
	"/connections":       {"POST", "/connections"},
	"/mcp-servers":       {"GET", "/mcp/servers"},
	"/mcps":              {"POST", "/mcp/sets"},
	"/usage":             {"GET", "/usage/summary"},
	"/llm-calls":         {"GET", "/llm-calls"},
}

func uiNavigationSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(uiNavigationFile)
	if err != nil {
		t.Fatalf("read %s: %v", uiNavigationFile, err)
	}
	return string(b)
}

// uiRouteList extracts the quoted route prefixes from one declaration. The file
// is small and stable; a parse miss fails loudly rather than silently checking
// nothing, which is the failure mode that matters for an authorization test.
func uiRouteList(t *testing.T, src, decl, open, close string) []string {
	t.Helper()
	start := strings.Index(src, decl)
	if start < 0 {
		t.Fatalf("%s: %q not found — update this test with the new declaration", uiNavigationFile, decl)
	}
	body := src[start+len(decl):]
	end := strings.Index(body, close)
	if end < 0 {
		t.Fatalf("%s: unterminated %s", uiNavigationFile, decl)
	}
	_ = open
	var out []string
	for _, m := range regexp.MustCompile(`'(/[^']*)'`).FindAllStringSubmatch(body[:end], -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		t.Fatalf("%s: no routes parsed from %s", uiNavigationFile, decl)
	}
	return out
}

func uiCapabilityRoutes(t *testing.T) []string {
	t.Helper()
	src := uiNavigationSource(t)
	// Only the map keys are routes; values are capability names without a
	// leading slash, so the shared pattern cannot pick them up.
	return uiRouteList(t, src, "const capabilityRoutes: Record<string, string> = {", "{", "};")
}

func uiPlatformRoutes(t *testing.T) []string {
	t.Helper()
	return uiRouteList(t, uiNavigationSource(t), "const platformRoutes = [", "[", "];")
}

func businessPolicyFor(method, path string) *BusinessRoutePolicy {
	for _, p := range workspaceBusinessPolicies() {
		if p.Method == method && matchesBusinessPattern(p.Pattern, path) {
			return &p
		}
	}
	return nil
}

// A capability route must have an API that actually reads the capability.
func TestUICapabilityRoutesAreCapabilityAdmitted(t *testing.T) {
	for _, route := range uiCapabilityRoutes(t) {
		if where, ok := uiCapabilityElsewhere[route]; ok {
			t.Logf("%s: admitted by %s", route, where)
			continue
		}
		probe, ok := uiRouteProbes[route]
		if !ok {
			t.Errorf("%s is offered on a workspace capability but this test has no probe for its API; add one to uiRouteProbes", route)
			continue
		}
		policy := businessPolicyFor(probe.method, probe.path)
		if policy == nil {
			t.Errorf("%s claims a workspace capability but %s /api/v1%s has no BusinessRoutePolicy, so it is installation-administrator only; move the route to platformRoutes in %s", route, probe.method, probe.path, uiNavigationFile)
			continue
		}
		if policy.Capability == "platform.manage" {
			t.Errorf("%s claims a workspace capability but %s /api/v1%s requires platform.manage", route, probe.method, probe.path)
		}
	}
}

// The converse: a route listed as administration must really be administration.
// Scoping its API is what moves it back, and leaving it here would then hide a
// page a member is allowed to use.
func TestUIPlatformOnlySurfaces(t *testing.T) {
	for _, route := range uiPlatformRoutes(t) {
		if slices.Contains(uiPlatformOutsideAPIGroup, route) {
			continue
		}
		probe, ok := uiRouteProbes[route]
		if !ok {
			t.Errorf("%s is hidden from non-administrators but this test has no probe for its API; add one to uiRouteProbes", route)
			continue
		}
		if policy := businessPolicyFor(probe.method, probe.path); policy != nil && policy.Capability != "platform.manage" {
			t.Errorf("%s is hidden from non-administrators but %s /api/v1%s is admitted on %q; move the route back to capabilityRoutes in %s", route, probe.method, probe.path, policy.Capability, uiNavigationFile)
		}
		if isSharedPlatformRoute(probe.method, probe.path) {
			t.Errorf("%s is hidden from non-administrators but %s /api/v1%s is a shared platform route", route, probe.method, probe.path)
		}
	}
}

// The feature catalog decides which surfaces the UI offers at all, and
// `isFeatureEnabled` reports enabled until it arrives. Admin-only admission
// therefore left every disabled feature visible and linked for exactly the
// accounts that cannot change it. Reading is shared; writing stays
// administration.
func TestFeatureCatalogReadIsShared(t *testing.T) {
	for _, tt := range []struct {
		method, path string
		shared       bool
	}{
		{"GET", "/features", true},
		{"PUT", "/features", false},
		{"PUT", "/features/{key}", false},
		{"POST", "/features/presets/full", false},
	} {
		if got := isSharedPlatformRoute(tt.method, tt.path); got != tt.shared {
			t.Errorf("%s /api/v1%s shared=%v want %v", tt.method, tt.path, got, tt.shared)
		}
	}
	if businessPolicyFor("GET", "/features") != nil {
		t.Error("GET /features should be admitted as a shared platform route, not on a workspace capability")
	}
}
