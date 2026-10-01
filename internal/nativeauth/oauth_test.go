package nativeauth

import (
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rakunlabs/at/internal/nativeauth/nativeauthtest"
)

func TestNativeOAuthStateSingleUseConcurrent(t *testing.T) {
	a, _, mux := nativeFixture(t)
	c := nativeauthtest.LoginCookie(t, mux, "admin")
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(c)
	state, err := a.NewOAuthState(r, "test::conn::destination", "callback")
	if err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			r := httptest.NewRequest("GET", "/?state="+state, nil)
			r.AddCookie(c)
			payload, ok := a.TakeOAuthState(httptest.NewRecorder(), r, "callback")
			if ok {
				winners.Add(1)
				if payload != "test::conn::destination" {
					t.Errorf("destination changed: %q", payload)
				}
			}
		})
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("state consumed %d times", winners.Load())
	}
}
