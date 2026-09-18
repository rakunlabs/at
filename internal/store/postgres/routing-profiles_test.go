package postgres

import (
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/worldline-go/types"
)

func TestRoutingProfileCRUDAndValidation(t *testing.T) {
	p, ctx, _, _ := workspaceFixture(t)

	created, err := p.CreateRoutingProfile(ctx, service.RoutingProfile{
		Name:        "my-stack",
		Description: "primary then cheap backup",
		Targets:     types.Slice[string]{"anthropic/claude-sonnet-4-6", "openai/gpt-4.1"},
		CreatedBy:   "admin@example.com",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" || created.WorkspaceID == "" {
		t.Fatalf("create did not populate identity: %+v", created)
	}
	if len(created.Targets) != 2 || created.Targets[0] != "anthropic/claude-sonnet-4-6" {
		t.Fatalf("targets not preserved in order: %+v", created.Targets)
	}

	byName, err := p.GetRoutingProfileByName(ctx, "my-stack")
	if err != nil || byName == nil {
		t.Fatalf("get by name: %v %+v", err, byName)
	}
	if byName.ID != created.ID {
		t.Fatalf("get by name returned a different row: %q vs %q", byName.ID, created.ID)
	}

	// Not-found is (nil, nil), never an error.
	missing, err := p.GetRoutingProfileByName(ctx, "absent")
	if err != nil || missing != nil {
		t.Fatalf("absent profile must be (nil, nil), got %+v %v", missing, err)
	}

	updated, err := p.UpdateRoutingProfile(ctx, created.ID, service.RoutingProfile{
		Name:      "my-stack",
		Targets:   types.Slice[string]{"openai/gpt-4.1"},
		UpdatedBy: "editor@example.com",
	})
	if err != nil || updated == nil {
		t.Fatalf("update: %v %+v", err, updated)
	}
	if len(updated.Targets) != 1 || updated.Targets[0] != "openai/gpt-4.1" {
		t.Fatalf("update did not replace targets: %+v", updated.Targets)
	}
	if updated.UpdatedBy != "editor@example.com" {
		t.Fatalf("update did not record actor: %q", updated.UpdatedBy)
	}

	// Updating a row that does not exist is (nil, nil), matching the other CRUDs.
	ghost, err := p.UpdateRoutingProfile(ctx, "01ABSENTABSENTABSENTABSENT", service.RoutingProfile{
		Name:    "ghost",
		Targets: types.Slice[string]{"openai/gpt-4.1"},
	})
	if err != nil || ghost != nil {
		t.Fatalf("update of absent row must be (nil, nil), got %+v %v", ghost, err)
	}

	if err := p.DeleteRoutingProfile(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	gone, err := p.GetRoutingProfile(ctx, created.ID)
	if err != nil || gone != nil {
		t.Fatalf("profile still present after delete: %+v %v", gone, err)
	}
}

func TestRoutingProfileRejectsInvalidInput(t *testing.T) {
	p, ctx, _, _ := workspaceFixture(t)

	cases := []struct {
		name    string
		profile service.RoutingProfile
	}{
		{"slash in name", service.RoutingProfile{Name: "team/stack", Targets: types.Slice[string]{"openai/gpt-4.1"}}},
		{"empty name", service.RoutingProfile{Name: "   ", Targets: types.Slice[string]{"openai/gpt-4.1"}}},
		{"illegal character", service.RoutingProfile{Name: "my stack", Targets: types.Slice[string]{"openai/gpt-4.1"}}},
		{"no targets", service.RoutingProfile{Name: "empty", Targets: types.Slice[string]{}}},
		{"unqualified target", service.RoutingProfile{Name: "bare", Targets: types.Slice[string]{"gpt-4.1"}}},
		{"target missing model", service.RoutingProfile{Name: "trailing", Targets: types.Slice[string]{"openai/"}}},
		{"duplicate target", service.RoutingProfile{Name: "dup", Targets: types.Slice[string]{"openai/gpt-4.1", "openai/gpt-4.1"}}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := p.CreateRoutingProfile(ctx, tt.profile); err == nil {
				t.Fatalf("expected rejection for %s", tt.name)
			}
		})
	}
}

func TestRoutingProfileNameUniquePerWorkspace(t *testing.T) {
	p, ctx, _, _ := workspaceFixture(t)

	if _, err := p.CreateRoutingProfile(ctx, service.RoutingProfile{
		Name:    "shared",
		Targets: types.Slice[string]{"openai/gpt-4.1"},
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := p.CreateRoutingProfile(ctx, service.RoutingProfile{
		Name:    "shared",
		Targets: types.Slice[string]{"anthropic/claude-sonnet-4-6"},
	}); err == nil {
		t.Fatal("expected UNIQUE(workspace_id,name) violation on duplicate name")
	}
}

// The gateway resolves profiles by (name, workspace) without a session
// principal, so this is the path a real request takes.
func TestRoutingProfileGatewayLookupIsWorkspaceScoped(t *testing.T) {
	p, ctx, w, admin := workspaceFixture(t)

	if _, err := p.CreateRoutingProfile(ctx, service.RoutingProfile{
		Name:    "my-stack",
		Targets: types.Slice[string]{"openai/gpt-4.1"},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	other, err := p.CreateWorkspace(ctx, "Beta", admin.ID)
	if err != nil {
		t.Fatalf("create second workspace: %v", err)
	}

	found, err := p.GetGatewayRoutingProfile(t.Context(), "my-stack", w.ID)
	if err != nil || found == nil {
		t.Fatalf("gateway lookup in owning workspace: %+v %v", found, err)
	}

	// A token bound to another workspace must not resolve this name.
	foreign, err := p.GetGatewayRoutingProfile(t.Context(), "my-stack", other.ID)
	if err != nil || foreign != nil {
		t.Fatalf("profile leaked across workspaces: %+v %v", foreign, err)
	}

	// A token with no workspace binding has no profiles; there is no public one.
	unbound, err := p.GetGatewayRoutingProfile(t.Context(), "my-stack", "")
	if err != nil || unbound != nil {
		t.Fatalf("unbound token resolved a profile: %+v %v", unbound, err)
	}

	listed, err := p.ListGatewayRoutingProfiles(t.Context(), w.ID)
	if err != nil || len(listed) != 1 {
		t.Fatalf("gateway list: %+v %v", listed, err)
	}
	if empty, err := p.ListGatewayRoutingProfiles(t.Context(), other.ID); err != nil || len(empty) != 0 {
		t.Fatalf("gateway list leaked across workspaces: %+v %v", empty, err)
	}
}
