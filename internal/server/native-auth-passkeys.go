package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/cookie"
	"github.com/rakunlabs/ada/middleware/auth/identity"
	"github.com/rakunlabs/ada/middleware/auth/strategy/passkey"
	"golang.org/x/net/publicsuffix"

	"github.com/rakunlabs/at/internal/service"
)

func (a *nativeAuth) initPasskeys(host string) error {
	a.keyStore, _ = a.store.(service.AuthPasskeyStorer)
	// IP origins remain usable for password auth, but are not WebAuthn RPs.
	if a.keyStore == nil || net.ParseIP(host) != nil {
		return nil
	}
	suffix, _ := publicsuffix.PublicSuffix(host)
	if host != "localhost" && (host == suffix || host != strings.ToLower(host) || len(host) > 253) {
		return fmt.Errorf("native_auth.origin must have a canonical DNS hostname for passkeys")
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("invalid native_auth.origin DNS hostname")
		}
		for _, ch := range label {
			if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
				return fmt.Errorf("invalid native_auth.origin DNS hostname")
			}
		}
	}
	var err error
	a.passkey, err = passkey.New(&passkey.Config{RPID: host, RPDisplayName: "AT", RPOrigins: []string{a.cfg.Origin}, UserVerification: passkey.UVRequired, ChallengeTTL: 5 * time.Minute})
	if err != nil {
		return fmt.Errorf("initialize native passkeys: %w", err)
	}
	return nil
}

func (a *nativeAuth) registerPasskeys(mux *ada.Server, base string) {
	public := mux.Group(base + "/auth")
	if a.passkey == nil {
		return
	}
	public.POST("/passkeys/login/begin", a.passkeyLoginBegin)
	// Finish consumes before checking origin/body/session, even on invalid attempts.
	public.POST("/passkeys/login/finish", a.passkeyFinish("login"))
	public.POST("/passkeys/enroll/finish", a.passkeyFinish("enroll"))
	self := mux.Group(base + "/auth/passkeys")
	self.Use(a.require(false))
	self.GET("", func(w http.ResponseWriter, r *http.Request) {
		keys, err := a.keyStore.ListAuthPasskeys(r.Context(), identity.FromContext(r.Context()).Subject)
		if err != nil {
			nativeError(w, 503, "passkeys unavailable")
			return
		}
		httpResponseJSON(w, map[string]any{"items": keys}, 200)
	})
	self.POST("/enroll/begin", a.passkeyEnrollBegin)
	self.POST("/{id}/delete", a.passkeyDelete)
}

func (a *nativeAuth) passkeyCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: a.session.CookieName + "_ceremony", Value: value, Path: a.session.Cookie.Path + "auth/passkeys/", HttpOnly: true, Secure: a.session.Cookie.Secure == cookie.SecureAlways, SameSite: http.SameSiteStrictMode, MaxAge: maxAge}
}

func (a *nativeAuth) savePasskeyBegin(w http.ResponseWriter, r *http.Request, c service.AuthChallenge, options any) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		nativeError(w, 503, "passkeys unavailable")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	c.Hash = nativeSessionHash(token)
	if err := a.keyStore.SaveAuthChallenge(r.Context(), c); err != nil {
		if errors.Is(err, service.ErrAuthConflict) {
			nativeError(w, 429, "ceremony capacity reached; retry later")
		} else {
			nativeError(w, 503, "passkeys unavailable")
		}
		return
	}
	http.SetCookie(w, a.passkeyCookie(token, 300))
	w.Header().Set("Cache-Control", "no-store")
	httpResponseJSON(w, map[string]any{"publicKey": options}, 200)
}

func (a *nativeAuth) passkeyRate(w http.ResponseWriter) bool {
	if !a.loginLimit.Allow() {
		w.Header().Set("Retry-After", "6")
		nativeError(w, 429, "authentication rate limit exceeded")
		return false
	}
	return true
}

