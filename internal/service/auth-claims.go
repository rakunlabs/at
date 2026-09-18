package service

import (
	"fmt"
	"strings"
)

// Role claims arrive in provider-specific shapes. Keycloak nests realm roles
// under realm_access.roles and client roles under resource_access.<client>.roles,
// Auth0 uses a namespaced claim, Entra sometimes nests group assignments. Reading
// only top-level claims made every one of those invisible, so a provider declares
// the paths to read and their values are folded into the roles assertion that
// workspace permission mappings already match on.
//
// Paths are dot-separated. A `*` segment matches every key of an object and every
// element of an array, which is what reaches Keycloak's per-client role sets. The
// walk is bounded in depth, fan-out, value count and total bytes: the harvested
// list is persisted inside the identity link's asserted-permissions document,
// which the store rejects beyond 8 KB, and a provider we do not control chooses
// the shape of the claims.
const (
	AuthClaimPathsMax        = 16
	authClaimPathBytesMax    = 256
	authClaimPathSegmentsMax = 8
	authClaimNodesMax        = 512
	authClaimValueBytesMax   = 256
	AuthClaimValuesMax       = 64
	authClaimBytesMax        = 3072
)

// ValidateClaimPath accepts only literal segments and the `*` wildcard, so a
// path cannot smuggle separators or blank segments that silently match nothing.
func ValidateClaimPath(path string) error {
	if path == "" || len(path) > authClaimPathBytesMax || strings.TrimSpace(path) != path {
		return fmt.Errorf("claim path must be 1-%d bytes without surrounding space", authClaimPathBytesMax)
	}
	segments := strings.Split(path, ".")
	if len(segments) > authClaimPathSegmentsMax {
		return fmt.Errorf("claim path %q exceeds %d segments", path, authClaimPathSegmentsMax)
	}
	for _, segment := range segments {
		if segment == "" || strings.ContainsAny(segment, " \t\r\n") {
			return fmt.Errorf("claim path %q has an empty or spaced segment", path)
		}
	}
	return nil
}

// ValidateClaimPaths bounds the configured set as a whole.
func ValidateClaimPaths(paths []string) error {
	if len(paths) > AuthClaimPathsMax {
		return fmt.Errorf("at most %d claim paths are allowed", AuthClaimPathsMax)
	}
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if err := ValidateClaimPath(path); err != nil {
			return err
		}
		if seen[path] {
			return fmt.Errorf("duplicate claim path %q", path)
		}
		seen[path] = true
	}
	return nil
}

// HarvestClaimValues collects the string values addressed by paths. Values are
// whitespace-split because space-delimited lists are a normal wire form for these
// claims; a value containing whitespace is therefore not addressable this way.
func HarvestClaimValues(claims map[string]any, paths []string) []string {
	if len(claims) == 0 || len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, 8)
	seen := make(map[string]bool, 8)
	bytes := 0
	nodes := 0
	for _, path := range paths {
		if ValidateClaimPath(path) != nil {
			continue
		}
		walkClaimPath(claims, strings.Split(path, "."), &nodes, func(leaf any) {
			for _, value := range claimLeafValues(leaf) {
				if seen[value] || len(out) >= AuthClaimValuesMax || bytes+len(value) > authClaimBytesMax {
					continue
				}
				seen[value] = true
				bytes += len(value)
				out = append(out, value)
			}
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// MergeClaimValues appends harvested values to the values the provider already
// reported. Reported values are preserved as they were before claim paths
// existed — bounding them would silently drop role matches that work today —
// while the appended ones stay inside the harvest bounds. Nil in, nil out, so an
// identity with nothing to assert keeps its previous stored shape.
func MergeClaimValues(reported, harvested []string) []string {
	if len(reported) == 0 && len(harvested) == 0 {
		return reported
	}
	out := make([]string, 0, len(reported)+len(harvested))
	seen := make(map[string]bool, len(reported)+len(harvested))
	for _, value := range reported {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	bytes := 0
	for _, value := range harvested {
		if value == "" || len(value) > authClaimValueBytesMax || seen[value] {
			continue
		}
		if bytes+len(value) > authClaimBytesMax {
			break
		}
		seen[value] = true
		bytes += len(value)
		out = append(out, value)
	}
	return out
}

// AuthUsernameClaims are read, in order, for the username an external provider
// reports. `preferred_username` is OIDC's username claim and comes first;
// `nickname` and `name` are display names and are only a fallback, because they
// are usually a full personal name rather than the handle an administrator
// types into a search box.
//
// This is a label, never an identity. The account stays keyed on provider ID
// plus subject, the value is refreshed from every sign-in, and a provider that
// reassigns a username therefore renames a row and grants nothing.
var AuthUsernameClaims = []string{"preferred_username", "nickname", "name"}

// AuthUsernameMax bounds the stored value in runes. The provider chooses this
// string and it is rendered in the administrator directory.
const AuthUsernameMax = 128

// ClaimUsername picks the first non-empty username claim, falling back to the
// name the strategy already resolved (ada reads `name` then
// `preferred_username` into Identity.Name, so the fallback covers a provider
// that only exposes it there).
func ClaimUsername(claims map[string]any, fallback string) string {
	for _, key := range AuthUsernameClaims {
		if text, ok := claims[key].(string); ok {
			if value := NormalizeAuthUsername(text); value != "" {
				return value
			}
		}
	}
	return NormalizeAuthUsername(fallback)
}

// NormalizeAuthUsername collapses whitespace, drops control characters and
// truncates on a rune boundary. Truncating on bytes would be able to store an
// invalid UTF-8 tail, which the JSON encoder then replaces on the way out.
func NormalizeAuthUsername(value string) string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\r' || r == '\n' || r < 0x20 || r == 0x7f
	})
	out := strings.Join(fields, " ")
	if runes := []rune(out); len(runes) > AuthUsernameMax {
		out = string(runes[:AuthUsernameMax])
	}
	return out
}

func walkClaimPath(node any, segments []string, nodes *int, leaf func(any)) {
	if *nodes >= authClaimNodesMax {
		return
	}
	*nodes++
	if len(segments) == 0 {
		leaf(node)
		return
	}
	segment, rest := segments[0], segments[1:]
	switch value := node.(type) {
	case map[string]any:
		if segment != "*" {
			if child, ok := value[segment]; ok {
				walkClaimPath(child, rest, nodes, leaf)
			}
			return
		}
		for _, child := range value {
			walkClaimPath(child, rest, nodes, leaf)
		}
	case []any:
		// An array in the middle of a path is traversed only by a wildcard, so a
		// literal segment never matches a positional index by accident.
		if segment != "*" {
			return
		}
		for _, child := range value {
			walkClaimPath(child, rest, nodes, leaf)
		}
	}
}

func claimLeafValues(leaf any) []string {
	switch value := leaf.(type) {
	case string:
		return claimTokens(value)
	case []string:
		out := make([]string, 0, len(value))
		for _, item := range value {
			out = append(out, claimTokens(item)...)
		}
		return out
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok {
				out = append(out, claimTokens(text)...)
			}
		}
		return out
	}
	return nil
}

func claimTokens(value string) []string {
	out := make([]string, 0, 1)
	for _, token := range strings.Fields(value) {
		if len(token) <= authClaimValueBytesMax {
			out = append(out, token)
		}
	}
	return out
}
