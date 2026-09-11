package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
)

var ErrAccessDenied = errors.New("access denied")
var ErrWorkspaceRequired = errors.New("workspace required")
var ErrWorkspaceConflict = errors.New("workspace conflict")
var ErrAccessResourceNotFound = fmt.Errorf("workspace resource not found: %w", ErrAccessDenied)

type AccessPrincipal struct {
	UserID            string        `json:"user_id"`
	WorkspaceID       string        `json:"workspace_id"`
	Role              string        `json:"role"`
	SessionID         string        `json:"session_id,omitempty"`
	PlatformAdmin     bool          `json:"platform_admin"`
	MembershipVersion int64         `json:"membership_version"`
	ExecutionDisabled bool          `json:"execution_disabled"`
	Grants            []AccessGrant `json:"grants"`
	Denied            []string      `json:"denied"`
}

type AccessGrant struct {
	Capability   string   `json:"capability"`
	ResourceIDs  []string `json:"resource_ids,omitempty"`
	PathPatterns []string `json:"path_patterns,omitempty"`
}

type AccessResource struct{ Kind, ID, WorkspaceID, OwnerID, Path string }

// WorkspaceResourceStorer resolves only authoritative metadata, never secret
// content. It is a reference/admission seam, not a substitute for scoped CRUD.
type WorkspaceResourceStorer interface {
	GetWorkspaceAccessResource(context.Context, string, string) (*AccessResource, error)
}
type AccessCapability struct {
	Key          string `json:"key"`
	PlatformOnly bool   `json:"platform_only"`
}

// The registry is finite: unknown actions never inherit authority from a wildcard.
func AccessCapabilities() []AccessCapability {
	var out []AccessCapability
	for _, kind := range []string{"organizations", "agents", "projects", "goals", "tasks", "comments", "labels", "approvals", "workflows", "skills", "variables", "connections", "bots", "mcp", "packs", "tokens", "files"} {
		for _, action := range []string{"read", "write"} {
			out = append(out, AccessCapability{Key: kind + "." + action})
		}
	}
	for _, key := range []string{"workspace.read", "workspace.write", "workspace.archive", "members.read", "members.manage", "permissions.read", "permissions.manage", "credentials.manage", "models.use", "agents.execute", "workflows.execute", "tasks.execute", "tasks.cancel", "usage.read", "traces.read", "providers.read", "providers.write", "execution.configure"} {
		out = append(out, AccessCapability{Key: key})
	}
	for _, key := range []string{"connections.use", "variables.use", "node_configs.use", "mcp.use", "bots.use"} {
		out = append(out, AccessCapability{Key: key})
	}
	for _, key := range []string{"platform.manage", "platform.execute", "platform.files", "platform.providers"} {
		out = append(out, AccessCapability{Key: key, PlatformOnly: true})
	}
	return out
}

func KnownAccessCapability(key string) (AccessCapability, bool) {
	for _, c := range AccessCapabilities() {
		if c.Key == key {
			return c, true
		}
	}
	return AccessCapability{}, false
}

func WorkspaceRoleRank(role string) int {
	switch role {
	case "viewer":
		return 1
	case "member":
		return 2
	case "admin":
		return 3
	case "owner":
		return 4
	}
	return 0
}

func WorkspaceRoleGrants(role string) []AccessGrant {
	rank := WorkspaceRoleRank(role)
	var grants []AccessGrant
	for _, c := range AccessCapabilities() {
		if rank == 0 || c.PlatformOnly {
			continue
		}
		min := 3
		if strings.HasSuffix(c.Key, ".read") {
			min = 1
		}
		if strings.HasSuffix(c.Key, ".write") || strings.HasSuffix(c.Key, ".execute") || c.Key == "models.use" || c.Key == "tasks.cancel" {
			min = 2
		}
		if c.Key == "connections.use" || c.Key == "variables.use" || c.Key == "node_configs.use" || c.Key == "mcp.use" || c.Key == "bots.use" {
			min = 2
		}
		if slices.Contains([]string{"workspace.write", "providers.write", "tokens.write", "connections.write", "variables.write", "bots.write", "mcp.write", "skills.write"}, c.Key) {
			min = 3
		}
		if c.Key == "workspace.archive" {
			min = 4
		}
		if rank >= min {
			grants = append(grants, AccessGrant{Capability: c.Key})
		}
	}
	return grants
}

