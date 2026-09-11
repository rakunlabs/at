package service

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// AuthSettings is a versioned installation policy, never a session record.
type AuthSettings struct {
	Version            int64  `json:"version"`
	Origin             string `json:"origin"`
	SessionTTLSeconds  int64  `json:"session_ttl_seconds"`
	RememberTTLSeconds int64  `json:"remember_ttl_seconds"`
	LocalLoginEnabled  bool   `json:"local_login_enabled"`
	SignupAdmission    string `json:"signup_admission"`
	DisplayTitle       string `json:"display_title"`
	MFAPolicy          string `json:"mfa_policy"`
	MaxSessions        int    `json:"max_sessions"`
}

func DefaultAuthSettings() AuthSettings {
	return AuthSettings{Version: 1, SessionTTLSeconds: 28800, RememberTTLSeconds: 2592000, LocalLoginEnabled: true, SignupAdmission: "invite_only", DisplayTitle: "AT", MFAPolicy: "enrolled_required", MaxSessions: 20}
}

// Validate allows an empty origin only before the installation is claimed.
func (s AuthSettings) Validate(unclaimed bool) error {
	if s.Version < 1 || s.SessionTTLSeconds < 600 || s.SessionTTLSeconds > 86400 || s.RememberTTLSeconds < s.SessionTTLSeconds || s.RememberTTLSeconds > 2592000 {
		return fmt.Errorf("invalid authentication version or session lifetime")
	}
	if s.SignupAdmission != "invite_only" && s.SignupAdmission != "approval_required" {
		return fmt.Errorf("signup admission must require invitation or approval")
	}
	if s.MFAPolicy != "enrolled_required" || s.MaxSessions != 20 {
		return fmt.Errorf("enrolled MFA and the session ceiling cannot be bypassed")
	}
	if strings.TrimSpace(s.DisplayTitle) == "" || len(s.DisplayTitle) > 128 || strings.ContainsAny(s.DisplayTitle, "\r\n\x00") {
		return fmt.Errorf("display title must contain 1-128 bytes")
	}
	if unclaimed && s.Origin == "" {
		return nil
	}
	return ValidateAuthOrigin(s.Origin)
}

func ValidateAuthOrigin(origin string) error {
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || u.Opaque != "" || origin != u.Scheme+"://"+u.Host || strings.ContainsAny(u.Host, "\\%") {
		return fmt.Errorf("authentication origin must be an exact HTTP(S) origin")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && (u.Scheme != "http" || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()))) {
		return fmt.Errorf("authentication requires HTTPS except on loopback")
	}
	return nil
}

type AuthSettingsState struct {
	Settings      AuthSettings
	SetupRequired bool
}

type AuthSettingsStorer interface {
	// Import is insert-only. A claimed store lacking an origin fails closed.
	InitializeAuthSettings(context.Context, AuthSettings) (*AuthSettingsState, error)
	GetAuthSettings(context.Context) (*AuthSettingsState, error)
	SaveAuthSettings(context.Context, AuthSettings) (*AuthSettings, error)
	// Claim, settings persistence and local administrator insertion are one commit.
	SetupAuth(context.Context, AuthUser, AuthSettings) (*AuthUser, error)
}
