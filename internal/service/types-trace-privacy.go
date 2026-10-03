package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Trace privacy rules decide, before an observation is written, whether it is
// recorded at all (skip) or recorded without its content (redact). They govern
// the trace store and trace export only: cost events, budgets and the Usage
// dashboard are written independently and are never affected, so suppressing
// traces is not a way around spending limits.
const (
	TracePrivacyNone   = ""
	TracePrivacyRedact = "redact"
	TracePrivacySkip   = "skip"
)

// TracePrivacyOptOutKey is the user preference an account sets to stop its own
// observations being recorded. It is honoured only while the installation
// setting AllowUserOptOut is on.
const TracePrivacyOptOutKey = "trace_opt_out"

var (
	ErrTracePrivacyRuleNotFound = errors.New("trace privacy rule not found")
	ErrTracePrivacyInvalid      = errors.New("invalid trace privacy rule")
)

// TracePrivacyRule matches observations on server-stamped attributes. Empty
// fields match everything; set fields are combined with AND. A rule without a
// workspace is an installation rule and applies in every workspace.
type TracePrivacyRule struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Scope       string `json:"scope"` // installation | workspace (derived)
	Description string `json:"description"`
	UserID      string `json:"user_id"`
	TokenID     string `json:"token_id"`
	Provider    string `json:"provider"`
	// Model is a case-insensitive glob (`*`, `?`). A pattern containing `/`
	// is matched against "provider/model".
	Model     string `json:"model"`
	Source    string `json:"source"`
	Action    string `json:"action"`
	Enabled   bool   `json:"enabled"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// TracePrivacySettings are installation-wide.
type TracePrivacySettings struct {
	AllowUserOptOut bool   `json:"allow_user_opt_out"`
	UpdatedAt       string `json:"updated_at,omitempty"`
}

// TracePrivacyPolicy is everything the recorder needs, loaded in one read.
type TracePrivacyPolicy struct {
	Rules         []TracePrivacyRule
	Settings      TracePrivacySettings
	OptedOutUsers map[string]bool
}

// TraceObservationFacts are the attributes a rule may match on. All of them
// are stamped by the server, never taken from request content.
type TraceObservationFacts struct {
	WorkspaceID string
	UserID      string
	TokenID     string
	Provider    string
	Model       string
	Source      string
}

// TracePrivacyApplyResult reports what applying a rule to stored traces did
// (or, for a dry run, would do).
type TracePrivacyApplyResult struct {
	DryRun       bool     `json:"dry_run"`
	Action       string   `json:"action"`
	Traces       int64    `json:"traces"`
	Observations int64    `json:"observations"`
	SpillRefs    []string `json:"-"`
}

// TracePrivacyStorer persists rules and settings. LoadTracePrivacyPolicy is an
// internal lookup for the recorder; every other method revalidates the caller.
type TracePrivacyStorer interface {
	LoadTracePrivacyPolicy(ctx context.Context) (TracePrivacyPolicy, error)
	ListTracePrivacyRules(ctx context.Context) ([]TracePrivacyRule, error)
	SaveTracePrivacyRule(ctx context.Context, rule TracePrivacyRule) (*TracePrivacyRule, error)
	DeleteTracePrivacyRule(ctx context.Context, id string) error
	ApplyTracePrivacyRule(ctx context.Context, id string, dryRun bool) (*TracePrivacyApplyResult, error)
	GetTracePrivacySettings(ctx context.Context) (TracePrivacySettings, error)
	SaveTracePrivacySettings(ctx context.Context, settings TracePrivacySettings) (TracePrivacySettings, error)
	// ApplyTracePrivacyToTrace retroactively applies an action to one trace
	// whose earlier observations were recorded before the decision was known.
	ApplyTracePrivacyToTrace(ctx context.Context, workspaceID, traceID, action string) ([]string, error)
}

// NormalizeTracePrivacyRule trims fields, derives the scope and validates.
func NormalizeTracePrivacyRule(r TracePrivacyRule) (TracePrivacyRule, error) {
	r.Description = strings.TrimSpace(r.Description)
	r.UserID = strings.TrimSpace(r.UserID)
	r.TokenID = strings.TrimSpace(r.TokenID)
	r.Provider = strings.TrimSpace(r.Provider)
	r.Model = strings.TrimSpace(r.Model)
	r.Source = strings.TrimSpace(r.Source)
	r.Action = strings.TrimSpace(r.Action)
	if r.Action == "" {
		r.Action = TracePrivacySkip
	}
	if r.Action != TracePrivacySkip && r.Action != TracePrivacyRedact {
		return r, fmt.Errorf("%w: action must be skip or redact", ErrTracePrivacyInvalid)
	}
	if len(r.Description) > 512 || !utf8.ValidString(r.Description) {
		return r, fmt.Errorf("%w: description must be at most 512 bytes", ErrTracePrivacyInvalid)
	}
	for name, v := range map[string]string{"user_id": r.UserID, "token_id": r.TokenID, "provider": r.Provider, "model": r.Model, "source": r.Source} {
		if len(v) > 256 || !utf8.ValidString(v) || strings.IndexFunc(v, unicode.IsControl) >= 0 {
			return r, fmt.Errorf("%w: %s must be at most 256 printable characters", ErrTracePrivacyInvalid, name)
		}
	}
	if r.WorkspaceID == "" {
		r.Scope = "installation"
	} else {
		r.Scope = "workspace"
	}
	return r, nil
}

// Matches reports whether the rule applies to an observation.
func (r TracePrivacyRule) Matches(f TraceObservationFacts) bool {
	if !r.Enabled {
		return false
	}
	if r.WorkspaceID != "" && r.WorkspaceID != f.WorkspaceID {
		return false
	}
	if r.UserID != "" && r.UserID != f.UserID {
		return false
	}
	if r.TokenID != "" && r.TokenID != f.TokenID {
		return false
	}
	if r.Provider != "" && r.Provider != f.Provider {
		return false
	}
	if r.Source != "" && r.Source != f.Source {
		return false
	}
	if r.Model != "" {
		subject := f.Model
		if strings.Contains(r.Model, "/") {
			subject = f.Provider + "/" + f.Model
		}
		if f.Model == "" || !GlobMatch(strings.ToLower(r.Model), strings.ToLower(subject)) {
			return false
		}
	}
	return true
}

// Decide returns the strictest action of every matching rule and the user's
// own opt-out, plus the ID of the rule that decided it ("opt-out" for the
// user preference).
func (p *TracePrivacyPolicy) Decide(f TraceObservationFacts) (string, string) {
	if p == nil {
		return TracePrivacyNone, ""
	}
	if p.Settings.AllowUserOptOut && f.UserID != "" && p.OptedOutUsers[f.UserID] {
		return TracePrivacySkip, "opt-out"
	}
	action, by := TracePrivacyNone, ""
	for _, r := range p.Rules {
		if !r.Matches(f) {
			continue
		}
		if StricterTracePrivacy(r.Action, action) == r.Action && r.Action != action {
			action, by = r.Action, r.ID
		}
		if action == TracePrivacySkip {
			break
		}
	}
	return action, by
}

// StricterTracePrivacy returns the stricter of two actions.
func StricterTracePrivacy(a, b string) string {
	rank := func(v string) int {
		switch v {
		case TracePrivacySkip:
			return 2
		case TracePrivacyRedact:
			return 1
		}
		return 0
	}
	if rank(a) >= rank(b) {
		return a
	}
	return b
}

// GlobMatch matches `*` (any run, including `/`) and `?` (one rune).
func GlobMatch(pattern, s string) bool {
	p, str := []rune(pattern), []rune(s)
	pi, si, star, mark := 0, 0, -1, 0
	for si < len(str) {
		switch {
		case pi < len(p) && (p[pi] == '?' || p[pi] == str[si]):
			pi++
			si++
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, si
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			si = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

// GlobToLike converts a GlobMatch pattern into a SQL LIKE pattern with `\`
// as the escape character.
func GlobToLike(pattern string) string {
	var b strings.Builder
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteByte('%')
		case '?':
			b.WriteByte('_')
		case '%', '_', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
