package server

import (
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

func (s *Server) PermissionCapabilitiesAPI(w http.ResponseWriter, r *http.Request) {
	httpResponseJSON(w, map[string]any{"items": service.AccessCapabilities()}, 200)
}
func (s *Server) PermissionPresetsAPI(w http.ResponseWriter, r *http.Request) {
	items := make([]service.PermissionBundle, 0, 4)
	for _, role := range []string{"owner", "admin", "member", "viewer"} {
		b := service.PermissionBundle{ID: "role:" + role, Key: role, Name: role, Description: "Server-owned role preset", Keys: []string{}, KeyPatterns: map[string][]string{}}
		for _, g := range service.WorkspaceRoleGrants(role) {
			b.Keys = append(b.Keys, g.Capability)
		}
		items = append(items, b)
	}
	httpResponseJSON(w, map[string]any{"items": items}, 200)
}

func (s *Server) ListPermissionsAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceStore(w)
	if store == nil {
		return
	}
	rows, err := store.ListPermissions(r.Context())
	if err != nil {
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, map[string]any{"items": rows}, 200)
}
func (s *Server) SavePermissionAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceStore(w)
	if store == nil {
		return
	}
	var req service.PermissionBundle
	if !decodeNativeBody(w, r, &req) {
		return
	}
	if req.ID != "" && req.ID != r.PathValue("id") {
		workspaceError(w, service.ErrAccessDenied)
		return
	}
	req.ID = r.PathValue("id")
	v, err := store.SavePermission(r.Context(), req)
	if err != nil {
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, v, 200)
}
func (s *Server) DeletePermissionAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceStore(w)
	if store == nil {
		return
	}
	if err := store.DeletePermission(r.Context(), r.PathValue("id")); err != nil {
		workspaceError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) GetUserPermissionsAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceStore(w)
	if store == nil {
		return
	}
	ids, err := store.GetUserPermissions(r.Context(), r.PathValue("user"))
	if err != nil {
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, map[string]any{"permission_ids": ids}, 200)
}
func (s *Server) SetUserPermissionsAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceStore(w)
	if store == nil {
		return
	}
	var req struct {
		IDs []string `json:"permission_ids"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	if err := store.SetUserPermissions(r.Context(), r.PathValue("user"), req.IDs); err != nil {
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, map[string]any{"permission_ids": req.IDs}, 200)
}
func (s *Server) SetUserDeniedAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceStore(w)
	if store == nil {
		return
	}
	var req struct {
		Keys []string `json:"capability_keys"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	if err := store.SetUserDenied(r.Context(), r.PathValue("user"), req.Keys); err != nil {
		workspaceError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) UserEffectiveAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceStore(w)
	if store == nil {
		return
	}
	ctx, err := s.revalidateAccessPrincipal(r.Context())
	if err != nil {
		workspaceError(w, err)
		return
	}
	a, _ := service.AccessPrincipalFromContext(ctx)
	if !a.Allows("permissions.read", service.AccessResource{WorkspaceID: a.WorkspaceID}) {
		workspaceError(w, service.ErrAccessDenied)
		return
	}
	// Existence must be checked within workspace membership, even for platform users.
	if _, err = store.GetUserPermissions(ctx, r.PathValue("user")); err != nil {
		workspaceError(w, err)
		return
	}
	_, report, err := store.ResolveWorkspaceAccess(ctx, a.WorkspaceID, r.PathValue("user"), "")
	if err != nil {
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, report, 200)
}
func (s *Server) ListPermissionMappingsAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceStore(w)
	if store == nil {
		return
	}
	rows, err := store.ListPermissionMappings(r.Context())
	if err != nil {
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, map[string]any{"items": rows}, 200)
}
func (s *Server) SavePermissionMappingAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceStore(w)
	if store == nil {
		return
	}
	var req service.PermissionMapping
	if !decodeNativeBody(w, r, &req) {
		return
	}
	v, err := store.SavePermissionMapping(r.Context(), req)
	if err != nil {
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, v, 201)
}
func (s *Server) DeletePermissionMappingAPI(w http.ResponseWriter, r *http.Request) {
	store := s.workspaceStore(w)
	if store == nil {
		return
	}
	if err := store.DeletePermissionMapping(r.Context(), r.PathValue("id")); err != nil {
		workspaceError(w, err)
		return
	}
	w.WriteHeader(204)
}
