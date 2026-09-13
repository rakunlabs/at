package server

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/password"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func TestWorkspaceHTTPAdmissionAndRestrictedRollout(t *testing.T) {
	p := postgrestest.New(t, nil)
	admin, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "admin", PasswordHash: password.Dummy, Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	user, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "reader", PasswordHash: password.Dummy}, false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: p, nativeAuth: a}
	mux := ada.New()
	a.register(mux, "/at")
	s.registerWorkspaceRoutes(mux, "/at")
	api := mux.Group("/at/api")
	api.Use(s.requireUnscopedWorkspacePlatform())
	api.GET("/v1/agents", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-AT-Workspace-ID"); got != "" {
			t.Errorf("stale workspace selection reached platform handler: %q", got)
		}
		w.WriteHeader(200)
	})
	ctx := service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: admin.ID})
	one, err := p.CreateWorkspace(ctx, "One", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	two, err := p.CreateWorkspace(ctx, "Two", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []*service.Workspace{one, two} {
		actor, _, err := p.ResolveWorkspaceAccess(ctx, w.ID, admin.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if err = p.SetWorkspaceMember(service.WithAccessPrincipal(ctx, actor), service.WorkspaceMembership{WorkspaceID: w.ID, UserID: user.ID, Role: "viewer", Status: "active"}); err != nil {
			t.Fatal(err)
		}
	}
	cookie := nativeLoginCookie(t, mux, "reader")
	call := func(method, path, body, workspace, origin string, c *http.Cookie, bearer string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if workspace != "" {
			r.Header.Set("X-AT-Workspace-ID", workspace)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if c != nil {
			r.AddCookie(c)
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		return rec
	}
	for _, tt := range []struct {
		method, path, body, workspace, origin string
		want                                  int
	}{
		{"GET", "/at/auth/workspaces", "", "", "", 200},
		{"GET", "/at/api/v1/permissions", "", "", "", 400},
		{"GET", "/at/api/v1/permissions", "", one.ID, "", 200},
		{"GET", "/at/api/v1/permissions", "", "foreign", "", 403},
		{"GET", "/at/api/v1/workspaces/" + two.ID + "/members", "", one.ID, "", 404},
		{"POST", "/at/api/v1/permissions", `{"key":"bad","keys":["agents.write"]}`, one.ID, "https://at.example", 403},
		{"POST", "/at/api/v1/workspaces", `{"name":"Bad","owner_id":"` + user.ID + `"}`, "", "https://at.example", 403},
		{"POST", "/at/auth/invitations/accept", `{"token":"bad"}`, "", "https://evil.test", 403},
		// Non-administrator: refused for lacking platform rights, not for scope.
		{"GET", "/at/api/v1/agents", "", one.ID, "", 403},
	} {
		rec := call(tt.method, tt.path, tt.body, tt.workspace, tt.origin, cookie, "")
		if rec.Code != tt.want {
			t.Fatalf("%s %s: %d want %d %s", tt.method, tt.path, rec.Code, tt.want, rec.Body)
		}
	}
	adminCookie := nativeLoginCookie(t, mux, "admin")
	// Installation-wide data must never be presented as selected-workspace data,
	// so an unpartitioned route answers but labels its scope.
	if rec := call("GET", "/at/api/v1/agents", "", one.ID, "", adminCookie, ""); rec.Code != 200 || rec.Header().Get("X-AT-Scope") != "installation" {
		t.Fatalf("platform route mislabelled scope: %d %q", rec.Code, rec.Header().Get("X-AT-Scope"))
	}
	if rec := call("GET", "/at/api/v1/permissions", "", one.ID, "", adminCookie, ""); rec.Code != 200 || rec.Header().Get("X-AT-Scope") != "workspace" {
		t.Fatalf("workspace route mislabelled scope: %d %q", rec.Code, rec.Header().Get("X-AT-Scope"))
	}
	var wg sync.WaitGroup
	for _, w := range []*service.Workspace{one, two} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := call("GET", "/at/auth/workspaces/"+w.ID+"/capabilities", "", "", "", cookie, "")
			var report service.EffectiveAccess
			if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &report) != nil || report.WorkspaceID != w.ID || report.UserID != user.ID {
				t.Errorf("request scope mixed: %d %s", rec.Code, rec.Body)
			}
		}()
	}
	wg.Wait()
	access, refresh, err := nativeCredentialPair()
	if err != nil {
		t.Fatal(err)
	}
	session := service.AuthSession{Hash: "workspace-mobile", Transport: "mobile", UserID: user.ID, Version: user.SessionVersion, AccessHash: nativeSessionHash(access), RefreshHash: nativeSessionHash(refresh), AccessExpiresAt: time.Now().Add(time.Minute), ExpiresAt: time.Now().Add(time.Hour)}
	if err = p.CreateAuthSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if rec := call("GET", "/at/auth/workspaces", "", "", "", nil, access); rec.Code != 200 {
		t.Fatalf("mobile admission %d %s", rec.Code, rec.Body)
	}
	actor, _, err := p.ResolveWorkspaceAccess(ctx, one.ID, admin.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = p.SetWorkspaceMember(service.WithAccessPrincipal(ctx, actor), service.WorkspaceMembership{WorkspaceID: one.ID, UserID: user.ID, Role: "viewer", Status: "revoked"}); err != nil {
		t.Fatal(err)
	}
	if rec := call("GET", "/at/api/v1/permissions", "", one.ID, "", cookie, ""); rec.Code != 403 {
		t.Fatalf("revoked membership admitted %d", rec.Code)
	}
	if rec := call("GET", "/at/auth/workspaces", "", "", "", nil, ""); rec.Code != 401 {
		t.Fatalf("anonymous admitted %d", rec.Code)
	}
}

func TestWorkspaceRoutePolicyCoverage(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range workspaceRoutePolicies {
		if !validWorkspaceRoutePolicy(r) {
			t.Fatalf("invalid route policy: %+v", r)
		}
		key := r.Method + " " + r.Path
		if seen[key] {
			t.Fatalf("duplicate policy %s", key)
		}
		seen[key] = true
		switch r.Class {
		case "self", "platform":
			if r.Capability != "" {
				t.Fatalf("unexpected capability %s", key)
			}
		case "workspace":
			c, ok := service.KnownAccessCapability(r.Capability)
			if !ok || c.PlatformOnly {
				t.Fatalf("invalid workspace capability %s", key)
			}
		default:
			t.Fatalf("unclassified route %s", key)
		}
	}
	unknown := workspaceRoutePolicies[0]
	unknown.Class = "unclassified"
	if validWorkspaceRoutePolicy(unknown) {
		t.Fatal("unclassified policy accepted")
	}
	// Enumerate every literal server.go route and fail if a new routing group is
	// introduced without classification. The full inventory is emitted with -v.
	data, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	// Management admission must stay deny-by-default: workspace-classified
	// business routes admit scoped members, everything else installation-only.
	for _, gate := range []string{"apiGroup.Use(runtimeAuth.withRuntime, s.workspaceBusinessAuthentication())", "internalGroup.Use(runtimeAuth.require(true))"} {
		if !strings.Contains(string(data), gate) {
			t.Fatalf("restricted rollout gate removed: %s", gate)
		}
	}
	f, err := parser.ParseFile(token.NewFileSet(), "server.go", data, 0)
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string]string{"apiGroup": "platform", "settingsGroup": "platform", "internalGroup": "platform", "gatewayGroup": "token", "webhookGroup": "machine-webhook", "baseGroup": "public-static", "mux": "public"}
	count := 0
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "Handle":
		default:
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		group, ok := sel.X.(*ast.Ident)
		if !ok {
			t.Error("unclassified chained route")
			return true
		}
		class, ok := groups[group.Name]
		if !ok {
			t.Errorf("unclassified route group %s", group.Name)
		}
		path, _ := strconv.Unquote(lit.Value)
		if group.Name == "gatewayGroup" && strings.HasPrefix(path, "/v1/health") {
			class = "public-health"
		}
		t.Logf("%s %s %s %s", class, group.Name, sel.Sel.Name, path)
		count++
		return true
	})
	if count < 200 {
		t.Fatalf("inventory unexpectedly small: %d", count)
	}
}

