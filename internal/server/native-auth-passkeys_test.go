package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
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
	"github.com/rakunlabs/ada/middleware/auth/password"
	"golang.org/x/time/rate"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

// A software ES256 authenticator: private key exists only in test memory. Both
// packed self-attestation and assertions are signed, using no verifier mocks.
type softwarePasskey struct {
	key *ecdsa.PrivateKey
	id  []byte
}

func newSoftwarePasskey(t *testing.T) softwarePasskey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		t.Fatal(err)
	}
	return softwarePasskey{k, id}
}

func passkeyTestCBOR(v any) []byte {
	head := func(major byte, n int) []byte {
		if n < 24 {
			return []byte{major<<5 | byte(n)}
		}
		if n < 256 {
			return []byte{major<<5 | 24, byte(n)}
		}
		return []byte{major<<5 | 25, byte(n >> 8), byte(n)}
	}
	switch v := v.(type) {
	case string:
		return append(head(3, len(v)), []byte(v)...)
	case []byte:
		return append(head(2, len(v)), v...)
	case int:
		if v < 0 {
			return head(1, -1-v)
		}
		return head(0, v)
	case map[string]any:
		b := head(5, len(v))
		for k, v := range v {
			b = append(b, passkeyTestCBOR(k)...)
			b = append(b, passkeyTestCBOR(v)...)
		}
		return b
	default:
		panic("unsupported test CBOR")
	}
}

