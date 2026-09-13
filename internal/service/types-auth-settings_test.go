package service

import "testing"

func TestAuthSettingsOrigins(t *testing.T) {
	for _, tt := range []struct {
		name, primary string
		additional    []string
		valid         bool
	}{
		{"legacy", "https://at.example", nil, true},
		{"multiple", "https://at.example", []string{"https://internal.example", "https://at.example:8443"}, true},
		{"loopback", "http://localhost:8080", []string{"http://127.0.0.1:8080"}, true},
		{"mixed transport", "http://localhost:8080", []string{"https://at.example"}, true},
		{"HTTPS and HTTP loopback", "https://at.example", []string{"http://localhost:8080", "http://127.0.0.1:8080", "http://[::1]:8080"}, true},
		{"duplicate primary", "https://at.example", []string{"https://at.example"}, false},
		{"duplicate alias", "https://at.example", []string{"https://other.example", "https://other.example"}, false},
		{"wildcard", "https://at.example", []string{"*"}, false},
		{"path", "https://at.example", []string{"https://other.example/path"}, false},
		{"trailing slash", "https://at.example", []string{"https://other.example/"}, false},
		{"public http", "https://at.example", []string{"http://other.example"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := DefaultAuthSettings()
			s.Origin = tt.primary
			s.AllowedOrigins = tt.additional
			if err := s.Validate(false); (err == nil) != tt.valid {
				t.Fatalf("validation: %v", err)
			}
		})
	}
}
