package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/password"
	"github.com/rakunlabs/ada/middleware/auth/strategy/totp"
	"golang.org/x/time/rate"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func TestSecurityTOTPInteroperabilityAndFence(t *testing.T) {
	secret := totp.SecretFromBytes([]byte("12345678901234567890"))
	s := service.AuthSecurityState{Secret: secret.Base32(), LastStep: -1}
	// RFC6238 SHA1 at 59 seconds is 94287082, truncated to six digits.
	if !verifySecurityCode(&s, "287082", time.Unix(59, 0)) {
		t.Fatal("RFC vector rejected")
	}
	if verifySecurityCode(&s, "287082", time.Unix(59, 0)) {
		t.Fatal("step replay accepted")
	}
	for _, delta := range []int64{-1, 0, 1, 2} {
		now := time.Unix(3000, 0)
		code, _ := totp.Default().Generate(secret, now.Add(time.Duration(delta)*30*time.Second))
		s.LastStep = -1
		if got := verifySecurityCode(&s, code, now); got != (delta <= 1) {
			t.Fatalf("skew %d: %v", delta, got)
		}
	}
	codes, hashes, err := newBackupCodes()
	if err != nil {
		t.Fatal(err)
	}
	s.BackupHashes = hashes
	if len(codes) != 10 || len(codes[0]) != 32 || hashes[0] == codes[0] {
		t.Fatal("backup entropy or hashing")
	}
	if !verifySecurityCode(&s, codes[0], time.Now()) || verifySecurityCode(&s, codes[0], time.Now()) {
		t.Fatal("backup one use")
	}
	if verifySecurityCode(&service.AuthSecurityState{}, codes[1], time.Now()) {
		t.Fatal("backup without factor")
	}
}

func TestSecurityPasskeyRecentAuthPostgres(t *testing.T) {
	p := postgrestest.New(t, []byte(strings.Repeat("k", 32)))
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "reader", PasswordHash: password.Dummy}, false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	a.loginLimit = rate.NewLimiter(rate.Inf, 100)
	mux := ada.New()
	a.register(mux, "/at")
	session := nativeLoginCookie(t, mux, "reader")
	key := newSoftwarePasskey(t)
	challenge, ceremony := passkeyBegin(t, mux, true, session, false)
	w := passkeyRequest(mux, "/at/auth/passkeys/enroll/finish", key.response(t, true, challenge, a.cfg.Origin, "at.example", u.ID, 0x45, 0, false), a.cfg.Origin, ceremony, session)
	if w.Code != 204 {
		t.Fatalf("enroll: %d %s", w.Code, w.Body)
	}
	if _, err := p.SetAuthUserPassword(t.Context(), u.ID, "unusable-local-password", &u.SessionVersion); err != nil {
		t.Fatal(err)
	}
	challenge, ceremony = passkeyBegin(t, mux, false, nil, false)
	w = passkeyRequest(mux, "/at/auth/passkeys/login/finish", key.response(t, false, challenge, a.cfg.Origin, "at.example", u.ID, 0x05, 1, false), a.cfg.Origin, ceremony)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	var binding *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == a.session.CookieName {
			session = c
		}
		if c.Name == a.securityCookie("", 0).Name {
			binding = c
		}
	}
	w = passkeyRequest(mux, "/at/auth/reauth/begin", `{"purpose":"password.change","method":"passkey"}`, a.cfg.Origin, session, binding)
	if w.Code != 200 {
		t.Fatalf("reauth begin: %d %s", w.Code, w.Body)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == a.securityCookie("", 0).Name {
			binding = c
		}
	}
	var begin struct {
		Challenge string `json:"challenge"`
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &begin); err != nil {
		t.Fatal(err)
	}
	assertion := key.response(t, false, begin.PublicKey.Challenge, a.cfg.Origin, "at.example", u.ID, 0x05, 2, false)
	body := `{"challenge":"` + begin.Challenge + `","credential":` + assertion + `}`
	w = passkeyRequest(mux, "/at/auth/reauth/finish", body, a.cfg.Origin, session, binding)
	if w.Code != 200 {
		t.Fatalf("reauth finish: %d %s", w.Code, w.Body)
	}
	var proof struct {
		Proof string `json:"proof"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &proof); err != nil {
		t.Fatal(err)
	}
	w = passkeyRequest(mux, "/at/auth/reauth/finish", body, a.cfg.Origin, session, binding)
	if w.Code != 401 {
		t.Fatal("assertion replay")
	}
	w = passkeyRequest(mux, "/at/auth/password", `{"proof":"`+proof.Proof+`","new_password":"a replacement password"}`, a.cfg.Origin, session, binding)
	if w.Code != 204 {
		t.Fatalf("external-only password replacement: %d %s", w.Code, w.Body)
	}
	u, err = p.GetAuthUserByID(t.Context(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.UpdateAuthSecurity(t.Context(), u.ID, "", u.SessionVersion, func(s *service.AuthSecurityUpdate) error {
		s.State.Secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
		s.Revoke = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	challenge, ceremony = passkeyBegin(t, mux, false, nil, false)
	w = passkeyRequest(mux, "/at/auth/passkeys/login/finish", key.response(t, false, challenge, a.cfg.Origin, "at.example", u.ID, 0x05, 3, false), a.cfg.Origin, ceremony)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"mfa_required":true`) {
		t.Fatalf("passkey bypassed MFA: %d %s", w.Code, w.Body)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == a.session.CookieName && c.MaxAge >= 0 {
			t.Fatal("passkey issued pre-MFA session")
		}
	}
}

