package service

import (
	"context"
	"time"
)

type WorkspaceID string

// DefaultWorkspaceID names the workspace every installation is guaranteed to
// have: it is created on demand, sorts first, and cannot be deleted. The ID is
// historical — it predates the workspace model and is spelled as a literal in
// the store layer — so new call sites use this constant rather than adding
// another copy of the string.
const DefaultWorkspaceID = "legacy-default"

type Workspace struct {
	ID               string    `json:"id" db:"id"`
	Name             string    `json:"name" db:"name"`
	Archived         bool      `json:"archived" db:"archived"`
	ExecutionEnabled bool      `json:"execution_enabled" db:"execution_enabled"`
	CreatedAt        time.Time `json:"created_at" db:"created_at"`
	Role             string    `json:"role,omitempty" db:"role"`
}
type WorkspaceMembership struct {
	WorkspaceID string `json:"workspace_id" db:"workspace_id"`
	UserID      string `json:"user_id" db:"user_id"`
	Role        string `json:"role" db:"role"`
	Status      string `json:"status" db:"status"`
	Version     int64  `json:"version" db:"version"`
}
type WorkspaceInvitation struct {
	ID          string    `json:"id" db:"id"`
	WorkspaceID string    `json:"workspace_id" db:"workspace_id"`
	Role        string    `json:"role" db:"role"`
	UserID      string    `json:"user_id,omitempty" db:"user_id"`
	Email       string    `json:"email,omitempty" db:"email"`
	IssuerID    string    `json:"issuer_id" db:"issuer_id"`
	TokenHash   string    `json:"-" db:"token_hash"`
	ExpiresAt   time.Time `json:"expires_at" db:"expires_at"`
	Consumed    bool      `json:"consumed" db:"consumed"`
}
type PermissionBundle struct {
	ID          string              `json:"id"`
	WorkspaceID string              `json:"workspace_id"`
	Key         string              `json:"key"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Keys        []string            `json:"keys"`
	KeyPatterns map[string][]string `json:"key_patterns"`
	ResourceIDs map[string][]string `json:"resource_ids,omitempty"`
}
type PermissionMapping struct {
	ID           string `json:"id"`
	WorkspaceID  string `json:"workspace_id"`
	ProviderID   string `json:"provider_id"`
	ClaimKind    string `json:"claim_kind"`
	ClaimValue   string `json:"claim_value"`
	PermissionID string `json:"permission_id"`
	// AdmitRole optionally admits a matching external identity to the workspace
	// with that role when it has no membership row at all. Empty means the
	// mapping only grants capabilities to an existing member, which is the
	// historical behaviour. Owner is never admissible.
	AdmitRole string `json:"admit_role,omitempty"`
}
type AccessGrantSource struct {
	AccessGrant
	Source       string `json:"source"`
	PermissionID string `json:"permission_id,omitempty"`
	ProviderID   string `json:"provider_id,omitempty"`
	MappingID    string `json:"mapping_id,omitempty"`
}
type EffectiveAccess struct {
	WorkspaceID       string              `json:"workspace_id"`
	UserID            string              `json:"user_id"`
	Role              string              `json:"role"`
	MembershipVersion int64               `json:"membership_version"`
	Capabilities      []string            `json:"capabilities"`
	Patterns          map[string][]string `json:"patterns"`
	Sources           []AccessGrantSource `json:"sources"`
	Denied            []string            `json:"denied"`
	ExecutionEnabled  bool                `json:"execution_enabled"`
}
type WorkspaceStorer interface {
	ListWorkspaces(context.Context) ([]Workspace, error)
	GetWorkspace(context.Context) (*Workspace, error)
	CreateWorkspace(context.Context, string, string) (*Workspace, error)
	UpdateWorkspace(context.Context, Workspace) (*Workspace, error)
	ListWorkspaceMembers(context.Context) ([]WorkspaceMembership, error)
	SetWorkspaceMember(context.Context, WorkspaceMembership) error
	CreateWorkspaceInvitation(context.Context, WorkspaceInvitation) (*WorkspaceInvitation, error)
	ListWorkspaceInvitations(context.Context) ([]WorkspaceInvitation, error)
	AcceptWorkspaceInvitation(context.Context, string) (*WorkspaceMembership, error)
	ResolveWorkspaceAccess(context.Context, string, string, string) (AccessPrincipal, EffectiveAccess, error)
	ListPermissions(context.Context) ([]PermissionBundle, error)
	SavePermission(context.Context, PermissionBundle) (*PermissionBundle, error)
	DeletePermission(context.Context, string) error
	GetUserPermissions(context.Context, string) ([]string, error)
	SetUserPermissions(context.Context, string, []string) error
	SetUserDenied(context.Context, string, []string) error
	ListPermissionMappings(context.Context) ([]PermissionMapping, error)
	SavePermissionMapping(context.Context, PermissionMapping) (*PermissionMapping, error)
	DeletePermissionMapping(context.Context, string) error
}

type WorkspacePreferences struct {
	Mode            string `json:"mode" db:"mode"`
	WorkspaceID     string `json:"workspace_id" db:"workspace_id"`
	LastWorkspaceID string `json:"last_workspace_id" db:"last_workspace_id"`
}

type WorkspaceDeletion struct {
	WorkspaceID string   `json:"workspace_id"`
	BotIDs      []string `json:"-"`
	TaskIDs     []string `json:"-"`
}

type WorkspaceLifecycleStorer interface {
	DeleteWorkspace(context.Context, string) (*WorkspaceDeletion, error)
	GetWorkspacePreferences(context.Context) (*WorkspacePreferences, error)
	SaveWorkspacePreferences(context.Context, WorkspacePreferences) (*WorkspacePreferences, error)
	SelectWorkspace(context.Context, string) error
}

func (b PermissionBundle) Grants() ([]AccessGrant, error) {
	grants := make([]AccessGrant, 0, len(b.Keys))
	for _, key := range b.Keys {
		g := AccessGrant{Capability: key, PathPatterns: b.KeyPatterns[key], ResourceIDs: b.ResourceIDs[key]}
		if err := ValidateAccessGrant(g); err != nil {
			return nil, err
		}
		grants = append(grants, g)
	}
	for key, values := range b.KeyPatterns {
		if !bundleHasKey(b.Keys, key) || len(values) == 0 {
			return nil, ErrAccessDenied
		}
	}
	for key, values := range b.ResourceIDs {
		if !bundleHasKey(b.Keys, key) || len(values) == 0 {
			return nil, ErrAccessDenied
		}
	}
	return grants, nil
}
func bundleHasKey(keys []string, key string) bool {
	for _, k := range keys {
		if k == key {
			return true
		}
	}
	return false
}
