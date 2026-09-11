package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/rakunlabs/ada"

	"github.com/rakunlabs/at/internal/service"
)

type workspaceRoutePolicy struct {
	Method, Path, Class, Capability string
	Handler                         func(*Server) http.HandlerFunc
}

// Exhaustive allowlist for non-admin workspace rollout. Existing apiGroup and
// internalGroup retain require(true); no unclassified business route is admitted.
var workspaceRoutePolicies = []workspaceRoutePolicy{
	{"GET", "/api/v1/workspaces/{workspace}/provider-grants", "workspace", "providers.read", func(s *Server) http.HandlerFunc { return s.ListWorkspaceProviderGrantsAPI }},
	{"POST", "/api/v1/workspaces/{workspace}/provider-grants", "workspace", "credentials.manage", func(s *Server) http.HandlerFunc { return s.SaveWorkspaceProviderGrantAPI }},
	{"DELETE", "/api/v1/workspaces/{workspace}/provider-grants/{id}", "workspace", "credentials.manage", func(s *Server) http.HandlerFunc { return s.DeleteWorkspaceProviderGrantAPI }},
	{"GET", "/api/v1/permissions/capabilities", "workspace", "permissions.read", func(s *Server) http.HandlerFunc { return s.PermissionCapabilitiesAPI }},
	{"GET", "/api/v1/permissions/presets", "workspace", "permissions.read", func(s *Server) http.HandlerFunc { return s.PermissionPresetsAPI }},
	{"GET", "/auth/workspaces", "self", "", func(s *Server) http.HandlerFunc { return s.ListWorkspacesAPI }},
	{"GET", "/auth/workspaces/{workspace}/capabilities", "self", "", func(s *Server) http.HandlerFunc { return s.WorkspaceCapabilitiesAPI }},
	{"POST", "/auth/invitations/accept", "self", "", func(s *Server) http.HandlerFunc { return s.AcceptWorkspaceInvitationAPI }},
	{"GET", "/api/v1/workspaces", "self", "", func(s *Server) http.HandlerFunc { return s.ListWorkspacesAPI }},
	{"POST", "/api/v1/workspaces", "platform", "", func(s *Server) http.HandlerFunc { return s.CreateWorkspaceAPI }},
	{"GET", "/api/v1/workspaces/{workspace}", "workspace", "workspace.read", func(s *Server) http.HandlerFunc { return s.GetWorkspaceAPI }},
	{"DELETE", "/api/v1/workspaces/{workspace}", "workspace", "workspace.archive", func(s *Server) http.HandlerFunc { return s.ArchiveWorkspaceAPI }},
	{"PUT", "/api/v1/workspaces/{workspace}", "workspace", "workspace.write", func(s *Server) http.HandlerFunc { return s.UpdateWorkspaceAPI }},
	{"GET", "/api/v1/workspaces/{workspace}/members", "workspace", "members.read", func(s *Server) http.HandlerFunc { return s.ListWorkspaceMembersAPI }},
	{"PUT", "/api/v1/workspaces/{workspace}/members/{user}", "workspace", "members.manage", func(s *Server) http.HandlerFunc { return s.SetWorkspaceMemberAPI }},
	{"GET", "/api/v1/workspaces/{workspace}/invitations", "workspace", "members.manage", func(s *Server) http.HandlerFunc { return s.ListWorkspaceInvitationsAPI }},
	{"POST", "/api/v1/workspaces/{workspace}/invitations", "workspace", "members.manage", func(s *Server) http.HandlerFunc { return s.CreateWorkspaceInvitationAPI }},
	{"GET", "/api/v1/permissions", "workspace", "permissions.read", func(s *Server) http.HandlerFunc { return s.ListPermissionsAPI }},
	{"POST", "/api/v1/permissions", "workspace", "permissions.manage", func(s *Server) http.HandlerFunc { return s.SavePermissionAPI }},
	{"PUT", "/api/v1/permissions/{id}", "workspace", "permissions.manage", func(s *Server) http.HandlerFunc { return s.SavePermissionAPI }},
	{"DELETE", "/api/v1/permissions/{id}", "workspace", "permissions.manage", func(s *Server) http.HandlerFunc { return s.DeletePermissionAPI }},
	{"GET", "/api/v1/user-permissions/{user}", "workspace", "permissions.read", func(s *Server) http.HandlerFunc { return s.GetUserPermissionsAPI }},
	{"PUT", "/api/v1/user-permissions/{user}", "workspace", "permissions.manage", func(s *Server) http.HandlerFunc { return s.SetUserPermissionsAPI }},
	{"PUT", "/api/v1/users-denied/{user}", "workspace", "permissions.manage", func(s *Server) http.HandlerFunc { return s.SetUserDeniedAPI }},
	{"GET", "/api/v1/users-effective/{user}", "workspace", "permissions.read", func(s *Server) http.HandlerFunc { return s.UserEffectiveAPI }},
	{"GET", "/api/v1/permission-mappings", "workspace", "permissions.read", func(s *Server) http.HandlerFunc { return s.ListPermissionMappingsAPI }},
	{"POST", "/api/v1/permission-mappings", "workspace", "permissions.manage", func(s *Server) http.HandlerFunc { return s.SavePermissionMappingAPI }},
	{"DELETE", "/api/v1/permission-mappings/{id}", "workspace", "permissions.manage", func(s *Server) http.HandlerFunc { return s.DeletePermissionMappingAPI }},
}

