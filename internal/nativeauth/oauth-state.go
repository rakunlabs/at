package nativeauth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type nativeOAuthState struct {
	sessionHash string
	payload     string
	target      string
	expires     time.Time
}

// Native OAuth state is opaque, bounded, short-lived and tied to the exact
// initiating session and return endpoint. Legacy state encoding stays unchanged.
func (a *Auth) NewOAuthState(r *http.Request, payload, target string) (string, error) {
	return a.newOAuthState(r, "", payload, target)
}

// NewScopedOAuthState is NewOAuthState with a nonsecret "<prefix>." in front
// of the nonce. A provider callback is a top-level navigation that cannot
// carry the workspace header, so the prefix tells the server which workspace
// admits it; the stored entry stays bound to the initiating session.
func (a *Auth) NewScopedOAuthState(r *http.Request, prefix, payload, target string) (string, error) {
	if prefix == "" || len(prefix) > 128 || strings.ContainsAny(prefix, ". \t\r\n") {
		return "", fmt.Errorf("invalid OAuth state scope")
	}
	return a.newOAuthState(r, prefix+".", payload, target)
}

func (a *Auth) newOAuthState(r *http.Request, prefix, payload, target string) (string, error) {
	c, err := a.CurrentSession(r)
	if err != nil {
		return "", fmt.Errorf("OAuth requires a session: %w", err)
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("generate OAuth state: %w", err)
	}
	a.oauthMu.Lock()
	defer a.oauthMu.Unlock()
	if a.oauthStates == nil {
		a.oauthStates = make(map[string]nativeOAuthState)
	}
	for key, entry := range a.oauthStates {
		if !entry.expires.After(time.Now()) {
			delete(a.oauthStates, key)
		}
	}
	if len(a.oauthStates) >= 1024 {
		return "", fmt.Errorf("too many pending OAuth authorizations; retry later")
	}
	state := prefix + base64.RawURLEncoding.EncodeToString(nonce[:])
	a.oauthStates[state] = nativeOAuthState{sessionHash: c.SessionID, payload: payload, target: target, expires: time.Now().Add(10 * time.Minute)}
	return state, nil
}

func (a *Auth) TakeOAuthState(w http.ResponseWriter, r *http.Request, target string) (string, bool) {
	c, err := a.CurrentSession(r)
	state := r.URL.Query().Get("state")
	a.oauthMu.Lock()
	defer a.oauthMu.Unlock()
	entry, ok := a.oauthStates[state]
	if err != nil || !ok || !entry.expires.After(time.Now()) || entry.target != target || entry.sessionHash != c.SessionID {
		WriteError(w, http.StatusForbidden, "invalid or expired OAuth state; restart authorization")
		return "", false
	}
	delete(a.oauthStates, state)
	return entry.payload, true
}

// ExpireOAuthStateForTests makes a pending state already expired, so handler
// tests can check expiry without waiting ten minutes.
func (a *Auth) ExpireOAuthStateForTests(state string) {
	a.oauthMu.Lock()
	defer a.oauthMu.Unlock()
	if entry, ok := a.oauthStates[state]; ok {
		entry.expires = time.Now().Add(-time.Second)
		a.oauthStates[state] = entry
	}
}