func (a *nativeAuth) passkeyReauth(w http.ResponseWriter, r *http.Request, currentPassword string) (*service.AuthUser, string) {
	if !a.passkeyRate(w) || !a.passwordSlot(w) {
		return nil, ""
	}
	defer func() { <-a.passwordSlots }()
	cookies := r.CookiesNamed(a.session.CookieName)
	if len(cookies) != 1 {
		nativeError(w, 401, "reauthentication required")
		return nil, ""
	}
	pair, err := a.currentSession(r)
	if err != nil {
		nativeError(w, 401, "reauthentication required")
		return nil, ""
	}
	hash := pair.SessionID
	live, _, err := a.store.ResolveAuthSession(r.Context(), hash)
	if err != nil {
		nativeError(w, 503, "authentication unavailable")
		return nil, ""
	}
	if live == nil {
		nativeError(w, 401, "reauthentication required")
		return nil, ""
	}
	u, err := a.store.GetAuthUserByID(r.Context(), live.ID)
	if err != nil {
		nativeError(w, 503, "authentication unavailable")
		return nil, ""
	}
	if u == nil || u.Disabled || u.SessionVersion != live.SessionVersion || a.password.Verify(u.PasswordHash, currentPassword) != nil {
		nativeError(w, 401, "reauthentication required")
		return nil, ""
	}
	return u, hash
}

func (a *nativeAuth) passkeyEnrollBegin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name            string `json:"name"`
		CurrentPassword string `json:"current_password"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if len(req.Name) == 0 || len(req.Name) > 80 || strings.ContainsFunc(req.Name, unicode.IsControl) {
		nativeError(w, 400, "name must be 1-80 bytes without control characters")
		return
	}
	u, hash := a.passkeyReauth(w, r, req.CurrentPassword)
	if u == nil {
		return
	}
	keys, err := a.keyStore.ListAuthPasskeys(r.Context(), u.ID)
	if err != nil {
		nativeError(w, 503, "passkeys unavailable")
		return
	}
	if len(keys) >= 20 {
		nativeError(w, 409, "maximum 20 passkeys per user")
		return
	}
	exclude := make([]passkey.PublicKeyCredentialDescriptor, 0, len(keys))
	for _, k := range keys {
		exclude = append(exclude, passkey.PublicKeyCredentialDescriptor{Type: "public-key", ID: base64.RawURLEncoding.EncodeToString(k.Credential.ID), Transports: k.Credential.Transports})
	}
	opts, data, err := a.passkey.BeginRegistration(passkey.User{Handle: []byte(u.ID), Name: u.Username, DisplayName: u.Username}, exclude)
	if err != nil {
		nativeError(w, 503, "passkeys unavailable")
		return
	}
	a.savePasskeyBegin(w, r, service.AuthChallenge{Purpose: "enroll", UserID: u.ID, Version: u.SessionVersion, SessionHash: hash, Name: req.Name, Data: *data}, opts)
}

func (a *nativeAuth) passkeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(w, r) || !a.passkeyRate(w) {
		return
	}
	var req struct {
		Username   string `json:"username"`
		RememberMe bool   `json:"remember_me"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	u, err := a.store.GetAuthUser(r.Context(), normalizeNativeUsername(req.Username))
	if err != nil {
		nativeError(w, 503, "passkeys unavailable")
		return
	}
	if u == nil || u.Disabled {
		nativeError(w, 401, "passkey login unavailable; use password")
		return
	}
	keys, err := a.keyStore.ListAuthPasskeys(r.Context(), u.ID)
	if err != nil {
		nativeError(w, 503, "passkeys unavailable")
		return
	}
	if len(keys) == 0 {
		nativeError(w, 401, "passkey login unavailable; use password")
		return
	}
	ids := make([][]byte, 0, len(keys))
	for _, key := range keys {
		ids = append(ids, key.Credential.ID)
	}
	opts, data, err := a.passkey.BeginLogin(ids)
	if err != nil {
		nativeError(w, 503, "passkeys unavailable")
		return
	}
	a.savePasskeyBegin(w, r, service.AuthChallenge{Purpose: "login", UserID: u.ID, Version: u.SessionVersion, Remember: req.RememberMe, Data: *data}, opts)
}