// Selectors intersect within a grant; grants union. Nil means unrestricted, while
// an explicitly supplied empty selector is invalid (never silently broadened).
func ValidateAccessGrant(g AccessGrant) error {
	if len(g.ResourceIDs) > 100 || len(g.PathPatterns) > 100 {
		return fmt.Errorf("too many selectors: %w", ErrAccessDenied)
	}
	c, ok := KnownAccessCapability(g.Capability)
	if !ok || c.PlatformOnly {
		return fmt.Errorf("invalid grant capability: %w", ErrAccessDenied)
	}
	if g.ResourceIDs != nil && len(g.ResourceIDs) == 0 || g.PathPatterns != nil && len(g.PathPatterns) == 0 {
		return fmt.Errorf("empty selector: %w", ErrAccessDenied)
	}
	for _, id := range g.ResourceIDs {
		if strings.TrimSpace(id) == "" || len(id) > 1024 {
			return fmt.Errorf("blank resource selector: %w", ErrAccessDenied)
		}
	}
	for _, pattern := range g.PathPatterns {
		if pattern == "" || len(pattern) > 1024 || strings.HasPrefix(pattern, "/") || strings.Contains(pattern, "\\") || slices.Contains(strings.Split(pattern, "/"), "..") {
			return fmt.Errorf("invalid path selector: %w", ErrAccessDenied)
		}
		if _, err := path.Match(pattern, ""); err != nil {
			return fmt.Errorf("invalid path selector: %w", err)
		}
	}
	return nil
}

func (p AccessPrincipal) Allows(cap string, r AccessResource) bool {
	c, ok := KnownAccessCapability(cap)
	if !ok || p.WorkspaceID == "" || r.WorkspaceID != p.WorkspaceID {
		return false
	}
	if p.ExecutionDisabled && (cap == "models.use" || strings.HasSuffix(cap, ".execute")) {
		return false
	}
	if c.PlatformOnly {
		return p.PlatformAdmin
	}
	if !p.PlatformAdmin && slices.Contains(p.Denied, cap) {
		return false
	}
	if p.PlatformAdmin {
		return true
	}
	for _, g := range p.Grants {
		if g.Capability != cap || ValidateAccessGrant(g) != nil {
			continue
		}
		if g.ResourceIDs != nil && !slices.Contains(g.ResourceIDs, r.ID) {
			continue
		}
		if g.PathPatterns != nil {
			if r.Path == "" || strings.HasPrefix(r.Path, "/") || path.Clean(r.Path) != r.Path || strings.Contains(r.Path, "\\") || slices.Contains(strings.Split(r.Path, "/"), "..") {
				continue
			}
			match := false
			for _, pattern := range g.PathPatterns {
				if yes, _ := path.Match(pattern, r.Path); yes {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		return true
	}
	return false
}

type accessPrincipalKey struct{}

func cloneAccessPrincipal(p AccessPrincipal) AccessPrincipal {
	p.Grants = slices.Clone(p.Grants)
	p.Denied = slices.Clone(p.Denied)
	for i := range p.Grants {
		p.Grants[i].ResourceIDs = slices.Clone(p.Grants[i].ResourceIDs)
		p.Grants[i].PathPatterns = slices.Clone(p.Grants[i].PathPatterns)
	}
	return p
}
func WithAccessPrincipal(ctx context.Context, p AccessPrincipal) context.Context {
	return context.WithValue(ctx, accessPrincipalKey{}, cloneAccessPrincipal(p))
}
func AccessPrincipalFromContext(ctx context.Context) (AccessPrincipal, bool) {
	p, ok := ctx.Value(accessPrincipalKey{}).(AccessPrincipal)
	return cloneAccessPrincipal(p), ok
}
func AuthorizeAccess(ctx context.Context, cap string, r AccessResource) error {
	p, ok := AccessPrincipalFromContext(ctx)
	if !ok || !p.Allows(cap, r) {
		return ErrAccessDenied
	}
	return nil
}

// AccessRevalidator is called before dequeue/resume/model/tool/delegation admission.
type AccessRevalidator func(context.Context) (context.Context, error)

// CanDelegateAccess conservatively proves containment; different patterns are not
// assumed equivalent. This avoids widening delegated authority via glob tricks.
func CanDelegateAccess(p AccessPrincipal, g AccessGrant) bool {
	if ValidateAccessGrant(g) != nil || p.WorkspaceID == "" {
		return false
	}
	if p.PlatformAdmin {
		return true
	}
	if slices.Contains(p.Denied, g.Capability) {
		return false
	}
	for _, own := range p.Grants {
		if own.Capability != g.Capability || ValidateAccessGrant(own) != nil {
			continue
		}
		ids := own.ResourceIDs == nil || g.ResourceIDs != nil && allContained(own.ResourceIDs, g.ResourceIDs)
		patterns := own.PathPatterns == nil || g.PathPatterns != nil && allContained(own.PathPatterns, g.PathPatterns)
		if ids && patterns {
			return true
		}
	}
	return false
}
func allContained(parent, child []string) bool {
	for _, v := range child {
		if !slices.Contains(parent, v) {
			return false
		}
	}
	return true
}
