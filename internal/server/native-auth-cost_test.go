package server

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

// testPassword is the plaintext every fixture account signs in with.
const testPassword = "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

// testPasswordHash is the stored hash for fixture accounts, built at the
// lowered work factor below. Fixtures used testPasswordHash, which happens to be
// a hash of this same plaintext — but built once at the 600k default. Since an
// encoded hash is verified with its own iteration count, every fixture sign-in
// paid full production cost under the race detector (about 1.5s per login) no
// matter what nativePasswordIterations said.
var testPasswordHash string

// TestMain lowers the PBKDF2 work factor for the whole package. Production
// keeps the library default; under -race, 600k rounds take minutes per hash
// and the auth tests alone exceed the go test timeout.
func TestMain(m *testing.M) {
	nativePasswordIterations = 1000
	hasher := nativePasswordHasher()
	hash, err := hasher.Hash(testPassword)
	if err != nil {
		fmt.Fprintln(os.Stderr, "build test password hash:", err)
		os.Exit(1)
	}
	testPasswordHash = hash
	// The unknown-user path derives against this one, so it has to be lowered
	// too or every rejected sign-in stays as expensive as before.
	if nativePasswordDummy, err = hasher.Hash(strings.Repeat("d", 32)); err != nil {
		fmt.Fprintln(os.Stderr, "build test dummy hash:", err)
		os.Exit(1)
	}
	code := m.Run()
	postgrestest.Release()
	os.Exit(code)
}
