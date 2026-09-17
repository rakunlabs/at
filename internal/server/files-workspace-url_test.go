package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/rakunlabs/ada"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func TestFileWorkspaceURLAdmissionPostgres(t *testing.T) {
	p := postgrestest.New(t, nil)
	admin, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "admin", PasswordHash: testPasswordHash, Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	user, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "reader", PasswordHash: testPasswordHash}, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx := service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: admin.ID})
	own, err := p.CreateWorkspace(ctx, "Owned", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := p.CreateWorkspace(ctx, "Not admitted", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	actor, _, err := p.ResolveWorkspaceAccess(ctx, own.ID, admin.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SetWorkspaceMember(service.WithAccessPrincipal(ctx, actor), service.WorkspaceMembership{WorkspaceID: own.ID, UserID: user.ID, Role: "viewer", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: p, nativeAuth: a}
	mux := ada.New()
	a.register(mux, "/at")
	group := mux.Group("/at/api/v1")
	group.Use(s.workspaceAuthentication(true, ""))
	admitted := func(w http.ResponseWriter, r *http.Request) {
		principal, _ := service.AccessPrincipalFromContext(r.Context())
		if principal.WorkspaceID != own.ID {
			t.Errorf("wrong admitted workspace: %s", principal.WorkspaceID)
		}
		if r.Header.Get("Range") != "" {
			w.Header().Set("Content-Range", "bytes 0-3/4")
			w.WriteHeader(206)
			return
		}
		w.WriteHeader(200)
	}
	group.GET("/files/serve", admitted)
	group.HEAD("/files/serve", admitted)
	group.POST("/files/serve", admitted)
	group.GET("/files/browse", admitted)
	cookie := nativeLoginCookie(t, mux, "reader")
	for _, tt := range []struct {
		name, method, path, header, byteRange string
		cookie                                *http.Cookie
		want                                  int
	}{
		{"native image", "GET", "files/serve?workspace_id=" + own.ID, "", "", cookie, 200},
		{"native HEAD", "HEAD", "files/serve?workspace_id=" + own.ID, "", "", cookie, 200},
		{"video seek", "GET", "files/serve?workspace_id=" + own.ID, "", "bytes=0-3", cookie, 206},
		{"matching header", "GET", "files/serve?workspace_id=" + own.ID, own.ID, "", cookie, 200},
		{"existing header", "GET", "files/serve", own.ID, "", cookie, 200},
		{"no selector", "GET", "files/serve", "", "", cookie, 400},
		{"ambiguous selector", "GET", "files/serve?workspace_id=" + own.ID, foreign.ID, "", cookie, 400},
		{"duplicate selector", "GET", "files/serve?workspace_id=" + own.ID + "&workspace_id=" + foreign.ID, "", "", cookie, 400},
		{"foreign workspace", "GET", "files/serve?workspace_id=" + foreign.ID, "", "", cookie, 403},
		{"unauthenticated", "GET", "files/serve?workspace_id=" + own.ID, "", "", nil, 401},
		{"not a general fallback", "GET", "files/browse?workspace_id=" + own.ID, "", "", cookie, 400},
		{"not for writes", "POST", "files/serve?workspace_id=" + own.ID, "", "", cookie, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := nativeRequest(mux, tt.method, "/at/api/v1/"+tt.path, "", a.cfg.Origin, tt.cookie, func(r *http.Request) {
				if tt.header != "" {
					r.Header.Set("X-AT-Workspace-ID", tt.header)
				}
				if tt.byteRange != "" {
					r.Header.Set("Range", tt.byteRange)
				}
			})
			if w.Code != tt.want {
				t.Fatalf("got %d want %d: %s", w.Code, tt.want, w.Body)
			}
		})
	}
	if _, err := p.InvalidateAuthUser(t.Context(), user.ID, false); err != nil {
		t.Fatal(err)
	}
	w := nativeRequest(mux, "GET", "/at/api/v1/files/serve?workspace_id="+own.ID, "", a.cfg.Origin, cookie)
	if w.Code != 401 || !strings.Contains(w.Body.String(), "authentication required") {
		t.Fatal("saved file URL bypassed session revocation")
	}
}