func TestWorkspaceHTTPRoleManagement(t *testing.T) {
	p := postgrestest.New(t, nil)
	platform, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "platform", PasswordHash: password.Dummy, Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	root := service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: platform.ID})
	w, err := p.CreateWorkspace(root, "Roles", platform.ID)
	if err != nil {
		t.Fatal(err)
	}
	actor, _, err := p.ResolveWorkspaceAccess(root, w.ID, platform.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	root = service.WithAccessPrincipal(root, actor)
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: p, nativeAuth: a}
	mux := ada.New()
	s.registerWorkspaceRoutes(mux, "/at")
	target, err := p.CreateAuthUser(root, service.AuthUser{Username: "target", PasswordHash: password.Dummy}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"viewer", "member", "admin", "owner"} {
		t.Run(role, func(t *testing.T) {
			u, err := p.CreateAuthUser(root, service.AuthUser{Username: role, PasswordHash: password.Dummy}, false)
			if err != nil {
				t.Fatal(err)
			}
			if err = p.SetWorkspaceMember(root, service.WorkspaceMembership{WorkspaceID: w.ID, UserID: u.ID, Role: role, Status: "active"}); err != nil {
				t.Fatal(err)
			}
			id := authIdentity(u)
			id.Claims = map[string]any{"session_version": u.SessionVersion}
			pair, err := a.Issue(root, id)
			if err != nil {
				t.Fatal(err)
			}
			cookie := &http.Cookie{Name: a.session.CookieName, Value: pair.Access.Value}
			call := func(method, path, body string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, path, strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Origin", "https://at.example")
				r.Header.Set("X-AT-Workspace-ID", w.ID)
				r.AddCookie(cookie)
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, r)
				return rec
			}
			if rec := call("GET", "/at/api/v1/workspaces/"+w.ID+"/members", ""); rec.Code != 200 {
				t.Fatalf("list members %d %s", rec.Code, rec.Body)
			}
			if rec := call("GET", "/at/api/v1/user-permissions/foreign-user", ""); rec.Code != 404 {
				t.Fatalf("foreign permission member %d %s", rec.Code, rec.Body)
			}
			want := 403
			if role == "admin" || role == "owner" {
				want = 204
			}
			if rec := call("PUT", "/at/api/v1/workspaces/"+w.ID+"/members/"+target.ID, `{"role":"viewer","status":"active"}`); rec.Code != want {
				t.Fatalf("membership %d want %d %s", rec.Code, want, rec.Body)
			}
			want = 403
			if role == "owner" {
				want = 204
			}
			if rec := call("PUT", "/at/api/v1/workspaces/"+w.ID+"/members/"+target.ID, `{"role":"owner","status":"active"}`); rec.Code != want {
				t.Fatalf("owner grant %d want %d %s", rec.Code, want, rec.Body)
			}
			if rec := call("POST", "/at/api/v1/workspaces", `{"name":"Forbidden","owner_id":"`+u.ID+`"}`); rec.Code != 403 {
				t.Fatalf("workspace creation %d", rec.Code)
			}
			if rec := call("GET", "/at/api/v1/workspaces/"+w.ID, ""); rec.Code != 200 {
				t.Fatalf("workspace detail %d %s", rec.Code, rec.Body)
			}
			want = 403
			if role == "owner" {
				want = 204
			}
			if rec := call("DELETE", "/at/api/v1/workspaces/"+w.ID, ""); rec.Code != want {
				t.Fatalf("workspace archive %d want %d %s", rec.Code, want, rec.Body)
			}
		})
	}
}