func TestSecurityHTTPPostgres(t *testing.T) {
	var audit bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&audit, nil)))
	defer slog.SetDefault(oldLogger)
	p := postgrestest.New(t, []byte(strings.Repeat("k", 32)))
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	a.loginLimit = rate.NewLimiter(rate.Inf, 100)
	mux := ada.New()
	a.register(mux, "/at")
	password := strings.Repeat("p", 20)
	hash, err := a.password.Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "security-user", PasswordHash: hash}, false)
	if err != nil {
		t.Fatal(err)
	}
	cookies := map[string]*http.Cookie{}
	call := func(path string, body any, want int) map[string]json.RawMessage {
		t.Helper()
		b, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/at/auth/"+path, strings.NewReader(string(b)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", a.cfg.Origin)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d want %d: %s", path, w.Code, want, w.Body)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s missing no-store", path)
		}
		for _, c := range w.Result().Cookies() {
			if c.MaxAge < 0 {
				delete(cookies, c.Name)
			} else {
				cookies[c.Name] = c
			}
		}
		var out map[string]json.RawMessage
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	text := func(m map[string]json.RawMessage, key string) string {
		var s string
		_ = json.Unmarshal(m[key], &s)
		return s
	}
	login := func() map[string]json.RawMessage {
		return call("login", map[string]any{"username": u.Username, "password": password, "remember_me": true}, 200)
	}
	proof := func(purpose string) string {
		b := call("reauth/begin", map[string]string{"purpose": purpose, "method": "password"}, 200)
		v := call("reauth/finish", map[string]string{"challenge": text(b, "challenge"), "password": password}, 200)
		return text(v, "proof")
	}
	login()
	call("users/"+u.ID+"/recovery", map[string]string{"proof": ""}, 403)
	oldAccess := *cookies[a.session.CookieName]
	wrong := proof("identity.link")
	call("totp/enroll", map[string]string{"proof": wrong}, 401)
	pr := proof("totp.enroll")
	enroll := call("totp/enroll", map[string]string{"proof": pr}, 200)
	call("totp/enroll", map[string]string{"proof": pr}, 401)
	oldEnrollment := enroll
	enroll = call("totp/enroll", map[string]string{"proof": proof("totp.enroll")}, 200)
	oldSecret, _ := totp.SecretFromBase32(text(oldEnrollment, "secret"))
	oldCode, _ := totp.Default().Generate(oldSecret, time.Now())
	call("totp/confirm", map[string]string{"enrollment": text(oldEnrollment, "enrollment"), "code": oldCode}, 401)
	if err := p.UpdateAuthSecurity(t.Context(), u.ID, "", u.SessionVersion, func(s *service.AuthSecurityUpdate) error {
		for i := range s.State.Transactions {
			if s.State.Transactions[i].Purpose == "totp.enroll" {
				s.State.Transactions[i].Expires = s.Now
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	call("totp/confirm", map[string]string{"enrollment": text(enroll, "enrollment"), "code": "000000"}, 401)
	enroll = call("totp/enroll", map[string]string{"proof": proof("totp.enroll")}, 200)
	secret, err := totp.SecretFromBase32(text(enroll, "secret"))
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.Default().Generate(secret, time.Now())
	activated := call("totp/confirm", map[string]string{"enrollment": text(enroll, "enrollment"), "code": code}, 200)
	var backups []string
	_ = json.Unmarshal(activated["backup_codes"], &backups)
	if len(backups) != 10 {
		t.Fatal("missing backups")
	}
	call("mfa/verify", map[string]string{"code": backups[0]}, 401)
	if _, err := a.Resolve(t.Context(), oldAccess.Value); err == nil {
		t.Fatal("pre-enrollment access live")
	}
	pending := login()
	challenge := text(pending, "challenge")
	if string(pending["mfa_required"]) != "true" || cookies[a.session.CookieName] != nil {
		t.Fatal("MFA issued ordinary credentials")
	}
	call("refresh", map[string]string{}, 401)
	call("mobile/approve", map[string]string{"request_id": challenge}, 401)
	call("mobile/token", map[string]string{"code": challenge}, 400)
	call("mfa/verify", map[string]string{"challenge": challenge, "code": code}, 401) // activation already consumed timestep
	call("mfa/verify", map[string]string{"challenge": challenge, "code": backups[0]}, 200)
	call("mfa/verify", map[string]string{"challenge": challenge, "code": backups[1]}, 401)
	oldMFA := *cookies[a.session.CookieName]
	// Regeneration requires a purpose-bound proof and a fresh second factor.
	regen := call("totp/backup-codes/regenerate", map[string]string{"proof": proof("totp.backup-codes"), "code": backups[1]}, 200)
	var fresh []string
	_ = json.Unmarshal(regen["backup_codes"], &fresh)
	if _, err := a.Resolve(t.Context(), oldMFA.Value); err == nil {
		t.Fatal("regeneration retained session")
	}
	pending = login()
	challenge = text(pending, "challenge")
	call("mfa/verify", map[string]string{"challenge": challenge, "code": backups[2]}, 401)
	call("mfa/verify", map[string]string{"challenge": challenge, "code": fresh[0]}, 200)
	// A pending challenge cannot be completed from a different browser.
	pending = login()
	challenge = text(pending, "challenge")
	binding := cookies[a.securityCookie("", 0).Name]
	delete(cookies, a.securityCookie("", 0).Name)
	call("mfa/verify", map[string]string{"challenge": challenge, "code": fresh[1]}, 401)
	cookies[binding.Name] = binding
	// Two server replicas race one challenge and backup: exactly one family.
	other, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	var won atomic.Int32
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			r := httptest.NewRequest("POST", "/at/auth/mfa/verify", strings.NewReader(`{"challenge":"`+challenge+`","code":"`+fresh[1]+`"}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", a.cfg.Origin)
			r.AddCookie(binding)
			w := httptest.NewRecorder()
			if i%2 == 0 {
				a.mfaVerify(w, r)
			} else {
				other.mfaVerify(w, r)
			}
			if w.Code == 200 {
				won.Add(1)
			} else if w.Code != 401 {
				t.Errorf("race: %d %s", w.Code, w.Body)
			}
		})
	}
	wg.Wait()
	if won.Load() != 1 {
		t.Fatalf("MFA winners %d", won.Load())
	}
	ticket, err := IssueAuthRecoveryTicket(t.Context(), p, u.ID, "operator")
	if err != nil {
		t.Fatal(err)
	}
	call("recovery/inspect", map[string]string{"ticket": ticket}, 200)
	call("recovery/redeem", map[string]string{"ticket": ticket, "password": strings.Repeat("n", 20)}, 204)
	call("recovery/redeem", map[string]string{"ticket": ticket, "password": password}, 401)
	call("login", map[string]string{"username": u.Username, "password": password}, 401)
	password = strings.Repeat("n", 20)
	out := login()
	if out["mfa_required"] != nil {
		t.Fatal("recovery retained factor")
	}
	live, err := p.GetAuthUserByID(t.Context(), u.ID)
	if err != nil || live.ID != u.ID || live.Admin || live.Disabled {
		t.Fatalf("recovery changed identity: %+v %v", live, err)
	}
	staleTicket, err := IssueAuthRecoveryTicket(t.Context(), p, u.ID, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.InvalidateAuthUser(t.Context(), u.ID, false); err != nil {
		t.Fatal(err)
	}
	call("recovery/inspect", map[string]string{"ticket": staleTicket}, 401)
	expiredTicket, err := IssueAuthRecoveryTicket(t.Context(), p, u.ID, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.UpdateAuthSecurity(t.Context(), u.ID, "", -1, func(s *service.AuthSecurityUpdate) error {
		for i := range s.State.Transactions {
			if s.State.Transactions[i].Purpose == "recovery" {
				s.State.Transactions[i].Expires = s.Now
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	call("recovery/inspect", map[string]string{"ticket": expiredTicket}, 401)
	for _, secret := range []string{password, secret.Base32(), backups[0], fresh[0], ticket} {
		if strings.Contains(audit.String(), secret) {
			t.Fatal("credential entered security logs")
		}
	}
	for _, event := range []string{"recovery.issue", "recovery.redeem", `"outcome":"rejected"`, `"outcome":"success"`} {
		if !strings.Contains(audit.String(), event) {
			t.Fatalf("missing audit event %s", event)
		}
	}
}

func TestSecurityMFATimestepAndAttemptsPostgres(t *testing.T) {
	p := postgrestest.New(t, []byte(strings.Repeat("k", 32)))
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "mfa-races", PasswordHash: "verified-by-primary"}, false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := totp.NewSecret(nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.UpdateAuthSecurity(t.Context(), u.ID, "", u.SessionVersion, func(s *service.AuthSecurityUpdate) error {
		s.State.Secret = secret.Base32()
		s.State.LastStep = -1
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	type pending struct {
		challenge string
		cookie    *http.Cookie
	}
	begin := func(a *nativeAuth) pending {
		t.Helper()
		r := httptest.NewRequest("POST", "/at/auth/login", nil)
		w := httptest.NewRecorder()
		a.finishPrimaryLogin(w, r, u, false, "password")
		if w.Code != 200 {
			t.Fatalf("begin %d %s", w.Code, w.Body)
		}
		var result struct {
			Challenge string `json:"challenge"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		var c *http.Cookie
		for _, v := range w.Result().Cookies() {
			if v.Name == a.securityCookie("", 0).Name && v.MaxAge > 0 {
				c = v
			}
		}
		return pending{result.Challenge, c}
	}
	verify := func(a *nativeAuth, pending pending, code string) int {
		r := httptest.NewRequest("POST", "/at/auth/mfa/verify", strings.NewReader(`{"challenge":"`+pending.challenge+`","code":"`+code+`"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", a.cfg.Origin)
		r.AddCookie(pending.cookie)
		w := httptest.NewRecorder()
		a.mfaVerify(w, r)
		return w.Code
	}
	first, second := begin(a), begin(b)
	code, _ := totp.Default().Generate(secret, time.Now())
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i, c := range []pending{first, second} {
		wg.Go(func() {
			instance := a
			if i == 1 {
				instance = b
			}
			if got := verify(instance, c, code); got == 200 {
				winners.Add(1)
			} else if got != 401 {
				t.Errorf("step race status %d", got)
			}
		})
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("step winners %d", winners.Load())
	}
	exhausted := begin(a)
	for range 5 {
		if got := verify(a, exhausted, "invalid"); got != 401 {
			t.Fatalf("invalid code %d", got)
		}
	}
	// Even a correct future-skew step cannot resurrect an exhausted challenge.
	code, _ = totp.Default().Generate(secret, time.Now().Add(30*time.Second))
	if got := verify(b, exhausted, code); got != 401 {
		t.Fatalf("exhausted challenge %d", got)
	}
	stale := begin(a)
	if _, err := p.InvalidateAuthUser(t.Context(), u.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := verify(b, stale, code); got != 401 {
		t.Fatalf("disabled account %d", got)
	}
}

func TestSecurityFactorRemovalPostgres(t *testing.T) {
	p := postgrestest.New(t, []byte(strings.Repeat("k", 32)))
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "reader", PasswordHash: password.Dummy}, false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	a.loginLimit = rate.NewLimiter(rate.Inf, 100)
	mux := ada.New()
	a.register(mux, "/at")
	session := nativeLoginCookie(t, mux, "reader")
	codes, hashes, err := newBackupCodes()
	if err != nil {
		t.Fatal(err)
	}
	// Fixture represents an already-MFA-complete family and active factor.
	if err := p.UpdateAuthSecurity(t.Context(), u.ID, "", u.SessionVersion, func(s *service.AuthSecurityUpdate) error {
		s.State.Secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
		s.State.BackupHashes = hashes
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	getProof := func() string {
		t.Helper()
		w := passkeyRequest(mux, "/at/auth/reauth/begin", `{"purpose":"totp.remove","method":"password"}`, a.cfg.Origin, session)
		if w.Code != 200 {
			t.Fatalf("begin %d %s", w.Code, w.Body)
		}
		var result struct {
			Challenge string `json:"challenge"`
			Proof     string `json:"proof"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		var binding *http.Cookie
		for _, c := range w.Result().Cookies() {
			if c.Name == a.securityCookie("", 0).Name {
				binding = c
			}
		}
		w = passkeyRequest(mux, "/at/auth/reauth/finish", `{"challenge":"`+result.Challenge+`","password":"`+strings.Repeat("x", 32)+`"}`, a.cfg.Origin, session, binding)
		if w.Code != 200 {
			t.Fatalf("finish %d %s", w.Code, w.Body)
		}
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		return result.Proof
	}
	proof := getProof()
	w := passkeyRequest(mux, "/at/auth/totp/remove", `{"proof":"`+proof+`","code":"invalid"}`, a.cfg.Origin, session)
	if w.Code != 401 {
		t.Fatal("removed without second factor")
	}
	w = passkeyRequest(mux, "/at/auth/totp/remove", `{"proof":"`+proof+`","code":"`+codes[0]+`"}`, a.cfg.Origin, session)
	if w.Code != 401 {
		t.Fatal("reused consumed recent proof")
	}
	proof = getProof()
	w = passkeyRequest(mux, "/at/auth/totp/remove", `{"proof":"`+proof+`","code":"`+codes[0]+`"}`, a.cfg.Origin, session)
	if w.Code != 204 {
		t.Fatalf("remove %d %s", w.Code, w.Body)
	}
	if _, err := a.Resolve(t.Context(), session.Value); err == nil {
		t.Fatal("removal retained old session")
	}
	if err := p.UpdateAuthSecurity(t.Context(), u.ID, "", -1, func(s *service.AuthSecurityUpdate) error {
		if s.State.Secret != "" || len(s.State.BackupHashes) != 0 || len(s.State.Transactions) != 0 {
			t.Fatal("removal retained factors/proofs")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	nativeLoginCookie(t, mux, "reader") // ordinary login becomes usable again
}

func TestSecurityAccountAdmissionPostgres(t *testing.T) {
	p := postgrestest.New(t, nil)
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "account-limit", PasswordHash: "hash"}, false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := range 80 {
		wg.Go(func() {
			instance := a
			if i%2 == 1 {
				instance = b
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/at/auth/login", nil)
			if instance.admitSecurityAccount(w, r, u.ID) {
				wins.Add(1)
			} else if w.Code != 429 {
				t.Errorf("account admission status %d", w.Code)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 60 {
		t.Fatalf("account admission winners %d", wins.Load())
	}
}

func TestSecurityRecentProofScopePostgres(t *testing.T) {
	p := postgrestest.New(t, nil)
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "proof-owner", PasswordHash: "hash"}, false)
	if err != nil {
		t.Fatal(err)
	}
	v, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "other-owner", PasswordHash: "hash"}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []*service.AuthUser{u, v} {
		if err := p.CreateAuthSession(t.Context(), service.AuthSession{Hash: user.ID + "-session", UserID: user.ID, Version: user.SessionVersion, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	raw, hash, err := securityToken(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.UpdateAuthSecurity(t.Context(), u.ID, u.ID+"-session", u.SessionVersion, func(s *service.AuthSecurityUpdate) error {
		s.State.Transactions = []service.AuthTransaction{{Hash: hash, Purpose: "proof:identity.link", SessionID: u.ID + "-session", Version: u.SessionVersion, Expires: s.Now.Add(time.Minute)}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, user, sid, purpose string
		version                  int64
	}{
		{"wrong user", v.ID, v.ID + "-session", "identity.link", v.SessionVersion},
		{"wrong session", u.ID, v.ID + "-session", "identity.link", u.SessionVersion},
		{"wrong purpose", u.ID, u.ID + "-session", "totp.remove", u.SessionVersion},
		{"wrong version", u.ID, u.ID + "-session", "identity.link", u.SessionVersion + 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := a.consumeRecentAuthProof(t.Context(), tt.user, tt.sid, tt.version, tt.purpose, raw); err == nil {
				t.Fatal("proof escaped binding")
			}
		})
	}
	if err := a.consumeRecentAuthProof(t.Context(), u.ID, u.ID+"-session", u.SessionVersion, "identity.link", raw); err != nil {
		t.Fatal(err)
	}
	if err := a.consumeRecentAuthProof(t.Context(), u.ID, u.ID+"-session", u.SessionVersion, "identity.link", raw); err == nil {
		t.Fatal("proof replay")
	}
}
