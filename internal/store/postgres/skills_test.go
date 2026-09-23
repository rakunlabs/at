package postgres

import (
	"context"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestSkill_ProvenanceRoundTrip(t *testing.T) {
	// Installation-scope tests act as the platform operator on the legacy workspace.
	ctx := service.WithLegacyWorkspaceAccess(context.Background())
	store := newTestStore(t, nil)

	in := service.Skill{
		Name:               "prov-skill",
		Description:        "Skill with provenance",
		Category:           "Utilities",
		Tags:               []string{"share"},
		SystemPrompt:       "Do things.",
		Tools:              []service.Tool{{Name: "t1", Description: "d", InputSchema: map[string]any{"type": "object"}}},
		Resources:          []service.SkillResource{{Path: "references/guide.md", Content: "guide", MediaType: "text/markdown"}},
		Version:            "1.0.0",
		Author:             "Jane Doe",
		License:            "MIT",
		SourceURL:          "https://example.com/skill.json",
		SourceType:         "git",
		SourceRef:          "main",
		SourcePath:         "skills/prov-skill",
		SourceCredentialID: "git-credential-1",
		SourceChecksum:     "abc123",
		CreatedBy:          "tester",
		UpdatedBy:          "tester",
	}

	created, err := store.CreateSkill(ctx, in)
	if err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}

	got, err := store.GetSkill(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetSkill: %v", err)
	}
	if got == nil {
		t.Fatal("GetSkill returned nil")
	}

	if got.Version != "1.0.0" || got.Author != "Jane Doe" || got.License != "MIT" {
		t.Errorf("provenance = %q/%q/%q, want 1.0.0/Jane Doe/MIT", got.Version, got.Author, got.License)
	}
	if got.SourceURL != "https://example.com/skill.json" {
		t.Errorf("source_url = %q", got.SourceURL)
	}
	if got.SourceChecksum != "abc123" {
		t.Errorf("source_checksum = %q", got.SourceChecksum)
	}
	if got.SourceType != "git" || got.SourceRef != "main" || got.SourcePath != "skills/prov-skill" {
		t.Fatalf("source package metadata = %q/%q/%q", got.SourceType, got.SourceRef, got.SourcePath)
	}
	if got.SourceCredentialID != "git-credential-1" {
		t.Fatalf("source credential = %q", got.SourceCredentialID)
	}
	if len(got.Resources) != 1 || got.Resources[0].Content != "guide" {
		t.Fatalf("resources = %+v", got.Resources)
	}

	// Update keeps provenance when caller passes it through.
	got.Version = "1.1.0"
	got.UpdatedBy = "tester2"
	updated, err := store.UpdateSkill(ctx, created.ID, *got)
	if err != nil {
		t.Fatalf("UpdateSkill: %v", err)
	}
	if updated == nil {
		t.Fatal("UpdateSkill returned nil")
	}
	if updated.Version != "1.1.0" {
		t.Errorf("updated version = %q, want 1.1.0", updated.Version)
	}
	if updated.SourceChecksum != "abc123" {
		t.Errorf("updated source_checksum = %q, want abc123", updated.SourceChecksum)
	}

	// GetSkillByName also returns the new columns.
	byName, err := store.GetSkillByName(ctx, "prov-skill")
	if err != nil {
		t.Fatalf("GetSkillByName: %v", err)
	}
	if byName == nil || byName.Author != "Jane Doe" {
		t.Fatalf("GetSkillByName = %+v, want author Jane Doe", byName)
	}
}
