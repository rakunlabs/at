package server

import (
	"fmt"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/ada"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func TestMobileBeginSourceLimits(t *testing.T) {
	a := &nativeAuth{mobileBeginSources: make(map[string]nativeMobileSource)}
	now := time.Now()
	r := httptest.NewRequest("POST", "/auth/mobile/begin", nil)
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 60 {
		wg.Go(func() {
			if a.allowMobileBegin(r, now) {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if allowed.Load() != 30 {
		t.Fatalf("concurrent admission: %d", allowed.Load())
	}
	r.RemoteAddr = "192.0.2.1:9999"
	r.Header.Set("X-Forwarded-For", "198.51.100.42")
	r.Header.Set("X-Real-IP", "198.51.100.42")
	r.Header.Set("Forwarded", "for=198.51.100.42")
	if a.allowMobileBegin(r, now) {
		t.Fatal("port/forwarded headers reset quota")
	}
	r.RemoteAddr = "[::ffff:192.0.2.1]:1234"
	if a.allowMobileBegin(r, now) {
		t.Fatal("mapped IP reset quota")
	}
	r.RemoteAddr = "198.51.100.42:1234"
	if !a.allowMobileBegin(r, now) {
		t.Fatal("one source starved another")
	}
	r.RemoteAddr = "192.0.2.1:1234"
	if !a.allowMobileBegin(r, now.Add(time.Second)) {
		t.Fatal("quota did not replenish")
	}
	for i := range 1022 {
		r.RemoteAddr = fmt.Sprintf("[2001:db8::%x]:1234", i+1)
		if !a.allowMobileBegin(r, now) {
			t.Fatal("early source cap", i)
		}
	}
	r.RemoteAddr = "203.0.113.1:1234"
	if a.allowMobileBegin(r, now) || len(a.mobileBeginSources) != 1024 {
		t.Fatal("unbounded source map")
	}
	if !a.allowMobileBegin(r, now.Add(6*time.Minute)) || len(a.mobileBeginSources) != 1 {
		t.Fatal("idle source cleanup failed")
	}
}

func TestMobileBeginExhaustionPreservesCredentials(t *testing.T) {
	p := postgrestest.New(t, nil)
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	u, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "mobile-limit", PasswordHash: "unused"}, false)
	if err != nil {
		t.Fatal(err)
	}
	access, refresh, err := nativeCredentialPair()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.CreateAuthSession(t.Context(), service.AuthSession{Hash: "limited-mobile", UserID: u.ID, Version: u.SessionVersion, Transport: "mobile", AccessHash: nativeSessionHash(access), RefreshHash: nativeSessionHash(refresh), ExpiresAt: time.Now().Add(time.Hour), AccessExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	mux := ada.New()
	a.register(mux, "/at")
	// Invalid unauthenticated begins still spend the peer's admission quota.
	for range 30 {
		if w := nativeRequest(mux, "POST", "/at/auth/mobile/begin", `{}`, "", nil); w.Code != 400 {
			t.Fatal(w.Code, w.Body)
		}
	}
	if w := nativeRequest(mux, "POST", "/at/auth/mobile/begin", `{}`, "", nil); w.Code != 429 {
		t.Fatal("begin not exhausted", w.Code)
	}
	if w := nativeRequest(mux, "POST", "/at/auth/mobile/refresh", `{"refresh_token":"`+refresh+`"}`, "", nil); w.Code != 200 {
		t.Fatal("begin starved refresh", w.Code, w.Body)
	}
	// Even the now-consumed refresh identifies the entire family for logout.
	if w := nativeRequest(mux, "POST", "/at/auth/mobile/logout", `{"refresh_token":"`+refresh+`"}`, "", nil); w.Code != 204 || len(w.Result().Cookies()) != 0 {
		t.Fatal("begin starved logout", w.Code, w.Body)
	}
	if user, _, err := p.ResolveAuthSession(t.Context(), "limited-mobile"); err != nil || user != nil {
		t.Fatal("logout did not revoke", err)
	}
}
