package service

import "testing"

func TestTracePrivacyRuleMatching(t *testing.T) {
	facts := TraceObservationFacts{WorkspaceID: "ws", UserID: "u1", TokenID: "t1", Provider: "openai", Model: "gpt-5-mini", Source: "gateway"}
	tests := []struct {
		name string
		rule TracePrivacyRule
		want bool
	}{
		{"empty rule matches everything", TracePrivacyRule{Enabled: true}, true},
		{"disabled never matches", TracePrivacyRule{UserID: "u1"}, false},
		{"user", TracePrivacyRule{Enabled: true, UserID: "u1"}, true},
		{"other user", TracePrivacyRule{Enabled: true, UserID: "u2"}, false},
		{"and of fields", TracePrivacyRule{Enabled: true, UserID: "u1", TokenID: "t2"}, false},
		{"other workspace", TracePrivacyRule{Enabled: true, WorkspaceID: "other"}, false},
		{"installation rule", TracePrivacyRule{Enabled: true, Provider: "openai"}, true},
		{"glob", TracePrivacyRule{Enabled: true, Model: "GPT-5*"}, true},
		{"glob miss", TracePrivacyRule{Enabled: true, Model: "claude-*"}, false},
		{"qualified glob", TracePrivacyRule{Enabled: true, Model: "openai/*mini"}, true},
		{"qualified glob other provider", TracePrivacyRule{Enabled: true, Model: "azure/*"}, false},
		{"source", TracePrivacyRule{Enabled: true, Source: "chat"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rule.Matches(facts); got != tt.want {
				t.Fatalf("Matches = %v, want %v", got, tt.want)
			}
		})
	}
	// A model rule never matches an observation that carries no model (tool
	// rows); the trace-level suppression covers those.
	if (TracePrivacyRule{Enabled: true, Model: "*"}).Matches(TraceObservationFacts{}) {
		t.Fatal("model rule matched a model-less observation")
	}
}

func TestTracePrivacyDecideStrictest(t *testing.T) {
	p := &TracePrivacyPolicy{Rules: []TracePrivacyRule{
		{ID: "r", Enabled: true, Action: TracePrivacyRedact, Provider: "openai"},
		{ID: "s", Enabled: true, Action: TracePrivacySkip, UserID: "u1"},
	}}
	if a, by := p.Decide(TraceObservationFacts{Provider: "openai", UserID: "u1"}); a != TracePrivacySkip || by != "s" {
		t.Fatalf("got %s by %s", a, by)
	}
	if a, _ := p.Decide(TraceObservationFacts{Provider: "openai", UserID: "u2"}); a != TracePrivacyRedact {
		t.Fatalf("got %s", a)
	}
	if a, _ := p.Decide(TraceObservationFacts{Provider: "anthropic"}); a != TracePrivacyNone {
		t.Fatalf("got %s", a)
	}
	p.OptedOutUsers = map[string]bool{"u3": true}
	if a, _ := p.Decide(TraceObservationFacts{UserID: "u3"}); a != TracePrivacyNone {
		t.Fatal("opt-out honoured while the installation forbids it")
	}
	p.Settings.AllowUserOptOut = true
	if a, by := p.Decide(TraceObservationFacts{UserID: "u3"}); a != TracePrivacySkip || by != "opt-out" {
		t.Fatalf("opt-out ignored: %s %s", a, by)
	}
	var nilPolicy *TracePrivacyPolicy
	if a, _ := nilPolicy.Decide(TraceObservationFacts{}); a != TracePrivacyNone {
		t.Fatal("nil policy decided")
	}
}

func TestTracePrivacyNormalize(t *testing.T) {
	r, err := NormalizeTracePrivacyRule(TracePrivacyRule{Model: "  gpt-* "})
	if err != nil || r.Action != TracePrivacySkip || r.Model != "gpt-*" || r.Scope != "installation" {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := NormalizeTracePrivacyRule(TracePrivacyRule{Action: "hide"}); err == nil {
		t.Fatal("unknown action accepted")
	}
	if _, err := NormalizeTracePrivacyRule(TracePrivacyRule{UserID: "a\nb"}); err == nil {
		t.Fatal("control character accepted")
	}
}

func TestGlob(t *testing.T) {
	for _, c := range []struct {
		p, s string
		want bool
	}{
		{"*", "", true}, {"a*c", "abbbc", true}, {"a?c", "abc", true}, {"a?c", "ac", false},
		{"*mini", "gpt-5-mini", true}, {"gpt-5", "gpt-5-mini", false},
	} {
		if got := GlobMatch(c.p, c.s); got != c.want {
			t.Errorf("GlobMatch(%q,%q)=%v", c.p, c.s, got)
		}
	}
	if got := GlobToLike(`a*b?c%d_e\`); got != `a%b_c\%d\_e\\` {
		t.Fatalf("GlobToLike = %q", got)
	}
}