func (a *nativeAuth) passkeyFinish(purpose string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookies := r.CookiesNamed(a.passkeyCookie("", 0).Name)
		if len(cookies) != 1 || len(cookies[0].Value) != 43 {
			nativeError(w, 401, "invalid ceremony")
			return
		}
		c, err := a.keyStore.ConsumeAuthChallenge(r.Context(), nativeSessionHash(cookies[0].Value))
		http.SetCookie(w, a.passkeyCookie("", -1))
		if err != nil {
			nativeError(w, 503, "passkeys unavailable")
			return
		}
		if c == nil || c.Purpose != purpose {
			nativeError(w, 401, "invalid ceremony")
			return
		}
		if !a.sameOrigin(w, r) || !a.passkeyRate(w) {
			return
		}
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" {
			nativeError(w, 415, "application/json required")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64*1024))
		if err != nil {
			nativeError(w, 413, "invalid ceremony body")
			return
		}
		u, err := a.store.GetAuthUserByID(r.Context(), c.UserID)
		if err != nil {
			nativeError(w, 503, "passkeys unavailable")
			return
		}
		if u == nil || u.Disabled || u.SessionVersion != c.Version {
			nativeError(w, 401, "invalid ceremony")
			return
		}
		if purpose == "enroll" {
			sessions := r.CookiesNamed(a.session.CookieName)
			pair, resolveErr := a.currentSession(r)
			if len(sessions) != 1 || resolveErr != nil || pair.SessionID != c.SessionHash {
				nativeError(w, 401, "invalid ceremony session")
				return
			}
			live, _, err := a.store.ResolveAuthSession(r.Context(), c.SessionHash)
			if err != nil {
				nativeError(w, 503, "passkeys unavailable")
				return
			}
			if live == nil || live.ID != c.UserID || live.SessionVersion != c.Version {
				nativeError(w, 401, "invalid ceremony session")
				return
			}
			key, _, err := a.passkey.FinishRegistration(&c.Data, body)
			if err != nil {
				nativeError(w, 401, "invalid passkey response")
				return
			}
			// Ada obtains the credential ID from attested data. Also require the
			// browser envelope to identify that same credential, not a second ID.
			var response passkey.RegistrationResponseJSON
			_ = json.Unmarshal(body, &response)
			id := base64.RawURLEncoding.EncodeToString(key.ID)
			if response.RawID != id || response.ID != id {
				nativeError(w, 401, "invalid credential ID")
				return
			}
			if err := a.keyStore.CreateAuthPasskey(r.Context(), *c, *key); err != nil {
				if errors.Is(err, service.ErrAuthConflict) {
					nativeError(w, 409, "enrollment no longer valid or credential already registered")
				} else {
					nativeError(w, 503, "passkeys unavailable")
				}
				return
			}
			w.WriteHeader(204)
			return
		}
		var response passkey.AssertionResponseJSON
		if json.Unmarshal(body, &response) != nil || response.ID != response.RawID {
			nativeError(w, 401, "invalid passkey response")
			return
		}
		rawID, err := base64.RawURLEncoding.DecodeString(response.RawID)
		if err != nil {
			nativeError(w, 401, "invalid passkey response")
			return
		}
		keys, err := a.keyStore.ListAuthPasskeys(r.Context(), c.UserID)
		if err != nil {
			nativeError(w, 503, "passkeys unavailable")
			return
		}
		for _, key := range keys {
			if !bytes.Equal(rawID, key.Credential.ID) {
				continue
			}
			result, err := a.passkey.FinishLogin(&c.Data, &key.Credential, body)
			if err != nil {
				nativeError(w, 401, "invalid passkey response")
				return
			}
			if err := a.keyStore.AdvanceAuthPasskey(r.Context(), c.UserID, key.ID, c.Version, key.Credential.SignCount, result.NewSignCount, c.Data.Expires); err != nil {
				if errors.Is(err, service.ErrAuthConflict) {
					nativeError(w, 401, "passkey changed; restart login")
				} else {
					nativeError(w, 503, "passkeys unavailable")
				}
				return
			}
			ctx := context.WithValue(r.Context(), nativeCeremonyDeadlineContextKey{}, c.Data.Expires)
			a.finishLogin(w, r.WithContext(ctx), u, c.Remember)
			return
		}
		nativeError(w, 401, "invalid passkey response")
	}
}

func (a *nativeAuth) passkeyDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentPassword string `json:"current_password"`
	}
	if !decodeNativeBody(w, r, &req) {
		return
	}
	u, hash := a.passkeyReauth(w, r, req.CurrentPassword)
	if u == nil {
		return
	}
	if err := a.keyStore.DeleteAuthPasskey(r.Context(), u.ID, r.PathValue("id"), u.SessionVersion, hash); err != nil {
		if errors.Is(err, service.ErrAuthConflict) {
			nativeError(w, 409, "passkey or session no longer valid")
		} else {
			nativeError(w, 503, "passkeys unavailable")
		}
		return
	}
	a.clearCredentialCookies(w)
	w.WriteHeader(204)
}
