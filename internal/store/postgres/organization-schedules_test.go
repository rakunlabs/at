package postgres

import (
	"errors"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestOrganizationScheduleTriggers(t *testing.T) {
	p, ctx, _, admin := workspaceFixture(t)
	org, err := p.CreateOrganization(ctx, service.Organization{Name: "Shorts"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{"schedule": "0 7 * * *", "task_title": "Daily Short"}
	tr, err := p.CreateTrigger(ctx, service.Trigger{TargetType: service.TriggerTargetOrganization, TargetID: org.ID, Type: "cron", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if tr.TargetType != service.TriggerTargetOrganization || tr.TargetID != org.ID || tr.WorkflowID != "" {
		t.Fatalf("stored target: %+v", tr)
	}
	tr.Enabled = true
	if _, err := p.UpdateTrigger(ctx, tr.ID, *tr); err != nil {
		t.Fatalf("update organization schedule: %v", err)
	}

	for name, bad := range map[string]service.Trigger{
		"http":          {TargetType: service.TriggerTargetOrganization, TargetID: org.ID, Type: "http"},
		"entry node":    {TargetType: service.TriggerTargetOrganization, TargetID: org.ID, Type: "cron", EntryNodeID: "input"},
		"unknown org":   {TargetType: service.TriggerTargetOrganization, TargetID: "missing", Type: "cron"},
		"with workflow": {TargetType: service.TriggerTargetOrganization, TargetID: org.ID, WorkflowID: "wf", Type: "cron"},
	} {
		if _, err := p.CreateTrigger(ctx, bad); !errors.Is(err, service.ErrAccessResourceNotFound) {
			t.Fatalf("%s: %v", name, err)
		}
	}

	// Another workspace cannot schedule this workspace's organization.
	b, err := p.CreateWorkspace(ctx, "B", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	principalB, _, err := p.ResolveWorkspaceAccess(ctx, b.ID, admin.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	ctxB := service.WithAccessPrincipal(ctx, principalB)
	if _, err := p.CreateTrigger(ctxB, service.Trigger{TargetType: service.TriggerTargetOrganization, TargetID: org.ID, Type: "cron", Config: cfg}); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("foreign organization: %v", err)
	}
}
