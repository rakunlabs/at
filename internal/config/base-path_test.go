package config

import "testing"

// base_path reaches the route table two different ways: ada groups, which run
// path.Join and tolerate almost anything, and raw prefix concatenation for the
// /auth, workspace and runtime route tables, which does not. Normalizing once
// at load time is what keeps the two in agreement.
func TestNormalizeBasePath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty stays root", "", ""},
		{"canonical is unchanged", "/at", "/at"},
		{"trailing slash is dropped", "/at/", "/at"},
		{"repeated trailing slashes are dropped", "/at///", "/at"},
		{"missing leading slash is added", "at", "/at"},
		{"bare and trailing combined", "at/", "/at"},
		{"root slash becomes empty", "/", ""},
		{"nested prefix", "/apps/at/", "/apps/at"},
		{"duplicate separators collapse", "/apps//at", "/apps/at"},
		{"dot segments are cleaned", "/apps/./at", "/apps/at"},
		{"surrounding space is ignored", "  /at  ", "/at"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeBasePath(tt.in)
			if err != nil {
				t.Fatalf("NormalizeBasePath(%q) returned %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeBasePath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// Input that cannot be made safe is reported instead of being mangled into a
// prefix that silently routes somewhere else.
func TestNormalizeBasePathRejectsUnsafe(t *testing.T) {
	for _, in := range []string{"/at?x=1", "/at#frag", "/at%2f", `/at\x`, "/../etc", "..", "/apps/x/../at"} {
		t.Run(in, func(t *testing.T) {
			if got, err := NormalizeBasePath(in); err == nil {
				t.Fatalf("NormalizeBasePath(%q) = %q, want an error", in, got)
			}
		})
	}
}

// Normalization must be a fixed point: applying it to its own output cannot
// change the value, or a second pass somewhere in startup would drift.
func TestNormalizeBasePathIsIdempotent(t *testing.T) {
	for _, in := range []string{"", "/at", "/at/", "at", "/apps/at//"} {
		once, err := NormalizeBasePath(in)
		if err != nil {
			t.Fatalf("NormalizeBasePath(%q): %v", in, err)
		}
		twice, err := NormalizeBasePath(once)
		if err != nil {
			t.Fatalf("NormalizeBasePath(%q): %v", once, err)
		}
		if once != twice {
			t.Fatalf("not idempotent for %q: %q then %q", in, once, twice)
		}
	}
}
