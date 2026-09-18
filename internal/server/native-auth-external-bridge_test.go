package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/ada/middleware/auth/session"

	"github.com/rakunlabs/at/internal/config"
)

func bridgeTestAuth() *nativeExternalAuth {
	return &nativeExternalAuth{a: &nativeAuth{
		cfg:     config.NativeAuth{Origin: "https://at.example"},
		session: session.Session{Cookie: session.CookieOptions{Path: "/at/"}},
	}}
}

// The identity provider returns the browser to the callback as a top-level
// navigation inside the popup the UI opened, so the response has to be a
// document that hands the result to the opener. Answering JSON parked the popup
// on a page of JSON and the opener waited for a message that never arrived.
func TestExternalCallbackPopupBridge(t *testing.T) {
	e := bridgeTestAuth()
	tests := []struct {
		name     string
		record   func(w http.ResponseWriter)
		code     int
		contains []string
		excludes []string
	}{
		{
			name: "identity is delivered to the opener",
			record: func(w http.ResponseWriter) {
				w.Header().Set("Set-Cookie", "at_session=value; Path=/at/")
				httpResponseJSON(w, map[string]any{"subject": "user"}, 200)
			},
			code:     200,
			contains: []string{`"at-auth-result"`, `"subject":"user"`, `postMessage(payload, "https://at.example")`},
			// The wildcard target would hand an MFA challenge to whatever page
			// happens to be the opener.
			excludes: []string{`postMessage(payload, "*")`},
		},
		{
			name: "pending second factor travels as a result",
			record: func(w http.ResponseWriter) {
				httpResponseJSON(w, map[string]any{"mfa_required": true, "challenge": "token"}, 200)
			},
			code:     200,
			contains: []string{`"mfa_required":true`, `"challenge":"token"`},
		},
		{
			name: "refusal is reported instead of hanging",
			record: func(w http.ResponseWriter) {
				nativeError(w, 401, "linking account changed; restart")
			},
			code:     401,
			contains: []string{`"error":true`, "linking account changed"},
		},
		{
			name:     "a body that is not JSON cannot be passed off as a result",
			record:   func(w http.ResponseWriter) { _, _ = w.Write([]byte("upstream failure")) },
			code:     200,
			contains: []string{`"error":true`},
			excludes: []string{"upstream failure"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.record(rec)
			w := httptest.NewRecorder()
			e.deliverCallback(w, rec)
			if w.Code != tt.code {
				t.Fatalf("status %d, want %d", w.Code, tt.code)
			}
			if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
				t.Fatalf("content type %q", w.Header().Get("Content-Type"))
			}
			for _, want := range tt.contains {
				if !strings.Contains(w.Body.String(), want) {
					t.Fatalf("missing %q in %s", want, w.Body)
				}
			}
			for _, reject := range tt.excludes {
				if strings.Contains(w.Body.String(), reject) {
					t.Fatalf("unexpected %q in %s", reject, w.Body)
				}
			}
			// The document carries a session-bearing result: it must not be
			// framable and must run only its own nonce-pinned script.
			policy := w.Header().Get("Content-Security-Policy")
			nonce, _, _ := strings.Cut(strings.TrimPrefix(policy, "default-src 'none'; script-src 'nonce-"), "'")
			if nonce == "" || !strings.Contains(policy, "frame-ancestors 'none'") || w.Header().Get("X-Frame-Options") != "DENY" || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("weak bridge headers: %q", policy)
			}
			if strings.Count(w.Body.String(), `nonce="`+nonce+`"`) != 2 || strings.Contains(w.Body.String(), "<script>") {
				t.Fatalf("script is not nonce-pinned: %s", w.Body)
			}
		})
	}
}

// Cookies are the entire point of the callback, and the continuation is the
// only redirect target the flow accepts — it must reach the opener as data, not
// as a header nobody reads.
func TestExternalCallbackBridgeHeaders(t *testing.T) {
	e := bridgeTestAuth()
	rec := httptest.NewRecorder()
	rec.Header().Add("Set-Cookie", "at_session=value; Path=/at/")
	rec.Header().Add("Set-Cookie", "at_refresh=value; Path=/at/auth/")
	rec.Header().Set("X-AT-Auth-Continue", "/at/#/mobile-authorize?request_id=abc")
	httpResponseJSON(rec, map[string]any{"subject": "user"}, 200)
	w := httptest.NewRecorder()
	e.deliverCallback(w, rec)
	if len(w.Header().Values("Set-Cookie")) != 2 {
		t.Fatalf("session cookies lost: %v", w.Header().Values("Set-Cookie"))
	}
	if w.Header().Get("X-AT-Auth-Continue") != "" {
		t.Fatal("continuation header forwarded instead of consumed")
	}
	if !strings.Contains(w.Body.String(), `"continue":"/at/#/mobile-authorize?request_id=abc"`) {
		t.Fatalf("continuation not delivered: %s", w.Body)
	}
}

// ada restarts an interrupted flow by redirecting; wrapping that in a document
// would strand the popup on a page instead of continuing the ceremony.
func TestExternalCallbackBridgeReplaysRedirect(t *testing.T) {
	e := bridgeTestAuth()
	rec := httptest.NewRecorder()
	rec.Header().Set("Location", "https://idp.example/authorize?state=abc")
	rec.WriteHeader(http.StatusFound)
	w := httptest.NewRecorder()
	e.deliverCallback(w, rec)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "https://idp.example/authorize?state=abc" || w.Body.Len() != 0 {
		t.Fatalf("redirect not replayed: %d %q %s", w.Code, w.Header().Get("Location"), w.Body)
	}
}

// The payload is caller-influenced (an upstream error message reaches it), so
// it must not be able to close the script element it is embedded in.
func TestExternalCallbackBridgeEscapesPayload(t *testing.T) {
	e := bridgeTestAuth()
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", "application/json")
	rec.WriteHeader(200)
	_, _ = rec.Write([]byte(`{"message":"</script><script>alert(1)</script>"}`))
	w := httptest.NewRecorder()
	e.deliverCallback(w, rec)
	if strings.Contains(w.Body.String(), "</script><script>") || strings.Contains(w.Body.String(), "alert(1)</script>") {
		t.Fatalf("payload broke out of its script element: %s", w.Body)
	}
	if !strings.Contains(w.Body.String(), `\u003c/script\u003e`) {
		t.Fatalf("payload not escaped: %s", w.Body)
	}
}
