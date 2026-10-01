package nativeauth

import (
	"fmt"
	"os"
	"testing"

	"github.com/rakunlabs/at/internal/nativeauth/nativeauthtest"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

// TestMain lowers the PBKDF2 work factor for the whole package. Production
// keeps the library default; under -race, 600k rounds take minutes per hash
// and the auth tests alone exceed the go test timeout.
func TestMain(m *testing.M) {
	hash, err := LowerPasswordCostForTests(nativeauthtest.Password)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	nativeauthtest.PasswordHash = hash
	code := m.Run()
	postgrestest.Release()
	os.Exit(code)
}
