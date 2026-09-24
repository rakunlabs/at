package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestSkillFilesAPIUploadOverwritesMatchingPath(t *testing.T) {
	store := newFakeSkillStore()
	store.skills["skill-1"] = &service.Skill{
		ID:           "skill-1",
		Name:         "docs",
		SystemPrompt: "instructions",
		Resources:    []service.SkillResource{{Path: "references/guide.md", Content: "old"}},
	}
	s := &Server{skillStore: store}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/skills/skill-1/files", strings.NewReader(`{
		"files": [
			{"path":"references/guide.md","content":"new"},
			{"path":"scripts/run.sh","content":"echo ok"}
		]
	}`))
	req.SetPathValue("id", "skill-1")
	w := httptest.NewRecorder()

	s.PutSkillFilesAPI(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	got := store.skills["skill-1"].Resources
	if len(got) != 2 || got[0].Content != "new" || got[1].Path != "scripts/run.sh" {
		t.Fatalf("resources = %+v", got)
	}
}

func TestImportSkillFilesAPICreatesFromSelectedFolder(t *testing.T) {
	store := newFakeSkillStore()
	s := &Server{skillStore: store}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/skills/import-files", strings.NewReader(`{
		"files": [
			{"path":"my-skill/SKILL.md","content":"---\nname: folder-skill\ndescription: From folder\n---\n\nInstructions.\n"},
			{"path":"my-skill/.DS_Store","content":"ignored"},
			{"path":"my-skill/references/guide.md","content":"guide"},
			{"path":"my-skill/scripts/run.sh","content":"echo ok"}
		]
	}`))
	req = req.WithContext(service.WithAccessPrincipal(req.Context(), service.AccessPrincipal{UserID: "user-1", WorkspaceID: service.DefaultWorkspaceID}))
	w := httptest.NewRecorder()

	s.ImportSkillFilesAPI(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if len(store.created) != 1 || store.created[0].Name != "folder-skill" {
		t.Fatalf("created = %+v", store.created)
	}
	if store.created[0].OwnerUserID != "user-1" || store.created[0].Scope != "personal" {
		t.Fatalf("ownership = %+v", store.created[0])
	}
	resources := store.created[0].Resources
	if len(resources) != 2 || resources[0].Path != "references/guide.md" || resources[1].Path != "scripts/run.sh" {
		t.Fatalf("resources = %+v", resources)
	}
}

func TestDeleteSkillFileAPIProtectsMainFile(t *testing.T) {
	store := newFakeSkillStore()
	store.skills["skill-1"] = &service.Skill{ID: "skill-1", Name: "docs"}
	s := &Server{skillStore: store}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/skills/skill-1/files?path=SKILL.md", nil)
	req.SetPathValue("id", "skill-1")
	w := httptest.NewRecorder()

	s.DeleteSkillFileAPI(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestMergeSkillFilesCreatesReplacesAndKeepsStructure(t *testing.T) {
	skill := service.Skill{
		Name:         "docs",
		SystemPrompt: "old prompt",
		Resources: []service.SkillResource{
			{Path: "references/keep.md", Content: "keep"},
			{Path: "references/replace.md", Content: "old"},
		},
	}

	updated, err := mergeSkillFiles(skill, []skillFile{
		{Path: "references/replace.md", Content: "new"},
		{Path: "scripts/run.sh", Content: "#!/bin/sh\necho ok\n"},
	})
	if err != nil {
		t.Fatalf("mergeSkillFiles: %v", err)
	}
	if len(updated.Resources) != 3 {
		t.Fatalf("resources = %+v, want 3", updated.Resources)
	}
	if updated.Resources[1].Path != "references/replace.md" || updated.Resources[1].Content != "new" {
		t.Fatalf("replacement = %+v", updated.Resources[1])
	}
	if updated.Resources[2].Path != "scripts/run.sh" {
		t.Fatalf("nested path = %+v", updated.Resources[2])
	}
}

func TestMergeSkillFilesUpdatesDefinitionFromSkillMD(t *testing.T) {
	skill := service.Skill{
		Name:           "old",
		SourceURL:      "https://example.com/repo.git",
		SourceChecksum: "managed",
		Resources:      []service.SkillResource{{Path: "references/guide.md", Content: "guide"}},
	}
	main := `---
name: renamed
description: Updated
version: 2.0.0
---

New instructions.
`

	updated, err := mergeSkillFiles(skill, []skillFile{{Path: "SKILL.md", Content: main}})
	if err != nil {
		t.Fatalf("mergeSkillFiles: %v", err)
	}
	if updated.Name != "renamed" || updated.Description != "Updated" || strings.TrimSpace(updated.SystemPrompt) != "New instructions." {
		t.Fatalf("updated skill = %+v", updated)
	}
	if updated.SourceURL != skill.SourceURL || updated.SourceChecksum != skill.SourceChecksum {
		t.Fatal("editing SKILL.md changed system-managed provenance")
	}
	if len(updated.Resources) != 1 || updated.Resources[0].Path != "references/guide.md" {
		t.Fatalf("resources = %+v", updated.Resources)
	}
}

func TestCleanSkillFilePath(t *testing.T) {
	tests := []struct {
		path string
		ok   bool
	}{
		{path: "references/guide.md", ok: true},
		{path: `scripts\\run.sh`, ok: true},
		{path: "../secret", ok: false},
		{path: "/etc/passwd", ok: false},
		{path: ".git/config", ok: false},
		{path: "references/.hidden", ok: false},
		{path: "", ok: false},
	}
	for _, tt := range tests {
		t.Run(strings.ReplaceAll(tt.path, "/", "_"), func(t *testing.T) {
			_, err := cleanSkillFilePath(tt.path)
			if (err == nil) != tt.ok {
				t.Fatalf("cleanSkillFilePath(%q) error = %v, ok = %v", tt.path, err, tt.ok)
			}
		})
	}
}

func TestNormalizeSkillImportFilesRejectsMultipleOrOutsideMain(t *testing.T) {
	if _, err := normalizeSkillImportFiles([]skillFile{
		{Path: "a/SKILL.md", Content: "a"},
		{Path: "b/SKILL.md", Content: "b"},
	}); err == nil {
		t.Fatal("expected multiple SKILL.md error")
	}
	if _, err := normalizeSkillImportFiles([]skillFile{
		{Path: "skill/SKILL.md", Content: "a"},
		{Path: "outside.txt", Content: "b"},
	}); err == nil {
		t.Fatal("expected outside file error")
	}
}

func TestMergeSkillFilesRejectsBinaryAndOversizedFile(t *testing.T) {
	skill := service.Skill{Name: "test"}
	if _, err := mergeSkillFiles(skill, []skillFile{{Path: "data.txt", Content: "bad\x00data"}}); err == nil {
		t.Fatal("expected binary content error")
	}
	if _, err := mergeSkillFiles(skill, []skillFile{{Path: "large.txt", Content: strings.Repeat("x", maxSkillResourceSize+1)}}); err == nil {
		t.Fatal("expected file size error")
	}
}
