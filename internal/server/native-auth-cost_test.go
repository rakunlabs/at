package server

import "testing"

// TestMain lowers the PBKDF2 work factor for the whole package. Production
// keeps the library default; under -race, 600k rounds take minutes per hash
// and the auth tests alone exceed the go test timeout.
func TestMain(m *testing.M) {
	nativePasswordIterations = 1000
	m.Run()
}