func (s *Server) registerWorkspaceRoutes(mux *ada.Server, base string) {
	for _, route := range workspaceRoutePolicies {
		if !validWorkspaceRoutePolicy(route) {
			mux.HandleWithMethod(route.Method, base+route.Path, func(w http.ResponseWriter, r *http.Request) { nativeError(w, 403, "route policy unavailable") })
			continue
		}
		inner := route.Handler(s)
		if route.Class == "platform" {
			next := inner
			inner = func(w http.ResponseWriter, r *http.Request) {
				p, ok := service.AccessPrincipalFromContext(r.Context())
				if !ok || !p.PlatformAdmin {
					nativeError(w, 403, "installation administrator required")
					return
				}
				next(w, r)
			}
		}
		handler := s.workspaceAuthentication(route.Class == "workspace", route.Capability)(inner)
		mux.HandleWithMethod(route.Method, base+route.Path, handler.ServeHTTP)
	}
}

func validWorkspaceRoutePolicy(route workspaceRoutePolicy) bool {
	if route.Handler == nil {
		return false
	}
	switch route.Class {
	case "self", "platform":
		return route.Capability == ""
	case "workspace":
		c, ok := service.KnownAccessCapability(route.Capability)
		return ok && !c.PlatformOnly
	default:
		return false
	}
}
func workspaceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrAccessResourceNotFound):
		nativeError(w, 404, "workspace resource not found")
	case errors.Is(err, service.ErrWorkspaceRequired):
		nativeError(w, 400, "workspace required")
	case errors.Is(err, service.ErrAccessDenied):
		nativeError(w, 403, "workspace access denied")
	case errors.Is(err, service.ErrWorkspaceConflict), errors.Is(err, service.ErrAuthConflict):
		nativeError(w, 409, "workspace operation conflicts with current state")
	default:
		nativeError(w, 503, "workspace operation unavailable")
	}
}
func (s *Server) workspaceStore(w http.ResponseWriter) service.WorkspaceStorer {
	store, ok := s.store.(service.WorkspaceStorer)
	if !ok {
		nativeError(w, 503, "workspace store unavailable")
		return nil
	}
	return store
}

