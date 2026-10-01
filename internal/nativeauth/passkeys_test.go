package nativeauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/ada"
	"golang.org/x/time/rate"

	"github.com/rakunlabs/at/internal/nativeauth/nativeauthtest"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func TestNativePasskeySignedPostgres(t *testing.T) {
	p := postgrestest.New(t, nil)
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "reader", PasswordHash: nativeauthtest.PasswordHash}, false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(nativeauthtest.Config(), p)
	if err != nil {
		t.Fatal(err)
	}
	a.LoginLimit = rate.NewLimiter(rate.Inf, 10000)
	mux := ada.New()
	a.Register(mux, "/at")
	session := nativeauthtest.LoginCookie(t, mux, "reader")
	key := nativeauthtest.NewSoftwarePasskey(t)
	challenge, ceremony := nativeauthtest.PasskeyBegin(t, mux, true, session, false)
	response := key.Response(t, true, challenge, a.cfg.Origin, "at.example", u.ID, 0x45, 0, false)
	w := nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/enroll/finish", response, a.cfg.Origin, ceremony, session)
	if w.Code != 204 {
		t.Fatalf("enrollment: %d %s", w.Code, w.Body)
	}
	if w := nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/enroll/finish", response, a.cfg.Origin, ceremony, session); w.Code != 401 {
		t.Fatal("replayed enrollment accepted")
	}
	keys, err := p.ListAuthPasskeys(t.Context(), u.ID)
	if err != nil || len(keys) != 1 || string(keys[0].Credential.UserHandle) != u.ID {
		t.Fatalf("persisted keys: %+v %v", keys, err)
	}
	list := nativeauthtest.Request(mux, "GET", "/at/auth/passkeys", "", "", session)
	if list.Code != 200 || strings.Contains(list.Body.String(), "PublicKey") || strings.Contains(list.Body.String(), "Credential") || !strings.Contains(list.Body.String(), `"last_used_at":null`) {
		t.Fatalf("key list: %d %s", list.Code, list.Body)
	}
	t.Run("anonymous begins cannot spend victim quota", func(t *testing.T) {
		for range 6 {
			nativeauthtest.PasskeyBegin(t, mux, false, nil, false)
		}
		challenge, ceremony := nativeauthtest.PasskeyBegin(t, mux, false, nil, false)
		body := key.Response(t, false, challenge, a.cfg.Origin, "at.example", u.ID, 5, 0, false)
		if w := nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/login/finish", body, a.cfg.Origin, ceremony); w.Code != 200 {
			t.Fatalf("legitimate login starved: %d %s", w.Code, w.Body)
		}
		_, enrollment := nativeauthtest.PasskeyBegin(t, mux, true, session, false)
		if c, err := p.ConsumeAuthChallenge(t.Context(), SessionHash(enrollment.Value)); err != nil || c == nil || c.Purpose != "enroll" {
			t.Fatalf("authenticated enrollment starved: %+v %v", c, err)
		}
	})
	for _, remember := range []bool{false, true} {
		t.Run(fmt.Sprintf("valid remember=%t", remember), func(t *testing.T) {
			challenge, ceremony := nativeauthtest.PasskeyBegin(t, mux, false, nil, remember)
			body := key.Response(t, false, challenge, a.cfg.Origin, "at.example", u.ID, 5, 0, false)
			w := nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/login/finish", body, a.cfg.Origin, ceremony)
			if w.Code != 200 {
				t.Fatalf("signed login: %d %s", w.Code, w.Body)
			}
			var login *http.Cookie
			for _, c := range w.Result().Cookies() {
				if c.Name == a.session.CookieName {
					login = c
				}
			}
			checkNativeLifetime(t, a, login, remember)
			if strings.Contains(w.Body.String(), login.Value) {
				t.Fatal("session leaked")
			}
			if w := nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/login/finish", body, a.cfg.Origin, ceremony); w.Code != 401 {
				t.Fatal("replayed login accepted")
			}
		})
	}
	for _, test := range []string{"wrong browser", "wrong challenge", "wrong user", "wrong origin", "wrong RP", "missing UV", "bad signature", "expired", "oversized", "HTTP origin", "wrong purpose", "malformed"} {
		t.Run(test, func(t *testing.T) {
			challenge, ceremony := nativeauthtest.PasskeyBegin(t, mux, false, nil, false)
			origin, rp, handle, flags, bad, header := a.cfg.Origin, "at.example", u.ID, byte(5), false, a.cfg.Origin
			usedCookie := ceremony
			path := "/at/auth/passkeys/login/finish"
			switch test {
			case "wrong browser":
				usedCookie = &http.Cookie{Name: ceremony.Name, Value: strings.Repeat("Z", 43)}
			case "wrong challenge":
				challenge = base64.RawURLEncoding.EncodeToString(make([]byte, 32))
			case "wrong user":
				handle = "another-user"
			case "wrong origin":
				origin = "https://evil.example"
			case "wrong RP":
				rp = "evil.example"
			case "missing UV":
				flags = 1
			case "bad signature":
				bad = true
			case "HTTP origin":
				header = "https://evil.example"
			case "wrong purpose":
				path = "/at/auth/passkeys/enroll/finish"
			case "expired":
				// Inject expired SessionData through a test adapter; PostgreSQL's
				// own expiration and atomic consume have separate store tests.
				a.keyStore = &expiredChallengeStore{AuthPasskeyStorer: p}
				defer func() { a.keyStore = p }()
			}
			body := key.Response(t, false, challenge, origin, rp, handle, flags, 0, bad)
			if test == "oversized" {
				body = strings.Repeat("x", 65537)
			}
			if test == "malformed" {
				body = "{"
			}
			w := nativeauthtest.PasskeyRequest(mux, path, body, header, usedCookie)
			if w.Code < 400 {
				t.Fatalf("invalid accepted: %d %s", w.Code, w.Body)
			}
			if test != "wrong browser" {
				if c, err := p.ConsumeAuthChallenge(t.Context(), SessionHash(ceremony.Value)); err != nil || c != nil {
					t.Fatalf("invalid attempt retained challenge: %+v %v", c, err)
				}
			} else {
				_, _ = p.ConsumeAuthChallenge(t.Context(), SessionHash(ceremony.Value))
			}
		})
	}
	for _, test := range []string{"wrong session", "logged out", "version changed", "missing UV", "bad signature", "wrong origin", "wrong RP", "duplicate"} {
		t.Run("enroll "+test, func(t *testing.T) {
			session := nativeauthtest.LoginCookie(t, mux, "reader")
			challenge, ceremony := nativeauthtest.PasskeyBegin(t, mux, true, session, false)
			origin, rp, flags, bad := a.cfg.Origin, "at.example", byte(0x45), false
			candidate := nativeauthtest.NewSoftwarePasskey(t)
			switch test {
			case "wrong session":
				session = nativeauthtest.LoginCookie(t, mux, "reader")
			case "logged out":
				if err := a.Revoke(t.Context(), session.Value); err != nil {
					t.Fatal(err)
				}
			case "version changed":
				if _, err := p.InvalidateAuthUser(t.Context(), u.ID, false); err != nil {
					t.Fatal(err)
				}
			case "missing UV":
				flags = 0x41
			case "bad signature":
				bad = true
			case "wrong origin":
				origin = "https://evil.example"
			case "wrong RP":
				rp = "evil.example"
			case "duplicate":
				candidate = key
			}
			body := candidate.Response(t, true, challenge, origin, rp, u.ID, flags, 0, bad)
			w := nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/enroll/finish", body, a.cfg.Origin, ceremony, session)
			if w.Code < 400 {
				t.Fatalf("invalid enrollment accepted: %d", w.Code)
			}
			if c, err := p.ConsumeAuthChallenge(t.Context(), SessionHash(ceremony.Value)); err != nil || c != nil {
				t.Fatal("enrollment challenge retained")
			}
		})
	}
	t.Run("concurrent signed counter", func(t *testing.T) {
		challenge1, cookie1 := nativeauthtest.PasskeyBegin(t, mux, false, nil, false)
		challenge2, cookie2 := nativeauthtest.PasskeyBegin(t, mux, false, nil, false)
		body1 := key.Response(t, false, challenge1, a.cfg.Origin, "at.example", u.ID, 5, 1, false)
		body2 := key.Response(t, false, challenge2, a.cfg.Origin, "at.example", u.ID, 5, 1, false)
		var winners atomic.Int32
		var wg sync.WaitGroup
		for _, req := range []struct {
			body   string
			cookie *http.Cookie
		}{{body1, cookie1}, {body2, cookie2}} {
			wg.Go(func() {
				w := nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/login/finish", req.body, a.cfg.Origin, req.cookie)
				if w.Code == 200 {
					winners.Add(1)
				} else if w.Code != 401 {
					t.Errorf("counter race: %d %s", w.Code, w.Body)
				}
			})
		}
		wg.Wait()
		if winners.Load() != 1 {
			t.Fatalf("signed counter winners: %d", winners.Load())
		}
	})
	// Capture a valid assertion, then delete its key. The version bump and
	// revocation must also invalidate an already-started login ceremony.
	session = nativeauthtest.LoginCookie(t, mux, "reader")
	challenge, ceremony = nativeauthtest.PasskeyBegin(t, mux, false, nil, false)
	body := key.Response(t, false, challenge, a.cfg.Origin, "at.example", u.ID, 5, 2, false)
	w = nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/"+keys[0].ID+"/delete", `{"current_password":"`+strings.Repeat("x", 32)+`"}`, a.cfg.Origin, session)
	if w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w := nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/login/finish", body, a.cfg.Origin, ceremony); w.Code != 401 {
		t.Fatal("deleted key logged in")
	}
	if w := nativeauthtest.Request(mux, "GET", "/at/auth/me", "", "", session); w.Code != 401 {
		t.Fatal("delete retained session")
	}
	// Password fallback and enrollment after removing the last key still work.
	session = nativeauthtest.LoginCookie(t, mux, "reader")
	challenge, ceremony = nativeauthtest.PasskeyBegin(t, mux, true, session, false)
	w = nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/enroll/finish", key.Response(t, true, challenge, a.cfg.Origin, "at.example", u.ID, 0x45, 0, false), a.cfg.Origin, ceremony, session)
	if w.Code != 204 {
		t.Fatalf("re-enrollment: %d %s", w.Code, w.Body)
	}
}