// Guides back the in-product documentation page, which every signed-in member
// can open. Gating them behind the admin role left non-administrators staring
// at a load error, so they are admitted as a shared platform route while real
// administration stays admin-only.
func TestSharedPlatformRoutesAdmitNonAdministrators(t *testing.T) {
	p := postgrestest.New(t, nil)
	if _, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "admin", PasswordHash: password.Dummy, Admin: true}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "reader", PasswordHash: password.Dummy}, false); err != nil {
		t.Fatal(err)
	}
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	// The route matcher strips BasePath, so it must agree with where the group
	// is mounted or every pattern silently misses.
	s := &Server{store: p, nativeAuth: a, config: config.Server{BasePath: "/at"}}
	mux := ada.New()
	a.register(mux, "/at")
	api := mux.Group("/at/api")
	api.Use(s.workspaceBusinessAuthentication())
	reached := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }
	for _, route := range []struct{ method, path string }{
		{"GET", "/v1/guides"}, {"POST", "/v1/guides"}, {"GET", "/v1/guides/{id}"},
		{"PUT", "/v1/guides/{id}"}, {"DELETE", "/v1/guides/{id}"}, {"GET", "/v1/bots"},
	} {
		api.HandleWithMethod(route.method, route.path, reached)
	}
	// One login per user: the shared login limiter rejects a per-request sign-in.
	cookies := map[string]*http.Cookie{"reader": nativeLoginCookie(t, mux, "reader"), "admin": nativeLoginCookie(t, mux, "admin")}
	call := func(method, path, user string) int {
		r := httptest.NewRequest(method, path, strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		if path == "/at/api/v1/bots" {
			r.Header.Set("X-AT-Workspace-ID", "legacy-default")
		}
		r.Header.Set("Origin", a.cfg.Origin)
		r.AddCookie(cookies[user])
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		return rec.Code
	}
	for _, tt := range []struct {
		method, path, user string
		want               int
	}{
		{"GET", "/at/api/v1/guides", "reader", 200},
		{"POST", "/at/api/v1/guides", "reader", 200},
		{"GET", "/at/api/v1/guides/abc", "reader", 200},
		{"PUT", "/at/api/v1/guides/abc", "reader", 200},
		{"DELETE", "/at/api/v1/guides/abc", "reader", 200},
		{"GET", "/at/api/v1/guides", "admin", 200},
		// Bots require workspace admission; this reader has no membership.
		{"GET", "/at/api/v1/bots", "reader", 403},
		{"GET", "/at/api/v1/bots", "admin", 200},
	} {
		if got := call(tt.method, tt.path, tt.user); got != tt.want {
			t.Errorf("%s %s as %s: %d want %d", tt.method, tt.path, tt.user, got, tt.want)
		}
	}
	if code := func() int {
		r := httptest.NewRequest("GET", "/at/api/v1/guides", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		return rec.Code
	}(); code != 401 {
		t.Errorf("anonymous guide read: %d want 401", code)
	}
}