func (s *Server) GetWorkspaceAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceStore(w)
	if store == nil {
		return
	}
	v, err := store.GetWorkspace(r.Context())
	if err != nil {
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, v, 200)
}
func (s *Server) ArchiveWorkspaceAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceStore(w)
	if store == nil {
		return
	}
	v, err := store.GetWorkspace(r.Context())
	if err != nil {
		workspaceError(w, err)
		return
	}
	v.Archived = true
	if _, err = store.UpdateWorkspace(r.Context(), *v); err != nil {
		workspaceError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) ListWorkspacesAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceStore(w)
	if store == nil {
		return
	}
	rows, err := store.ListWorkspaces(r.Context())
	if err != nil {
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, map[string]any{"items": rows}, 200)
}
func (s *Server) WorkspaceCapabilitiesAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceStore(w)
	if store == nil {
		return
	}
	a, ok := service.AccessPrincipalFromContext(r.Context())
	if !ok {
		workspaceError(w, service.ErrAccessDenied)
		return
	}
	_, report, err := store.ResolveWorkspaceAccess(r.Context(), r.PathValue("workspace"), a.UserID, a.SessionID)
	if err != nil {
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, report, 200)
}
func (s *Server) CreateWorkspaceAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceStore(w)
	if store == nil {
		return
	}
	var req struct {
		Name    string `json:"name"`
		OwnerID string `json:"owner_id"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	// An omitted owner means the creating administrator, so a workspace is
	// never created without an active owner.
	if req.OwnerID == "" {
		if p, ok := service.AccessPrincipalFromContext(r.Context()); ok {
			req.OwnerID = p.UserID
		}
	}
	v, err := store.CreateWorkspace(r.Context(), req.Name, req.OwnerID)
	if err != nil {
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, v, 201)
}
func (s *Server) UpdateWorkspaceAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceStore(w)
	if store == nil {
		return
	}
	var req struct {
		Name             string `json:"name"`
		Archived         bool   `json:"archived"`
		ExecutionEnabled bool   `json:"execution_enabled"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	v, err := store.UpdateWorkspace(r.Context(), service.Workspace{ID: r.PathValue("workspace"), Name: req.Name, Archived: req.Archived, ExecutionEnabled: req.ExecutionEnabled})
	if err != nil {
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, v, 200)
}
func (s *Server) ListWorkspaceMembersAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceStore(w)
	if store == nil {
		return
	}
	rows, err := store.ListWorkspaceMembers(r.Context())
	if err != nil {
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, map[string]any{"items": rows}, 200)
}
func (s *Server) SetWorkspaceMemberAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceStore(w)
	if store == nil {
		return
	}
	var req struct {
		Role   string `json:"role"`
		Status string `json:"status"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	err := store.SetWorkspaceMember(r.Context(), service.WorkspaceMembership{WorkspaceID: r.PathValue("workspace"), UserID: r.PathValue("user"), Role: req.Role, Status: req.Status})
	if err != nil {
		workspaceError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) ListWorkspaceInvitationsAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceStore(w)
	if store == nil {
		return
	}
	rows, err := store.ListWorkspaceInvitations(r.Context())
	if err != nil {
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, map[string]any{"items": rows}, 200)
}
func workspaceInvitationHash(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}
func (s *Server) CreateWorkspaceInvitationAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceStore(w)
	if store == nil {
		return
	}
	var req struct {
		Role      string    `json:"role"`
		UserID    string    `json:"user_id"`
		Email     string    `json:"email"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		workspaceError(w, err)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(entropy[:])
	if req.ExpiresAt.IsZero() {
		req.ExpiresAt = time.Now().Add(24 * time.Hour)
	}
	v, err := store.CreateWorkspaceInvitation(r.Context(), service.WorkspaceInvitation{WorkspaceID: r.PathValue("workspace"), Role: req.Role, UserID: req.UserID, Email: req.Email, ExpiresAt: req.ExpiresAt, TokenHash: workspaceInvitationHash(token)})
	if err != nil {
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, map[string]any{"invitation": v, "token": token}, 201)
}
func (s *Server) AcceptWorkspaceInvitationAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceStore(w)
	if store == nil {
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(req.Token)
	if err != nil || len(raw) != 32 {
		workspaceError(w, service.ErrAccessDenied)
		return
	}
	m, err := store.AcceptWorkspaceInvitation(r.Context(), workspaceInvitationHash(req.Token))
	if err != nil {
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, m, 200)
}
