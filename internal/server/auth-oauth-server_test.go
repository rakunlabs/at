package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/nativeauth"
	"github.com/rakunlabs/at/internal/nativeauth/nativeauthtest"
	"github.com/rakunlabs/at/internal/service"
)

func TestNativeOAuthReturns(t *testing.T) {
	a, _, mux := nativeFixture(t)
	admin := nativeauthtest.LoginCookie(t, mux, "admin")
	reader := nativeauthtest.LoginCookie(t, mux, "reader")
	// A second session of the SAME administrator must not accept the first's state.
	id := nativeauth.Identity(&service.AuthUser{ID: "admin", Admin: true})
	id.Claims = map[string]any{"session_version": int64(0)}
	pair, err := a.Issue(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	other := &http.Cookie{Name: admin.Name, Value: pair.Access.Value}
	exchanges := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exchanges++
		fmt.Fprint(w, `{"access_token":"token"}`)
	}))
	defer upstream.Close()
	vars := newFakeVariableStore()
	vars.vars["id"] = &service.Variable{Key: "test_client_id", Value: "client"}
	vars.vars["secret"] = &service.Variable{Key: "test_client_secret", Value: "secret"}
	s := &Server{nativeAuth: a, config: nativeauthtest.Config(), variableStore: vars, builtinConnectors: []service.Connector{{
		Slug: "test", AuthKind: service.ConnectorAuthOAuth2,
		OAuth: &service.ConnectorOAuth{AuthURL: "https://provider.example/authorize", TokenURL: upstream.URL},
	}}}
	api := mux.Group("/at/api/v1/oauth")
	api.Use(a.Require(true))
	api.GET("/start", s.OAuthStartAPI)
	api.GET("/manual-url", s.OAuthManualAuthURLAPI)
	api.GET("/callback", s.OAuthCallbackAPI)
	api.GET("/code-display", s.OAuthCodeDisplayAPI)
	api.POST("/callback", s.OAuthCallbackAPI) // Exercise middleware denial, not router 405.
	api.POST("/exchange", s.OAuthExchangeAPI)
	start := func(manual bool) string {
		t.Helper()
		path := "start"
		if manual {
			path = "manual-url"
		}
		w := nativeauthtest.Request(mux, "GET", "/at/api/v1/oauth/"+path+"?provider=test", "", "", admin)
		if w.Code != 200 {
			t.Fatalf("start: %d %s", w.Code, w.Body)
		}
		var data map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(data["url"])
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(u.Query().Get("redirect_uri"), "https://at.example/at/api/v1/oauth/") {
			t.Fatal(u)
		}
		state := u.Query().Get("state")
		if len(state) != 43 || state == "test" {
			t.Fatalf("nonopaque state: %q", state)
		}
		return state
	}
	request := func(method, path, state, origin, mode, dest string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/at/api/v1/oauth/"+path+"?code=code&state="+url.QueryEscape(state), nil)
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		r.Header.Set("Sec-Fetch-Mode", mode)
		r.Header.Set("Sec-Fetch-Dest", dest)
		r.Header.Set("Origin", origin)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	for _, target := range []string{"callback", "code-display"} {
		t.Run(target, func(t *testing.T) {
			state := start(target == "code-display")
			wrongTarget := "callback"
			if target == wrongTarget {
				wrongTarget = "code-display"
			}
			for _, tc := range []struct {
				name, method, path, state, origin, mode, dest string
				cookie                                        *http.Cookie
				status                                        int
			}{
				{"missing session", "GET", target, state, "", "navigate", "document", nil, 401},
				{"nonadmin", "GET", target, state, "", "navigate", "document", reader, 403},
				{"other session", "GET", target, state, "", "navigate", "document", other, 403},
				{"wrong endpoint", "GET", wrongTarget, state, "", "navigate", "document", admin, 403},
				{"foreign origin", "GET", target, state, "https://evil.example", "navigate", "document", admin, 403},
				{"null origin", "GET", target, state, "null", "navigate", "document", admin, 403},
				{"fetch", "GET", target, state, "", "cors", "empty", admin, 403},
				{"iframe", "GET", target, state, "", "navigate", "iframe", admin, 403},
				{"missing state", "GET", target, "", "", "navigate", "document", admin, 403},
				{"forged state", "GET", target, "test::conn::attacker", "", "navigate", "document", admin, 403},
				{"hostile post", "POST", "callback", state, "https://evil.example", "navigate", "document", admin, 403},
				{"cross-site post", "POST", "callback", state, a.Origin(), "navigate", "document", admin, 403},
				{"exchange post", "POST", "exchange", state, "https://evil.example", "navigate", "document", admin, 403},
				{"start navigation", "GET", "start", state, "", "navigate", "document", admin, 403},
			} {
				t.Run(tc.name, func(t *testing.T) {
					w := request(tc.method, tc.path, tc.state, tc.origin, tc.mode, tc.dest, tc.cookie)
					if w.Code != tc.status {
						t.Fatalf("%d: %s", w.Code, w.Body)
					}
				})
			}
			before := exchanges
			w := request("GET", target, state, "", "navigate", "document", admin)
			if w.Code != 200 {
				t.Fatalf("return: %d %s", w.Code, w.Body)
			}
			if target == "callback" && (exchanges != before+1 || len(vars.created) != 1) {
				t.Fatal("callback did not exchange and persist token")
			}
			if target == "code-display" && !strings.Contains(w.Body.String(), `id="code">code</div>`) {
				t.Fatal(w.Body)
			}
			before = exchanges
			if w := request("GET", target, state, "", "navigate", "document", admin); w.Code != 403 {
				t.Fatal("replay accepted")
			}
			if exchanges != before {
				t.Fatal("replay exchanged credentials")
			}
			state = start(target == "code-display")
			r := httptest.NewRequest("GET", "/at/api/v1/oauth/"+target+"?error=access_denied&state="+state, nil)
			r.AddCookie(admin)
			w = httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "access_denied") {
				t.Fatalf("provider error: %d %s", w.Code, w.Body)
			}
			if w := request("GET", target, state, "", "navigate", "document", admin); w.Code != 403 {
				t.Fatal("provider error did not consume state")
			}
			state = start(target == "code-display")
			a.ExpireOAuthStateForTests(state)
			if w := request("GET", target, state, "", "navigate", "document", admin); w.Code != 403 {
				t.Fatal("expired state accepted")
			}
		})
	}
}

func TestOAuthCodeDisplayLegacyAndEscaping(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	s.OAuthCodeDisplayAPI(w, httptest.NewRequest("GET", "/api/v1/oauth/code-display?code="+url.QueryEscape("<script>alert(1)</script>"), nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), "<script>alert") || !strings.Contains(w.Body.String(), "&lt;script&gt;") {
		t.Fatalf("legacy display: %d %s", w.Code, w.Body)
	}
}
