package postgres

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

func TestTracePrivacyPostgres(t *testing.T) {
	p, ctx, ws, admin := workspaceFixture(t)
	other, err := p.CreateWorkspace(ctx, "Other privacy", admin.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Workspace admins manage workspace rules only; members nothing.
	wsAdmin := workspaceMember(t, p, ctx, ws.ID, workspaceUser(t, p, "privacy-admin"), "admin")
	member := workspaceMember(t, p, ctx, ws.ID, workspaceUser(t, p, "privacy-member"), "member")
	if _, err := p.SaveTracePrivacyRule(member, service.TracePrivacyRule{Enabled: true}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("member created a rule: %v", err)
	}
	if _, err := p.SaveTracePrivacyRule(wsAdmin, service.TracePrivacyRule{Scope: "installation", Enabled: true}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("workspace admin created an installation rule: %v", err)
	}
	wsRule, err := p.SaveTracePrivacyRule(wsAdmin, service.TracePrivacyRule{Enabled: true, Model: "secret-*", Action: "skip"})
	if err != nil || wsRule.WorkspaceID != ws.ID || wsRule.Scope != "workspace" {
		t.Fatalf("workspace rule: %+v %v", wsRule, err)
	}
	instRule, err := p.SaveTracePrivacyRule(ctx, service.TracePrivacyRule{Scope: "installation", Enabled: true, UserID: "u-redact", Action: "redact"})
	if err != nil || instRule.WorkspaceID != "" {
		t.Fatalf("installation rule: %+v %v", instRule, err)
	}
	if rules, err := p.ListTracePrivacyRules(wsAdmin); err != nil || len(rules) != 1 {
		t.Fatalf("workspace admin sees installation rules: %d %v", len(rules), err)
	}
	if rules, err := p.ListTracePrivacyRules(ctx); err != nil || len(rules) != 2 {
		t.Fatalf("platform admin list: %d %v", len(rules), err)
	}
	if err := p.DeleteTracePrivacyRule(wsAdmin, instRule.ID); !errors.Is(err, service.ErrTracePrivacyRuleNotFound) {
		t.Fatalf("workspace admin deleted installation rule: %v", err)
	}

	// Policy and opt-out.
	if err := p.SetUserPreference(ctx, service.UserPreference{UserID: "opted", Key: service.TracePrivacyOptOutKey, Value: json.RawMessage("true")}); err != nil {
		t.Fatal(err)
	}
	policy, err := p.LoadTracePrivacyPolicy(t.Context())
	if err != nil || len(policy.Rules) != 2 || len(policy.OptedOutUsers) != 0 {
		t.Fatalf("policy: %+v %v", policy, err)
	}
	if _, err := p.SaveTracePrivacySettings(wsAdmin, service.TracePrivacySettings{AllowUserOptOut: true}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("workspace admin changed installation settings: %v", err)
	}
	if _, err := p.SaveTracePrivacySettings(ctx, service.TracePrivacySettings{AllowUserOptOut: true}); err != nil {
		t.Fatal(err)
	}
	policy, _ = p.LoadTracePrivacyPolicy(t.Context())
	if !policy.Settings.AllowUserOptOut || !policy.OptedOutUsers["opted"] {
		t.Fatalf("opt-out not loaded: %+v", policy)
	}

	// Apply to history.
	record := func(id, workspace, trace, model, user string) {
		t.Helper()
		if err := p.RecordLLMCall(t.Context(), service.LLMCall{ID: id, WorkspaceID: workspace, TraceID: trace, Provider: "p", Model: model, UserID: user,
			RequestBody: "body-" + id, Input: "in", RequestRef: "/tmp/x/.at-llm-audit/d/" + id + "-request.json",
			Trace: &service.TraceUpdate{Name: "n", Input: "trace-in"}}); err != nil {
			t.Fatal(err)
		}
	}
	record("a1", ws.ID, "t-secret", "secret-model", "")
	record("a2", ws.ID, "t-secret", "", "") // tool row of the same trace
	record("b1", ws.ID, "t-plain", "plain", "")
	record("c1", other.ID, "t-other", "secret-model", "") // other workspace
	record("d1", ws.ID, "t-user", "plain", "u-redact")
	record("d2", ws.ID, "t-user", "", "")
	one := 1.0
	if _, err := p.CreateTraceScore(ctx, service.TraceScore{TraceID: "t-secret", Name: "q", DataType: service.ScoreNumeric, Value: &one}); err != nil {
		t.Fatal(err)
	}

	dry, err := p.ApplyTracePrivacyRule(wsAdmin, wsRule.ID, true)
	if err != nil || dry.Traces != 1 || dry.Observations != 2 {
		t.Fatalf("dry run: %+v %v", dry, err)
	}
	count := func(trace string) int64 {
		n, _ := p.goqu.From(p.tableLLMCalls).Where(goqu.Ex{"trace_id": trace}).CountContext(t.Context())
		return n
	}
	if count("t-secret") != 2 {
		t.Fatal("dry run deleted rows")
	}
	res, err := p.ApplyTracePrivacyRule(wsAdmin, wsRule.ID, false)
	if err != nil || res.Traces != 1 || len(res.SpillRefs) != 2 {
		t.Fatalf("apply: %+v %v", res, err)
	}
	if count("t-secret") != 0 || count("t-plain") != 1 || count("t-other") != 1 {
		t.Fatal("skip apply removed the wrong traces")
	}
	if n, _ := p.goqu.From(p.tableTraceScores).Where(goqu.Ex{"trace_id": "t-secret"}).CountContext(t.Context()); n != 0 {
		t.Fatal("scores of a deleted trace survived")
	}

	red, err := p.ApplyTracePrivacyRule(ctx, instRule.ID, false)
	if err != nil || red.Traces != 1 || red.Observations != 2 {
		t.Fatalf("redact apply: %+v %v", red, err)
	}
	var bodies []string
	if err := p.goqu.From(p.tableLLMCalls).Select(goqu.L("request_body || input || request_ref")).Where(goqu.Ex{"trace_id": "t-user"}).ScanValsContext(t.Context(), &bodies); err != nil || len(bodies) != 2 || bodies[0] != "" || bodies[1] != "" {
		t.Fatalf("redact left content: %q %v", bodies, err)
	}
	var plain string
	if _, err := p.goqu.From(p.tableLLMCalls).Select("request_body").Where(goqu.Ex{"id": "b1"}).ScanValContext(t.Context(), &plain); err != nil || plain != "body-b1" {
		t.Fatalf("redact touched another trace: %q", plain)
	}

	// Recorder path: retroactive cleanup of one trace.
	if _, err := p.ApplyTracePrivacyToTrace(t.Context(), ws.ID, "t-plain", service.TracePrivacySkip); err != nil || count("t-plain") != 0 {
		t.Fatalf("trace cleanup: %v", err)
	}
}
