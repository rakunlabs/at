package server

import (
	"fmt"
	"os"
	"testing"

	"github.com/rakunlabs/at/internal/nativeauth"
	"github.com/rakunlabs/at/internal/nativeauth/nativeauthtest"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

// TestMain lowers the PBKDF2 work factor for the whole package. Fixture
// accounts store nativeauthtest.PasswordHash; a default-cost hash would pay
// full production cost (about 1.5s under -race) on every sign-in.
func TestMain(m *testing.M) {
	hash, err := nativeauth.LowerPasswordCostForTests(nativeauthtest.Password)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	nativeauthtest.PasswordHash = hash
	code := m.Run()
	postgrestest.Release()
	os.Exit(code)
}
