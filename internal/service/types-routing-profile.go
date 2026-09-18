package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/rakunlabs/query"
	"github.com/worldline-go/types"
)

// RoutingProfile is a named, workspace-scoped model chain. A gateway request
// naming the profile as its model is expanded to Targets and routed through the
// same fallback machinery as an explicit at_fallbacks list.
//
// The name deliberately cannot contain "/": absence of a slash is what
// distinguishes a profile name from a direct "provider/model" reference during
// chain resolution, so it is enforced here rather than relied upon at read time.
type RoutingProfile struct {
	WorkspaceID string              `json:"workspace_id"`
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Targets     types.Slice[string] `json:"targets"` // ordered "provider/model" entries
	CreatedAt   string              `json:"created_at"`
	UpdatedAt   string              `json:"updated_at"`
	CreatedBy   string              `json:"created_by,omitempty"`
	UpdatedBy   string              `json:"updated_by,omitempty"`
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
	GetGatewayRoutingProfile(ctx context.Context, name, workspaceID string) (*RoutingProfile, error)
	ListGatewayRoutingProfiles(ctx context.Context, workspaceID string) ([]RoutingProfile, error)
}

// routingProfileNamePattern excludes "/" by construction. See RoutingProfile.
var routingProfileNamePattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// RoutingProfileMaxTargets bounds a chain so one profile cannot turn a single
// request into an unbounded sequence of upstream attempts.
const RoutingProfileMaxTargets = 16

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

	return p
}
