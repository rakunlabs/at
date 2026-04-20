package server

import "testing"

func TestParseOAuthState(t *testing.T) {
	tests := []struct {
		name         string
		state        string
		wantProvider string
		wantUserID   string
		wantConnID   string
	}{
		{"bare provider", "youtube", "youtube", "", ""},
		{"provider + user_id", "google::discord::12345", "google", "discord::12345", ""},
		{"provider + connection_id", "youtube::conn::conn_01HV", "youtube", "", "conn_01HV"},
		{"empty", "", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotP, gotU, gotC := parseOAuthState(tt.state)
			if gotP != tt.wantProvider {
				t.Errorf("provider: got %q, want %q", gotP, tt.wantProvider)
			}
			if gotU != tt.wantUserID {
				t.Errorf("user_id: got %q, want %q", gotU, tt.wantUserID)
			}
			if gotC != tt.wantConnID {
				t.Errorf("connection_id: got %q, want %q", gotC, tt.wantConnID)
			}
		})
	}
}

func TestBuildOAuthState(t *testing.T) {
	tests := []struct {
		name         string
		provider     string
		userID       string
		connectionID string
		want         string
	}{
		{"bare", "youtube", "", "", "youtube"},
		{"user_id", "google", "discord::12345", "", "google::discord::12345"},
		{"connection_id", "youtube", "", "conn_01HV", "youtube::conn::conn_01HV"},
		{"connection_id wins over user_id", "youtube", "ignored", "conn_01HV", "youtube::conn::conn_01HV"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildOAuthState(tt.provider, tt.userID, tt.connectionID)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOAuthStateRoundTrip(t *testing.T) {
	// build then parse: must recover the same parts.
	cases := []struct {
		p, u, c string
	}{
		{"youtube", "", ""},
		{"google", "discord::1234", ""},
		{"youtube", "", "conn_01HV"},
	}
	for _, tc := range cases {
		encoded := buildOAuthState(tc.p, tc.u, tc.c)
		p, u, c := parseOAuthState(encoded)
		if p != tc.p || u != tc.u || c != tc.c {
			t.Errorf("roundtrip(%q, %q, %q) -> %q -> (%q, %q, %q)", tc.p, tc.u, tc.c, encoded, p, u, c)
		}
	}
}
