package server

import (
	"net/http"
	"testing"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/identity"
	"golang.org/x/time/rate"

	"github.com/rakunlabs/at/internal/httpx"
	"github.com/rakunlabs/at/internal/nativeauth"
	"github.com/rakunlabs/at/internal/nativeauth/nativeauthtest"
	"github.com/rakunlabs/at/internal/service"
)

func nativeFixture(t *testing.T) (*nativeauth.Auth, *nativeauthtest.FakeStore, *ada.Server) {
	t.Helper()
	f := &nativeauthtest.FakeStore{Users: map[string]service.AuthUser{
		"admin":  {ID: "admin", Username: "admin", PasswordHash: nativeauthtest.PasswordHash, Admin: true},
		"reader": {ID: "reader", Username: "reader", PasswordHash: nativeauthtest.PasswordHash},
	}, Sessions: make(map[string]service.AuthSession)}
	a, err := nativeauth.New(nativeauthtest.Config(), f)
	if err != nil {
		t.Fatal(err)
	}
	a.LoginLimit = rate.NewLimiter(rate.Inf, 100)
	mux := ada.New()
	a.Register(mux, "/at")
	api := mux.Group("/at/api")
	api.Use(a.Require(true))
	api.GET("/v1/test", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, identity.FromContext(r.Context()), 200)
	})
	api.POST("/v1/test", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	return a, f, mux
}
