package service

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/rakunlabs/query"
	"github.com/rakunlabs/types"
)

// RoutingProfile is a named, workspace-scoped model chain. A gateway request
// naming the profile as its model is expanded to Targets and routed through the
// same fallback machinery as an explicit at_fallbacks list.
//
// The name deliberately cannot contain "/": absence of a slash is what
// distinguishes a profile name from a direct "provider/model" reference during
// chain resolution, so it is enforced here rather than relied upon at read time.
type RoutingProfile struct {
	// Personal profiles remain workspace-bound and visible only to their owner.
	OwnerUserID string              `json:"owner_user_id,omitempty"`
	Scope       string              `json:"scope,omitempty"`
	WorkspaceID string              `json:"workspace_id"`
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Targets     types.Slice[string] `json:"targets"` // ordered "provider/model" entries
	// Strategy decides the order targets are attempted in for each request;
	// every strategy still falls back through the remaining targets.
	Strategy string `json:"strategy"`
	// Weights are per-target weights for the weighted strategy (default 1;
	// 0 makes a target fallback-only). Keys must be listed in Targets.
	Weights   types.Map[int] `json:"weights,omitempty"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
	CreatedBy string         `json:"created_by,omitempty"`
	UpdatedBy string         `json:"updated_by,omitempty"`
}

// RoutingProfileStorer defines CRUD operations for routing profiles.
type RoutingProfileStorer interface {
	ListRoutingProfiles(ctx context.Context, q *query.Query) (*ListResult[RoutingProfile], error)
	GetRoutingProfile(ctx context.Context, id string) (*RoutingProfile, error)
	GetRoutingProfileByName(ctx context.Context, name string) (*RoutingProfile, error)
	CreateRoutingProfile(ctx context.Context, p RoutingProfile) (*RoutingProfile, error)
	UpdateRoutingProfile(ctx context.Context, id string, p RoutingProfile) (*RoutingProfile, error)
	DeleteRoutingProfile(ctx context.Context, id string) error
}

// GatewayRoutingProfileStorer resolves a profile on the gateway request path,
// where the authority is the presented API token rather than a browser session
// principal. The workspace is therefore supplied explicitly from the token's
// persisted binding, mirroring GatewayMCPAdmissionStorer. An empty workspace
// resolves nothing: unlike an MCP endpoint there is no "public" profile, so a
// token with no workspace binding simply has no profiles.
type GatewayRoutingProfileStorer interface {
	GetGatewayRoutingProfile(ctx context.Context, name, workspaceID string, owner ...string) (*RoutingProfile, error)
	ListGatewayRoutingProfiles(ctx context.Context, workspaceID string, owner ...string) ([]RoutingProfile, error)
}

// routingProfileNamePattern excludes "/" by construction. See RoutingProfile.
var routingProfileNamePattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// RoutingProfileMaxTargets bounds a chain so one profile cannot turn a single
// request into an unbounded sequence of upstream attempts.
const RoutingProfileMaxTargets = 16

// Routing strategies. Each one only orders the chain: the request still falls
// back through every remaining target, and cooled or circuit-open targets are
// still moved to the back afterwards.
const (
	// RoutingStrategyPriority tries targets in their configured order.
	RoutingStrategyPriority = "priority"
	// RoutingStrategyRoundRobin rotates the first target per request.
	RoutingStrategyRoundRobin = "round_robin"
	// RoutingStrategyWeighted samples the order by Weights (uniform without).
	RoutingStrategyWeighted = "weighted"
	// RoutingStrategyLeastCost orders by configured model price; unpriced
	// targets keep their relative order after the priced ones.
	RoutingStrategyLeastCost = "least_cost"
	// RoutingStrategySticky keeps one session (or one API token when the
	// request names no session) on the same target, so provider prompt caches
	// stay warm. Rendezvous hashing moves few sessions when targets change.
	RoutingStrategySticky = "sticky"
)

// RoutingStrategies lists the accepted strategy values.
var RoutingStrategies = []string{
	RoutingStrategyPriority,
	RoutingStrategyRoundRobin,
	RoutingStrategyWeighted,
	RoutingStrategyLeastCost,
	RoutingStrategySticky,
}

// RoutingProfileMaxWeight bounds a single target weight.
const RoutingProfileMaxWeight = 1000

// ValidateRoutingProfile checks the name and target list. It does not check that
// the referenced providers exist: providers are configured independently and a
// profile may legitimately name one that is added later. Reachability is decided
// per attempt during chain resolution, where token access also applies.
func ValidateRoutingProfile(p RoutingProfile) error {
	name := strings.TrimSpace(p.Name)
	switch {
	case name == "":
		return fmt.Errorf("name is required")
	case len(name) > 200:
		return fmt.Errorf("name must be at most 200 characters")
	case strings.Contains(name, "/"):
		return fmt.Errorf("name may not contain %q: it would be indistinguishable from a provider/model reference", "/")
	case !routingProfileNamePattern.MatchString(name):
		return fmt.Errorf("name may only contain letters, digits, dot, underscore and hyphen")
	}

	if len(p.Targets) == 0 {
		return fmt.Errorf("at least one target is required")
	}
	if len(p.Targets) > RoutingProfileMaxTargets {
		return fmt.Errorf("at most %d targets are allowed", RoutingProfileMaxTargets)
	}

	seen := make(map[string]struct{}, len(p.Targets))
	for i, target := range p.Targets {
		t := strings.TrimSpace(target)
		if t == "" {
			return fmt.Errorf("target %d is empty", i+1)
		}
		slash := strings.Index(t, "/")
		if slash <= 0 || slash == len(t)-1 {
			return fmt.Errorf("target %q must be in provider/model form", target)
		}
		if _, dup := seen[t]; dup {
			return fmt.Errorf("target %q is listed more than once", t)
		}
		seen[t] = struct{}{}
	}

	if !slices.Contains(RoutingStrategies, p.Strategy) {
		return fmt.Errorf("strategy %q is not supported (use one of %s)", p.Strategy, strings.Join(RoutingStrategies, ", "))
	}
	for target, weight := range p.Weights {
		if _, ok := seen[target]; !ok {
			return fmt.Errorf("weight for %q does not match any target", target)
		}
		if weight < 0 || weight > RoutingProfileMaxWeight {
			return fmt.Errorf("weight for %q must be between 0 and %d", target, RoutingProfileMaxWeight)
		}
	}

	return nil
}

// NormalizeRoutingProfile trims the name, description and every target so that
// stored values match what chain resolution compares against.
func NormalizeRoutingProfile(p RoutingProfile) RoutingProfile {
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)

	targets := make(types.Slice[string], 0, len(p.Targets))
	for _, target := range p.Targets {
		if t := strings.TrimSpace(target); t != "" {
			targets = append(targets, t)
		}
	}
	p.Targets = targets

	p.Strategy = strings.TrimSpace(p.Strategy)
	if p.Strategy == "" {
		p.Strategy = RoutingStrategyPriority
	}
	if len(p.Weights) > 0 {
		weights := make(types.Map[int], len(p.Weights))
		for target, weight := range p.Weights {
			weights[strings.TrimSpace(target)] = weight
		}
		p.Weights = weights
	}

	return p
}