// Discoverable login: the sign-in screen no longer asks for a username, so the
// ceremony starts without one and the asserted credential ID names the account.
func TestNativePasskeyDiscoverableLoginPostgres(t *testing.T) {
	p := postgrestest.New(t, nil)
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "reader", PasswordHash: nativeauthtest.PasswordHash}, false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(nativeauthtest.Config(), p)
	if err != nil {
		t.Fatal(err)
	}
	a.LoginLimit = rate.NewLimiter(rate.Inf, 10000)
	mux := ada.New()
	a.Register(mux, "/at")
	session := nativeauthtest.LoginCookie(t, mux, "reader")
	key := nativeauthtest.NewSoftwarePasskey(t)
	challenge, ceremony := nativeauthtest.PasskeyBegin(t, mux, true, session, false)
	enrollment := key.Response(t, true, challenge, a.cfg.Origin, "at.example", u.ID, 0x45, 0, false)
	if w := nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/enroll/finish", enrollment, a.cfg.Origin, ceremony, session); w.Code != 204 {
		t.Fatalf("enrollment: %d %s", w.Code, w.Body)
	}
	begin := func(t *testing.T) (string, *http.Cookie) {
		t.Helper()
		w := nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/login/begin", `{"remember_me":false}`, a.cfg.Origin)
		if w.Code != 200 {
			t.Fatalf("usernameless begin: %d %s", w.Code, w.Body)
		}
		var result struct {
			PublicKey struct {
				Challenge string `json:"challenge"`
				RPID      string `json:"rpId"`
				Allow     []struct {
					ID string `json:"id"`
				} `json:"allowCredentials"`
			} `json:"publicKey"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		// An allow list would leak which credentials exist to an anonymous
		// caller and defeats the point: the authenticator chooses.
		if result.PublicKey.RPID != "at.example" || len(result.PublicKey.Allow) != 0 {
			t.Fatalf("discoverable options: %s", w.Body)
		}
		return result.PublicKey.Challenge, w.Result().Cookies()[0]
	}
	c, cookie := begin(t)
	w := nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/login/finish", key.Response(t, false, c, a.cfg.Origin, "at.example", u.ID, 5, 1, false), a.cfg.Origin, cookie)
	if w.Code != 200 {
		t.Fatalf("usernameless login: %d %s", w.Code, w.Body)
	}
	var issued *http.Cookie
	for _, got := range w.Result().Cookies() {
		if got.Name == a.session.CookieName && got.Value != "" {
			issued = got
		}
	}
	if issued == nil {
		t.Fatal("usernameless login issued no session")
	}
	if user, _, err := a.credentials.ResolveAuthAccess(t.Context(), SessionHash(issued.Value)); err != nil || user == nil || user.ID != u.ID {
		t.Fatalf("session identity: %+v %v", user, err)
	}
	t.Run("unknown credential", func(t *testing.T) {
		c, cookie := begin(t)
		stranger := nativeauthtest.NewSoftwarePasskey(t)
		if w := nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/login/finish", stranger.Response(t, false, c, a.cfg.Origin, "at.example", u.ID, 5, 1, false), a.cfg.Origin, cookie); w.Code != 401 {
			t.Fatalf("unenrolled credential accepted: %d %s", w.Code, w.Body)
		}
	})
	t.Run("disabled account", func(t *testing.T) {
		c, cookie := begin(t)
		if _, err := p.InvalidateAuthUser(t.Context(), u.ID, true); err != nil {
			t.Fatal(err)
		}
		if w := nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/login/finish", key.Response(t, false, c, a.cfg.Origin, "at.example", u.ID, 5, 2, false), a.cfg.Origin, cookie); w.Code != 401 {
			t.Fatalf("disabled account signed in: %d %s", w.Code, w.Body)
		}
	})
}

func (s *deadlineAuthStore) ConsumeAuthChallenge(ctx context.Context, hash string) (*service.AuthChallenge, error) {
	c, err := s.AuthPasskeyStorer.ConsumeAuthChallenge(ctx, hash)
	if c != nil && c.Purpose == "login" {
		if s.delay {
			c.Data.Expires = time.Now().Add(2 * time.Second)
		}
		s.deadline = c.Data.Expires
	}
	return c, err
}

func (s *deadlineAuthStore) AdvanceAuthPasskey(ctx context.Context, user, id string, version int64, old, next uint32, deadline time.Time) error {
	if deadline.IsZero() || !deadline.Equal(s.deadline) {
		return fmt.Errorf("counter admission lost ceremony deadline")
	}
	err := s.AuthPasskeyStorer.AdvanceAuthPasskey(ctx, user, id, version, old, next, deadline)
	if err == nil {
		s.advanced = true
		if s.delay {
			time.Sleep(time.Until(deadline) + 10*time.Millisecond)
		}
	}
	return err
}

func (s *deadlineAuthStore) CreateAuthSession(ctx context.Context, session service.AuthSession) error {
	if !s.deadline.IsZero() {
		if !session.AdmissionDeadline.Equal(s.deadline) {
			return fmt.Errorf("session admission lost ceremony deadline")
		}
		s.issued = true
	}
	return s.AuthStorer.CreateAuthSession(ctx, session)
}

func TestNativePasskeyLoginAdmissionDeadlinePostgres(t *testing.T) {
	p := postgrestest.New(t, nil)
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "reader", PasswordHash: nativeauthtest.PasswordHash}, false)
	if err != nil {
		t.Fatal(err)
	}
	store := &deadlineAuthStore{AuthStorer: p, AuthCredentialStorer: p, AuthPasskeyStorer: p}
	a, err := New(nativeauthtest.Config(), store)
	if err != nil {
		t.Fatal(err)
	}
	a.LoginLimit = rate.NewLimiter(rate.Inf, 100)
	mux := ada.New()
	a.Register(mux, "/at")
	session := nativeauthtest.LoginCookie(t, mux, "reader")
	key := nativeauthtest.NewSoftwarePasskey(t)
	challenge, ceremony := nativeauthtest.PasskeyBegin(t, mux, true, session, false)
	body := key.Response(t, true, challenge, a.cfg.Origin, "at.example", u.ID, 0x45, 0, false)
	if w := nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/enroll/finish", body, a.cfg.Origin, ceremony, session); w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
	for _, delay := range []bool{false, true} {
		store.delay, store.advanced, store.issued = delay, false, false
		challenge, ceremony = nativeauthtest.PasskeyBegin(t, mux, false, nil, true)
		body = key.Response(t, false, challenge, a.cfg.Origin, "at.example", u.ID, 5, 0, false)
		w := nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/login/finish", body, a.cfg.Origin, ceremony)
		if !store.advanced || !store.issued {
			t.Fatal("deadline did not reach both admissions", w.Code, w.Body)
		}
		if delay {
			if w.Code < 400 {
				t.Fatal("expired second admission accepted", w.Code, w.Body)
			}
			for _, c := range w.Result().Cookies() {
				if c.Name == a.session.CookieName && c.Value != "" {
					t.Fatal("expired admission issued session cookie")
				}
			}
		} else if w.Code != 200 {
			t.Fatal("valid admission rejected", w.Code, w.Body)
		}
	}
}

func (s *expiredChallengeStore) ConsumeAuthChallenge(ctx context.Context, hash string) (*service.AuthChallenge, error) {
	c, err := s.AuthPasskeyStorer.ConsumeAuthChallenge(ctx, hash)
	if c != nil {
		c.Data.Expires = time.Now().Add(-time.Minute)
	}
	return c, err
}

func checkNativeLifetime(t *testing.T, a *Auth, c *http.Cookie, remember bool) {
	t.Helper()
	if c == nil {
		t.Fatal("missing session cookie")
	}
	u, s, err := a.credentials.ResolveAuthAccess(t.Context(), SessionHash(c.Value))
	if err != nil || u == nil {
		t.Fatalf("session absent: %v", err)
	}
	want := nativeSessionTTL
	expires := s.ExpiresAt
	if remember {
		want = nativeRememberTTL
	}
	if delta := time.Until(expires); delta > want || delta < want-time.Minute {
		t.Fatalf("server TTL %v want %v", delta, want)
	}
	if remember {
		if c.MaxAge < 598 || c.MaxAge > 600 || !c.Expires.Equal(s.AccessExpiresAt.Truncate(time.Second)) {
			t.Fatalf("cookie/server expiry mismatch: %+v %v", c, expires)
		}
	} else if c.MaxAge != 0 || !c.Expires.IsZero() {
		t.Fatalf("non-remembered cookie persisted: %+v", c)
	}
}

func TestNativeRememberMe(t *testing.T) {
	a, _, mux := nativeFixture(t)
	for _, body := range []string{``, `,"remember_me":false`, `,"remember_me":true`} {
		w := nativeauthtest.Request(mux, "POST", "/at/auth/login", `{"username":"reader","password":"`+strings.Repeat("x", 32)+`"`+body+`}`, a.cfg.Origin, nil)
		if w.Code != 200 {
			t.Fatalf("login: %d %s", w.Code, w.Body)
		}
		checkNativeLifetime(t, a, w.Result().Cookies()[0], strings.Contains(body, "true"))
	}
	if w := nativeauthtest.Request(mux, "POST", "/at/auth/login", `{"username":"reader","password":"x","remember_me":"true"}`, a.cfg.Origin, nil); w.Code != 400 {
		t.Fatal("non-bool remember accepted")
	}
}

func TestNativeRememberConcurrentIssuance(t *testing.T) {
	a, f, _ := nativeFixture(t)
	u, err := f.GetAuthUser(t.Context(), "reader")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Go(func() {
			r := httptest.NewRequest("POST", "/at/auth/login", nil)
			w := httptest.NewRecorder()
			a.finishLogin(w, r, u, i%2 == 0)
			if w.Code != 200 {
				t.Errorf("issuance: %d", w.Code)
				return
			}
			checkNativeLifetime(t, a, w.Result().Cookies()[0], i%2 == 0)
		})
	}
	wg.Wait()
}

func TestNativePasskeyGuardsPostgres(t *testing.T) {
	p := postgrestest.New(t, nil)
	_, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "reader", PasswordHash: nativeauthtest.PasswordHash}, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.CreateAuthUser(t.Context(), service.AuthUser{Username: "admin", PasswordHash: nativeauthtest.PasswordHash, Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(nativeauthtest.Config(), p)
	if err != nil {
		t.Fatal(err)
	}
	a.LoginLimit = rate.NewLimiter(rate.Inf, 1000)
	mux := ada.New()
	a.Register(mux, "/at")
	session := nativeauthtest.LoginCookie(t, mux, "reader")
	for _, tt := range []struct {
		name, path, body, origin string
		session                  *http.Cookie
		code                     int
	}{
		{"password required", "/enroll/begin", `{"name":"key","current_password":"wrong"}`, a.cfg.Origin, session, 401},
		{"session required", "/enroll/begin", `{"name":"key","current_password":"` + strings.Repeat("x", 32) + `"}`, a.cfg.Origin, nil, 401},
		{"origin required", "/enroll/begin", `{}`, "", session, 403},
		{"name required", "/enroll/begin", `{"current_password":"x"}`, a.cfg.Origin, session, 400},
		{"unknown fields", "/enroll/begin", `{"name":"key","user_id":"other","current_password":"x"}`, a.cfg.Origin, session, 400},
		{"bounded begin", "/login/begin", `{"username":"` + strings.Repeat("x", 4097) + `"}`, a.cfg.Origin, nil, 413},
		{"keyless", "/login/begin", `{"username":"reader"}`, a.cfg.Origin, nil, 401},
		{"unknown", "/login/begin", `{"username":"unknown"}`, a.cfg.Origin, nil, 401},
		{"no remember coercion", "/login/begin", `{"username":"reader","remember_me":"true"}`, a.cfg.Origin, nil, 400},
		{"delete reauth", "/missing/delete", `{"current_password":"wrong"}`, a.cfg.Origin, session, 401},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys"+tt.path, tt.body, tt.origin, tt.session)
			if w.Code != tt.code {
				t.Fatalf("%d want %d: %s", w.Code, tt.code, w.Body)
			}
		})
	}
	challenge, ceremony := nativeauthtest.PasskeyBegin(t, mux, true, session, false)
	k := nativeauthtest.NewSoftwarePasskey(t)
	w := nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/enroll/finish", k.Response(t, true, challenge, a.cfg.Origin, "at.example", "", 0x45, 0, false), a.cfg.Origin, ceremony, session)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
	reader, _ := p.GetAuthUser(t.Context(), "reader")
	keys, _ := p.ListAuthPasskeys(t.Context(), reader.ID)
	admin := nativeauthtest.LoginCookie(t, mux, "admin")
	w = nativeauthtest.Request(mux, "GET", "/at/auth/passkeys", "", "", admin)
	if w.Code != 200 || strings.Contains(w.Body.String(), keys[0].ID) {
		t.Fatal("admin can list other's key", w.Code, w.Body)
	}
	w = nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/"+keys[0].ID+"/delete", `{"current_password":"`+strings.Repeat("x", 32)+`"}`, a.cfg.Origin, admin)
	if w.Code != 409 {
		t.Fatal("admin can delete other's key", w.Code, w.Body)
	}
	// Challenge/RP options must not derive from either request Host header.
	r := httptest.NewRequest("POST", "https://evil.example/at/auth/passkeys/login/begin", strings.NewReader(`{"username":"reader"}`))
	r.Header.Set("Origin", a.cfg.Origin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Forwarded-Host", "evil.example")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"rpId":"at.example"`) {
		t.Fatal("Host affected RP", w.Code, w.Body)
	}
	a.LoginLimit = rate.NewLimiter(0, 0)
	if w := nativeauthtest.PasskeyRequest(mux, "/at/auth/passkeys/login/begin", `{"username":"reader"}`, a.cfg.Origin); w.Code != 429 {
		t.Fatal("passkey rate limit bypassed")
	}
	for _, tt := range []struct {
		origin           string
		enabled, invalid bool
	}{
		{"http://localhost:3000", true, false}, {"https://localhost", true, false},
		{"http://127.0.0.1:3000", false, false}, {"https://192.0.2.1", false, false},
		{"https://com", false, true}, {"https://-invalid.example", false, true},
	} {
		cfg := nativeauthtest.Config()
		cfg.NativeAuth.Origin = tt.origin
		cfg.NativeAuth.InsecureHTTP = true
		got, err := New(cfg, p)
		if (err != nil) != tt.invalid {
			t.Errorf("%s: %v", tt.origin, err)
			continue
		}
		if err == nil && (got.passkey != nil) != tt.enabled {
			t.Errorf("%s capability", tt.origin)
		}
	}
}

type expiredChallengeStore struct{ service.AuthPasskeyStorer }

type deadlineAuthStore struct {
	service.AuthStorer
	service.AuthCredentialStorer
	service.AuthPasskeyStorer
	deadline time.Time
	delay    bool
	advanced bool
	issued   bool
}
