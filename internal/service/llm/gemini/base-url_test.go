package gemini

import "testing"

func TestNormalizeBaseURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"host", "https://generativelanguage.googleapis.com", "https://generativelanguage.googleapis.com"},
		{"trailing slash", "https://generativelanguage.googleapis.com/", "https://generativelanguage.googleapis.com"},
		{"versioned", "https://generativelanguage.googleapis.com/v1beta", "https://generativelanguage.googleapis.com"},
		{"versioned slash", "https://generativelanguage.googleapis.com/v1beta/", "https://generativelanguage.googleapis.com"},
		{"custom prefix", "https://proxy.example/google", "https://proxy.example/google"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeBaseURL(tt.in); got != tt.want {
				t.Fatalf("NormalizeBaseURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// A versioned base URL used to stream to /v1beta/v1beta/... and 404.
func TestNewDropsVersionedBaseURL(t *testing.T) {
	p, err := New("key", "gemini-2.5-flash", "https://generativelanguage.googleapis.com/v1beta", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if p.BaseURL != "https://generativelanguage.googleapis.com" {
		t.Fatalf("BaseURL = %q", p.BaseURL)
	}
}
