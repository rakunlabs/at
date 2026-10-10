package service

import "testing"

func TestSystemSettingsValidation(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*SystemSettings)
	}{
		{"version", func(s *SystemSettings) { s.Version = -1 }},
		{"name", func(s *SystemSettings) { s.Name = "" }},
		{"log", func(s *SystemSettings) { s.LogLevel = "trace" }},
		{"retention", func(s *SystemSettings) { s.WorkspaceTTLHours = -2 }},
		{"public path", func(s *SystemSettings) { s.ExternalURL = "https://example.com/at" }},
		{"public credentials", func(s *SystemSettings) { s.ExternalURL = "https://user:password@example.com" }},
		{"backend", func(s *SystemSettings) { s.Sandbox.Backend = "unknown" }},
		{"missing kube", func(s *SystemSettings) { s.Sandbox.Backend = "kubernetes" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := DefaultSystemSettings()
			tt.change(&s)
			if err := s.Normalize(); err == nil {
				t.Fatal("invalid setting accepted")
			}
		})
	}
	s := DefaultSystemSettings()
	s.Name = " My AT "
	s.ExternalURL = "https://example.com/"
	s.LogLevel = " DEBUG "
	if err := s.Normalize(); err != nil {
		t.Fatal(err)
	}
	if s.Name != "My AT" || s.LogLevel != "debug" || s.ExternalURL != "https://example.com" {
		t.Fatalf("not normalized: %+v", s)
	}
}