func (s softwarePasskey) response(t *testing.T, enroll bool, challenge, origin, rp, handle string, flags byte, count uint32, badSignature bool) string {
	t.Helper()
	b64 := base64.RawURLEncoding.EncodeToString
	kind := "webauthn.get"
	if enroll {
		kind = "webauthn.create"
	}
	client, _ := json.Marshal(map[string]any{"type": kind, "challenge": challenge, "origin": origin, "crossOrigin": false})
	rpHash := sha256.Sum256([]byte(rp))
	ad := append([]byte{}, rpHash[:]...)
	ad = append(ad, flags)
	ad = binary.BigEndian.AppendUint32(ad, count)
	if enroll {
		ad = append(ad, make([]byte, 16)...)
		ad = binary.BigEndian.AppendUint16(ad, uint16(len(s.id)))
		ad = append(ad, s.id...)
		// COSE EC2 / ES256 / P-256, fixed-size affine coordinates.
		ad = append(ad, 0xa5, 0x01, 0x02, 0x03, 0x26, 0x20, 0x01, 0x21, 0x58, 0x20)
		ad = append(ad, s.key.X.FillBytes(make([]byte, 32))...)
		ad = append(ad, 0x22, 0x58, 0x20)
		ad = append(ad, s.key.Y.FillBytes(make([]byte, 32))...)
	}
	clientHash := sha256.Sum256(client)
	signed := append(append([]byte{}, ad...), clientHash[:]...)
	digest := sha256.Sum256(signed)
	sig, err := ecdsa.SignASN1(rand.Reader, s.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	if badSignature {
		sig[len(sig)-1] ^= 0xff
	}
	response := map[string]any{"clientDataJSON": b64(client)}
	if enroll {
		response["attestationObject"] = b64(passkeyTestCBOR(map[string]any{"fmt": "packed", "authData": ad, "attStmt": map[string]any{"alg": -7, "sig": sig}}))
		response["transports"] = []string{"internal"}
	} else {
		response["authenticatorData"], response["signature"], response["userHandle"] = b64(ad), b64(sig), b64([]byte(handle))
	}
	blob, _ := json.Marshal(map[string]any{"id": b64(s.id), "rawId": b64(s.id), "type": "public-key", "response": response})
	return string(blob)
}

func passkeyRequest(h http.Handler, path, body, origin string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", origin)
	for _, c := range cookies {
		if c != nil {
			r.AddCookie(c)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func passkeyBegin(t *testing.T, h http.Handler, enroll bool, session *http.Cookie, remember bool) (string, *http.Cookie) {
	t.Helper()
	path, body := "/at/auth/passkeys/login/begin", fmt.Sprintf(`{"username":"reader","remember_me":%t}`, remember)
	if enroll {
		path, body = "/at/auth/passkeys/enroll/begin", `{"name":"Laptop","current_password":"`+strings.Repeat("x", 32)+`"}`
	}
	w := passkeyRequest(h, path, body, "https://at.example", session)
	if w.Code != 200 {
		t.Fatalf("begin: %d %s", w.Code, w.Body)
	}
	var result struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			RP        struct {
				ID string `json:"id"`
			} `json:"rp"`
			RPID string `json:"rpId"`
			User struct {
				ID string `json:"id"`
			} `json:"user"`
			Selection struct {
				UV       string `json:"userVerification"`
				Resident string `json:"residentKey"`
			} `json:"authenticatorSelection"`
			UV string `json:"userVerification"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if enroll && (result.PublicKey.RP.ID != "at.example" || result.PublicKey.Selection.UV != "required" || result.PublicKey.Selection.Resident != "preferred") {
		t.Fatal("unsafe enrollment options")
	}
	if !enroll && (result.PublicKey.RPID != "at.example" || result.PublicKey.UV != "required") {
		t.Fatal("unsafe login options")
	}
	c := w.Result().Cookies()[0]
	if !c.HttpOnly || !c.Secure || c.Path != "/at/auth/passkeys/" || c.SameSite != http.SameSiteStrictMode || c.MaxAge != 300 || c.Domain != "" {
		t.Fatalf("ceremony cookie: %+v", c)
	}
	return result.PublicKey.Challenge, c
}

func TestNativePasskeySignedPostgres(t *testing.T) {
	p := postgrestest.New(t, nil)
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "reader", PasswordHash: password.Dummy}, false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	a.loginLimit = rate.NewLimiter(rate.Inf, 10000)
	mux := ada.New()
	a.register(mux, "/at")
	session := nativeLoginCookie(t, mux, "reader")
	key := newSoftwarePasskey(t)
	challenge, ceremony := passkeyBegin(t, mux, true, session, false)
	response := key.response(t, true, challenge, a.cfg.Origin, "at.example", u.ID, 0x45, 0, false)
	w := passkeyRequest(mux, "/at/auth/passkeys/enroll/finish", response, a.cfg.Origin, ceremony, session)
	if w.Code != 204 {
		t.Fatalf("enrollment: %d %s", w.Code, w.Body)
	}
	if w := passkeyRequest(mux, "/at/auth/passkeys/enroll/finish", response, a.cfg.Origin, ceremony, session); w.Code != 401 {
		t.Fatal("replayed enrollment accepted")
	}
	keys, err := p.ListAuthPasskeys(t.Context(), u.ID)
	if err != nil || len(keys) != 1 || string(keys[0].Credential.UserHandle) != u.ID {
		t.Fatalf("persisted keys: %+v %v", keys, err)
	}
	list := nativeRequest(mux, "GET", "/at/auth/passkeys", "", "", session)
	if list.Code != 200 || strings.Contains(list.Body.String(), "PublicKey") || strings.Contains(list.Body.String(), "Credential") || !strings.Contains(list.Body.String(), `"last_used_at":null`) {
		t.Fatalf("key list: %d %s", list.Code, list.Body)
	}
	t.Run("anonymous begins cannot spend victim quota", func(t *testing.T) {
		for range 6 {
			passkeyBegin(t, mux, false, nil, false)
		}
		challenge, ceremony := passkeyBegin(t, mux, false, nil, false)
		body := key.response(t, false, challenge, a.cfg.Origin, "at.example", u.ID, 5, 0, false)
		if w := passkeyRequest(mux, "/at/auth/passkeys/login/finish", body, a.cfg.Origin, ceremony); w.Code != 200 {
			t.Fatalf("legitimate login starved: %d %s", w.Code, w.Body)
		}
		_, enrollment := passkeyBegin(t, mux, true, session, false)
		if c, err := p.ConsumeAuthChallenge(t.Context(), nativeSessionHash(enrollment.Value)); err != nil || c == nil || c.Purpose != "enroll" {
			t.Fatalf("authenticated enrollment starved: %+v %v", c, err)
		}
	})
	for _, remember := range []bool{false, true} {
		t.Run(fmt.Sprintf("valid remember=%t", remember), func(t *testing.T) {
			challenge, ceremony := passkeyBegin(t, mux, false, nil, remember)
			body := key.response(t, false, challenge, a.cfg.Origin, "at.example", u.ID, 5, 0, false)
			w := passkeyRequest(mux, "/at/auth/passkeys/login/finish", body, a.cfg.Origin, ceremony)
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
			if w := passkeyRequest(mux, "/at/auth/passkeys/login/finish", body, a.cfg.Origin, ceremony); w.Code != 401 {
				t.Fatal("replayed login accepted")
			}
		})
	}
	for _, test := range []string{"wrong browser", "wrong challenge", "wrong user", "wrong origin", "wrong RP", "missing UV", "bad signature", "expired", "oversized", "HTTP origin", "wrong purpose", "malformed"} {
		t.Run(test, func(t *testing.T) {
			challenge, ceremony := passkeyBegin(t, mux, false, nil, false)
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
			body := key.response(t, false, challenge, origin, rp, handle, flags, 0, bad)
			if test == "oversized" {
				body = strings.Repeat("x", 65537)
			}
			if test == "malformed" {
				body = "{"
			}
			w := passkeyRequest(mux, path, body, header, usedCookie)
			if w.Code < 400 {
				t.Fatalf("invalid accepted: %d %s", w.Code, w.Body)
			}
			if test != "wrong browser" {
				if c, err := p.ConsumeAuthChallenge(t.Context(), nativeSessionHash(ceremony.Value)); err != nil || c != nil {
					t.Fatalf("invalid attempt retained challenge: %+v %v", c, err)
				}
			} else {
				_, _ = p.ConsumeAuthChallenge(t.Context(), nativeSessionHash(ceremony.Value))
			}
		})
	}
	for _, test := range []string{"wrong session", "logged out", "version changed", "missing UV", "bad signature", "wrong origin", "wrong RP", "duplicate"} {
		t.Run("enroll "+test, func(t *testing.T) {
			session := nativeLoginCookie(t, mux, "reader")
			challenge, ceremony := passkeyBegin(t, mux, true, session, false)
			origin, rp, flags, bad := a.cfg.Origin, "at.example", byte(0x45), false
			candidate := newSoftwarePasskey(t)
			switch test {
			case "wrong session":
				session = nativeLoginCookie(t, mux, "reader")
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
			body := candidate.response(t, true, challenge, origin, rp, u.ID, flags, 0, bad)
			w := passkeyRequest(mux, "/at/auth/passkeys/enroll/finish", body, a.cfg.Origin, ceremony, session)
			if w.Code < 400 {
				t.Fatalf("invalid enrollment accepted: %d", w.Code)
			}
			if c, err := p.ConsumeAuthChallenge(t.Context(), nativeSessionHash(ceremony.Value)); err != nil || c != nil {
				t.Fatal("enrollment challenge retained")
			}
		})
	}
	t.Run("concurrent signed counter", func(t *testing.T) {
		challenge1, cookie1 := passkeyBegin(t, mux, false, nil, false)
		challenge2, cookie2 := passkeyBegin(t, mux, false, nil, false)
		body1 := key.response(t, false, challenge1, a.cfg.Origin, "at.example", u.ID, 5, 1, false)
		body2 := key.response(t, false, challenge2, a.cfg.Origin, "at.example", u.ID, 5, 1, false)
		var winners atomic.Int32
		var wg sync.WaitGroup
		for _, req := range []struct {
			body   string
			cookie *http.Cookie
		}{{body1, cookie1}, {body2, cookie2}} {
			wg.Go(func() {
				w := passkeyRequest(mux, "/at/auth/passkeys/login/finish", req.body, a.cfg.Origin, req.cookie)
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
	session = nativeLoginCookie(t, mux, "reader")
	challenge, ceremony = passkeyBegin(t, mux, false, nil, false)
	body := key.response(t, false, challenge, a.cfg.Origin, "at.example", u.ID, 5, 2, false)
	w = passkeyRequest(mux, "/at/auth/passkeys/"+keys[0].ID+"/delete", `{"current_password":"`+strings.Repeat("x", 32)+`"}`, a.cfg.Origin, session)
	if w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w := passkeyRequest(mux, "/at/auth/passkeys/login/finish", body, a.cfg.Origin, ceremony); w.Code != 401 {
		t.Fatal("deleted key logged in")
	}
	if w := nativeRequest(mux, "GET", "/at/auth/me", "", "", session); w.Code != 401 {
		t.Fatal("delete retained session")
	}
	// Password fallback and enrollment after removing the last key still work.
	session = nativeLoginCookie(t, mux, "reader")
	challenge, ceremony = passkeyBegin(t, mux, true, session, false)
	w = passkeyRequest(mux, "/at/auth/passkeys/enroll/finish", key.response(t, true, challenge, a.cfg.Origin, "at.example", u.ID, 0x45, 0, false), a.cfg.Origin, ceremony, session)
	if w.Code != 204 {
		t.Fatalf("re-enrollment: %d %s", w.Code, w.Body)
	}
}

type expiredChallengeStore struct{ service.AuthPasskeyStorer }

// Observe the server-held deadline at both admissions; optionally delay the
// second admission until it expires without cancelling the request context.
type deadlineAuthStore struct {
	service.AuthStorer
	service.AuthCredentialStorer
	service.AuthPasskeyStorer
	deadline time.Time
	delay    bool
	advanced bool
	issued   bool
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
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "reader", PasswordHash: password.Dummy}, false)
	if err != nil {
		t.Fatal(err)
	}
	store := &deadlineAuthStore{AuthStorer: p, AuthCredentialStorer: p, AuthPasskeyStorer: p}
	a, err := newNativeAuth(nativeTestConfig(), store)
	if err != nil {
		t.Fatal(err)
	}
	a.loginLimit = rate.NewLimiter(rate.Inf, 100)
	mux := ada.New()
	a.register(mux, "/at")
	session := nativeLoginCookie(t, mux, "reader")
	key := newSoftwarePasskey(t)
	challenge, ceremony := passkeyBegin(t, mux, true, session, false)
	body := key.response(t, true, challenge, a.cfg.Origin, "at.example", u.ID, 0x45, 0, false)
	if w := passkeyRequest(mux, "/at/auth/passkeys/enroll/finish", body, a.cfg.Origin, ceremony, session); w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
	for _, delay := range []bool{false, true} {
		store.delay, store.advanced, store.issued = delay, false, false
		challenge, ceremony = passkeyBegin(t, mux, false, nil, true)
		body = key.response(t, false, challenge, a.cfg.Origin, "at.example", u.ID, 5, 0, false)
		w := passkeyRequest(mux, "/at/auth/passkeys/login/finish", body, a.cfg.Origin, ceremony)
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

func checkNativeLifetime(t *testing.T, a *nativeAuth, c *http.Cookie, remember bool) {
	t.Helper()
	if c == nil {
		t.Fatal("missing session cookie")
	}
	u, s, err := a.credentials.ResolveAuthAccess(t.Context(), nativeSessionHash(c.Value))
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
		w := nativeRequest(mux, "POST", "/at/auth/login", `{"username":"reader","password":"`+strings.Repeat("x", 32)+`"`+body+`}`, a.cfg.Origin, nil)
		if w.Code != 200 {
			t.Fatalf("login: %d %s", w.Code, w.Body)
		}
		checkNativeLifetime(t, a, w.Result().Cookies()[0], strings.Contains(body, "true"))
	}
	if w := nativeRequest(mux, "POST", "/at/auth/login", `{"username":"reader","password":"x","remember_me":"true"}`, a.cfg.Origin, nil); w.Code != 400 {
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
	_, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "reader", PasswordHash: password.Dummy}, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.CreateAuthUser(t.Context(), service.AuthUser{Username: "admin", PasswordHash: password.Dummy, Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	a.loginLimit = rate.NewLimiter(rate.Inf, 1000)
	mux := ada.New()
	a.register(mux, "/at")
	session := nativeLoginCookie(t, mux, "reader")
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
			w := passkeyRequest(mux, "/at/auth/passkeys"+tt.path, tt.body, tt.origin, tt.session)
			if w.Code != tt.code {
				t.Fatalf("%d want %d: %s", w.Code, tt.code, w.Body)
			}
		})
	}
	challenge, ceremony := passkeyBegin(t, mux, true, session, false)
	k := newSoftwarePasskey(t)
	w := passkeyRequest(mux, "/at/auth/passkeys/enroll/finish", k.response(t, true, challenge, a.cfg.Origin, "at.example", "", 0x45, 0, false), a.cfg.Origin, ceremony, session)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
	reader, _ := p.GetAuthUser(t.Context(), "reader")
	keys, _ := p.ListAuthPasskeys(t.Context(), reader.ID)
	admin := nativeLoginCookie(t, mux, "admin")
	w = nativeRequest(mux, "GET", "/at/auth/passkeys", "", "", admin)
	if w.Code != 200 || strings.Contains(w.Body.String(), keys[0].ID) {
		t.Fatal("admin can list other's key", w.Code, w.Body)
	}
	w = passkeyRequest(mux, "/at/auth/passkeys/"+keys[0].ID+"/delete", `{"current_password":"`+strings.Repeat("x", 32)+`"}`, a.cfg.Origin, admin)
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
	a.loginLimit = rate.NewLimiter(0, 0)
	if w := passkeyRequest(mux, "/at/auth/passkeys/login/begin", `{"username":"reader"}`, a.cfg.Origin); w.Code != 429 {
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
		cfg := nativeTestConfig()
		cfg.NativeAuth.Origin = tt.origin
		cfg.NativeAuth.InsecureHTTP = true
		got, err := newNativeAuth(cfg, p)
		if (err != nil) != tt.invalid {
			t.Errorf("%s: %v", tt.origin, err)
			continue
		}
		if err == nil && (got.passkey != nil) != tt.enabled {
			t.Errorf("%s capability", tt.origin)
		}
	}
}

func TestNativePasskeyProductionBasePath(t *testing.T) {
	p := postgrestest.New(t, nil)
	_, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "reader", PasswordHash: password.Dummy}, false)
	if err != nil {
		t.Fatal(err)
	}
	cfg := nativeTestConfig()
	cfg.Workspace = &config.Workspace{Root: t.TempDir(), TTLHours: -1}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s, err := New(ctx, cfg, nil, p, "postgres", nil, nil, "test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	w := nativeRequest(s.server, "GET", "/at/auth/status", "", "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"passkeys":true`) || !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
	session := nativeLoginCookie(t, s.server, "reader")
	challenge, ceremony := passkeyBegin(t, s.server, true, session, false)
	k := newSoftwarePasskey(t)
	w = passkeyRequest(s.server, "/at/auth/passkeys/enroll/finish", k.response(t, true, challenge, cfg.NativeAuth.Origin, "at.example", "", 0x45, 0, false), cfg.NativeAuth.Origin, ceremony, session)
	if w.Code != 204 {
		t.Fatalf("production enrollment: %d %s", w.Code, w.Body)
	}
	challenge, ceremony = passkeyBegin(t, s.server, false, nil, true)
	w = passkeyRequest(s.server, "/at/auth/passkeys/login/finish", k.response(t, false, challenge, cfg.NativeAuth.Origin, "at.example", "", 5, 1, false), cfg.NativeAuth.Origin, ceremony)
	if w.Code != 200 {
		t.Fatalf("production assertion: %d %s", w.Code, w.Body)
	}
	if w := nativeRequest(s.server, "GET", "/auth/passkeys", "", "", session); w.Code == 200 {
		t.Fatal("unprefixed endpoint exposed")
	}
}
