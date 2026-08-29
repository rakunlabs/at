package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// TestSkillTemplatesParse ensures every embedded skill template is valid JSON
// and has its required declarative fields on every supported platform.
func TestSkillTemplatesParse(t *testing.T) {
	entries, err := skillTemplateFS.ReadDir("skill_templates")
	if err != nil {
		t.Fatalf("read skill_templates dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no skill templates embedded")
	}

	seenSlugs := map[string]string{}
	seenSkillNames := map[string]string{}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := skillTemplateFS.ReadFile("skill_templates/" + entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}

		var tmpl SkillTemplate
		if err := json.Unmarshal(data, &tmpl); err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		if err := validateSkillTemplate(tmpl, nil); err != nil {
			t.Errorf("validate %s: %v", entry.Name(), err)
		}

		if prev, ok := seenSlugs[tmpl.Slug]; ok {
			t.Errorf("%s: duplicate slug %q (also in %s)", entry.Name(), tmpl.Slug, prev)
		}
		seenSlugs[tmpl.Slug] = entry.Name()
		if prev, ok := seenSkillNames[tmpl.Skill.Name]; ok {
			t.Errorf("%s: duplicate skill name %q (also in %s)", entry.Name(), tmpl.Skill.Name, prev)
		}
		seenSkillNames[tmpl.Skill.Name] = entry.Name()
	}
}

// TestSkillTemplateBashSyntax parses bash handlers without executing bash or
// embedded Python bodies. Required-field validation remains covered above on
// platforms that do not provide bash.
func TestSkillTemplateBashSyntax(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash unavailable; skipping embedded handler syntax validation: %v", err)
	}

	entries, err := skillTemplateFS.ReadDir("skill_templates")
	if err != nil {
		t.Fatalf("read skill_templates dir: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := skillTemplateFS.ReadFile("skill_templates/" + entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		var tmpl SkillTemplate
		if err := json.Unmarshal(data, &tmpl); err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		for _, tool := range tmpl.Skill.Tools {
			if tool.HandlerType != "bash" {
				continue
			}
			cmd := exec.Command(bash, "-n")
			cmd.Stdin = strings.NewReader(tool.Handler)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("%s tool %q has invalid bash syntax: %v: %s", entry.Name(), tool.Name, err, strings.TrimSpace(string(output)))
			}
		}
	}
}

func TestValidateSkillTemplateRejectsMalformedDefinitions(t *testing.T) {
	valid := SkillTemplate{
		Slug:        "example",
		Name:        "Example",
		Description: "Example template",
		Category:    "Utilities",
		Skill: SkillTemplateData{
			Name:         "example",
			Description:  "Example skill",
			SystemPrompt: "Use the example tool.",
			Tools: []service.Tool{{
				Name:        "example_tool",
				Description: "Run the example",
				InputSchema: map[string]any{"type": "object"},
				HandlerType: "bash",
				Handler:     "printf '%s\\n' ok",
			}},
		},
	}

	tests := []struct {
		name   string
		mutate func(*SkillTemplate)
		want   string
	}{
		{"template field", func(tmpl *SkillTemplate) { tmpl.Description = "" }, "description is required"},
		{"skill field", func(tmpl *SkillTemplate) { tmpl.Skill.SystemPrompt = "" }, "skill.system_prompt is required"},
		{"tool description", func(tmpl *SkillTemplate) { tmpl.Skill.Tools[0].Description = "" }, "skill.tools[0].description is required"},
		{"tool input schema", func(tmpl *SkillTemplate) { tmpl.Skill.Tools[0].InputSchema = nil }, "skill.tools[0].inputSchema is required"},
		{"tool schema root", func(tmpl *SkillTemplate) { tmpl.Skill.Tools[0].InputSchema["type"] = "string" }, "inputSchema.type must be"},
		{"tool handler type", func(tmpl *SkillTemplate) { tmpl.Skill.Tools[0].HandlerType = "python" }, "handler_type \"python\" is unsupported"},
		{"bash syntax", func(tmpl *SkillTemplate) { tmpl.Skill.Tools[0].Handler = "invalid" }, "invalid bash syntax"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl := valid
			tmpl.Skill.Tools = append([]service.Tool(nil), valid.Skill.Tools...)
			tmpl.Skill.Tools[0].InputSchema = map[string]any{"type": "object"}
			tt.mutate(&tmpl)
			err := validateSkillTemplate(tmpl, func(handler string) error {
				if handler == "invalid" {
					return fmt.Errorf("parse failure")
				}
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validateSkillTemplate() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestSkillTemplateManagedChecksum(t *testing.T) {
	tools := []service.Tool{
		{
			Name:        "first",
			Description: "First tool",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}},
			HandlerType: "js",
			Handler:     "return args.value;",
		},
		{
			Name:        "second",
			Description: "Second tool",
			InputSchema: map[string]any{"type": "object"},
			HandlerType: "bash",
			Handler:     "printf second",
		},
	}
	tmpl := SkillTemplateData{SystemPrompt: "prompt", Tools: tools}
	want := checksumForSkillTemplate(t, tmpl)

	tests := []struct {
		name   string
		mutate func(*SkillTemplateData)
		equal  bool
	}{
		{"identical", func(*SkillTemplateData) {}, true},
		{"system prompt", func(data *SkillTemplateData) { data.SystemPrompt = "changed" }, false},
		{"tool name", func(data *SkillTemplateData) { data.Tools[0].Name = "changed" }, false},
		{"tool description", func(data *SkillTemplateData) { data.Tools[0].Description = "changed" }, false},
		{"tool input schema", func(data *SkillTemplateData) { data.Tools[0].InputSchema["required"] = []any{"value"} }, false},
		{"tool handler type", func(data *SkillTemplateData) { data.Tools[0].HandlerType = "bash" }, false},
		{"tool handler", func(data *SkillTemplateData) { data.Tools[0].Handler = "changed" }, false},
		{"tool order", func(data *SkillTemplateData) { data.Tools[0], data.Tools[1] = data.Tools[1], data.Tools[0] }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := SkillTemplateData{SystemPrompt: tmpl.SystemPrompt, Tools: cloneTools(t, tmpl.Tools)}
			tt.mutate(&candidate)
			got := checksumForSkillTemplate(t, candidate)
			if (got == want) != tt.equal {
				t.Fatalf("checksum equality = %v, want %v", got == want, tt.equal)
			}
		})
	}
}

func TestSkillTemplateSyncUpgradesUntouchedTemplate(t *testing.T) {
	old := SkillTemplateData{
		Name:         "embedded_skill",
		SystemPrompt: "old prompt",
		Tools: []service.Tool{{
			Name:        "tool",
			Description: "old description",
			InputSchema: map[string]any{"type": "object"},
			HandlerType: "bash",
			Handler:     "old handler",
		}},
	}
	current := SkillTemplateData{
		Name:         old.Name,
		SystemPrompt: "new prompt",
		Tools: []service.Tool{{
			Name:        "tool",
			Description: "new description",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}},
			HandlerType: "bash",
			Handler:     "fixed handler",
		}},
	}

	store := newFakeSkillStore()
	installed := templateOwnedSkill(t, "skill-1", "embedded", old)
	installed.Description = "User description"
	installed.Category = "User category"
	installed.Tags = []string{"user", "curated"}
	installed.Version = "2.3.4"
	installed.Author = "User Author"
	installed.License = "MIT"
	installed.CreatedAt = "2026-01-01T00:00:00Z"
	installed.UpdatedAt = "2026-01-02T00:00:00Z"
	installed.CreatedBy = "owner@example.com"
	installed.UpdatedBy = "owner@example.com"
	store.skills[installed.ID] = installed
	s := &Server{
		skillStore: store,
		skillTemplates: []SkillTemplate{{
			Slug:  "embedded",
			Skill: current,
		}},
	}

	s.syncInstalledSkillHandlers(t.Context())

	if len(store.updated) != 1 {
		t.Fatalf("updates = %d, want 1", len(store.updated))
	}
	updated := store.updated[0]
	if updated.SystemPrompt != current.SystemPrompt || !reflect.DeepEqual(updated.Tools, current.Tools) {
		t.Fatalf("template-owned content not synced: prompt=%q tools=%#v", updated.SystemPrompt, updated.Tools)
	}
	if updated.SourceChecksum != checksumForSkillTemplate(t, current) {
		t.Errorf("source checksum = %q, want current template checksum", updated.SourceChecksum)
	}
	wantMetadata := *installed
	wantMetadata.SystemPrompt = updated.SystemPrompt
	wantMetadata.Tools = updated.Tools
	wantMetadata.SourceChecksum = updated.SourceChecksum
	wantMetadata.UpdatedBy = "system"
	if !reflect.DeepEqual(updated, wantMetadata) {
		t.Errorf("sync changed user-owned metadata\ngot:  %#v\nwant: %#v", updated, wantMetadata)
	}

	s.syncInstalledSkillHandlers(t.Context())
	if len(store.updated) != 1 {
		t.Fatalf("idempotent sync updates = %d, want 1", len(store.updated))
	}
}

func TestSkillTemplateSyncPreservesCustomizedHandler(t *testing.T) {
	old := SkillTemplateData{
		Name:         "embedded_skill",
		SystemPrompt: "prompt",
		Tools:        []service.Tool{{Name: "tool", Description: "description", InputSchema: map[string]any{"type": "object"}, HandlerType: "bash", Handler: "old handler"}},
	}
	current := old
	current.Tools = cloneTools(t, old.Tools)
	current.Tools[0].Handler = "embedded bug fix"
	installed := templateOwnedSkill(t, "skill-1", "embedded", old)
	store := newFakeSkillStore()
	store.skills[installed.ID] = installed
	customized := *installed
	customized.Tools = cloneTools(t, installed.Tools)
	customized.Tools[0].Handler = "user custom handler"
	customized.SourceURL = "builtin://skill-template/forged"
	customized.SourceChecksum = checksumForSkillTemplate(t, SkillTemplateData{SystemPrompt: customized.SystemPrompt, Tools: customized.Tools})
	body, err := json.Marshal(customized)
	if err != nil {
		t.Fatalf("marshal customized skill: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/skills/skill-1", bytes.NewReader(body))
	req.SetPathValue("id", installed.ID)
	w := httptest.NewRecorder()
	s := &Server{skillStore: store}
	s.UpdateSkillAPI(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := store.skills[installed.ID]; got.SourceURL != installed.SourceURL || got.SourceChecksum != installed.SourceChecksum {
		t.Fatalf("user update changed system provenance to %q/%q", got.SourceURL, got.SourceChecksum)
	}
	store.updated = nil
	s.skillTemplates = []SkillTemplate{{Slug: "embedded", Skill: current}}

	s.syncInstalledSkillHandlers(t.Context())

	if len(store.updated) != 0 {
		t.Fatalf("updates = %d, want 0 for customized handler", len(store.updated))
	}
	if got := store.skills[installed.ID].Tools[0].Handler; got != "user custom handler" {
		t.Fatalf("handler = %q, want user customization preserved", got)
	}
}

func TestSkillTemplateSyncDescriptionAndSchemaOwnership(t *testing.T) {
	old := SkillTemplateData{
		Name:         "embedded_skill",
		SystemPrompt: "prompt",
		Tools:        []service.Tool{{Name: "tool", Description: "old description", InputSchema: map[string]any{"type": "object"}, HandlerType: "js", Handler: "return true;"}},
	}
	current := old
	current.Tools = cloneTools(t, old.Tools)
	current.Tools[0].Description = "new embedded description"
	current.Tools[0].InputSchema["properties"] = map[string]any{"value": map[string]any{"type": "string"}}

	tests := []struct {
		name       string
		customize  func(*service.Skill)
		wantUpdate bool
	}{
		{"untouched updates", func(*service.Skill) {}, true},
		{"custom description preserved", func(skill *service.Skill) { skill.Tools[0].Description = "user description" }, false},
		{"custom schema preserved", func(skill *service.Skill) { skill.Tools[0].InputSchema["required"] = []any{"user_field"} }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installed := templateOwnedSkill(t, "skill-1", "embedded", old)
			tt.customize(installed)
			store := newFakeSkillStore()
			store.skills[installed.ID] = installed
			s := &Server{skillStore: store, skillTemplates: []SkillTemplate{{Slug: "embedded", Skill: current}}}

			s.syncInstalledSkillHandlers(t.Context())

			if got := len(store.updated); got != btoi(tt.wantUpdate) {
				t.Fatalf("updates = %d, want %d", got, btoi(tt.wantUpdate))
			}
			if !tt.wantUpdate && !reflect.DeepEqual(store.skills[installed.ID].Tools, installed.Tools) {
				t.Fatal("customized tool definition was overwritten")
			}
		})
	}
}

func TestSkillTemplateSyncLegacyOwnership(t *testing.T) {
	managed := SkillTemplateData{
		Name:         "embedded_skill",
		SystemPrompt: "prompt",
		Tools:        []service.Tool{{Name: "tool", Description: "description", InputSchema: map[string]any{"type": "object"}, HandlerType: "js", Handler: "return true;"}},
	}

	tests := []struct {
		name       string
		customize  func(*service.Skill)
		wantUpdate bool
	}{
		{"exact current content is enrolled", func(*service.Skill) {}, true},
		{"previous system sync upgrades", func(skill *service.Skill) {
			skill.Tools[0].Handler = "older embedded handler"
			skill.UpdatedBy = "system"
		}, true},
		{"drift is preserved", func(skill *service.Skill) { skill.Tools[0].Handler = "unknown legacy edit" }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installed := &service.Skill{
				ID:           "skill-1",
				Name:         managed.Name,
				SystemPrompt: managed.SystemPrompt,
				Tools:        cloneTools(t, managed.Tools),
			}
			tt.customize(installed)
			store := newFakeSkillStore()
			store.skills[installed.ID] = installed
			s := &Server{skillStore: store, skillTemplates: []SkillTemplate{{Slug: "embedded", Skill: managed}}}

			s.syncInstalledSkillHandlers(t.Context())

			if got := len(store.updated); got != btoi(tt.wantUpdate) {
				t.Fatalf("updates = %d, want %d", got, btoi(tt.wantUpdate))
			}
			if tt.wantUpdate {
				updated := store.updated[0]
				if updated.SourceURL != skillTemplateSourceURL("embedded") || updated.SourceChecksum != checksumForSkillTemplate(t, managed) {
					t.Fatalf("legacy ownership marker = %q/%q", updated.SourceURL, updated.SourceChecksum)
				}
				if updated.UpdatedBy == "system" && !reflect.DeepEqual(updated.Tools, managed.Tools) {
					t.Fatal("previously synchronized legacy template was not upgraded")
				}
			}
		})
	}
}

func TestInstallSkillTemplateMarksManagedContent(t *testing.T) {
	tmpl := SkillTemplate{
		Slug:     "embedded",
		Category: "Utilities",
		Skill: SkillTemplateData{
			Name:         "embedded_skill",
			Description:  "description",
			SystemPrompt: "prompt",
			Tools:        []service.Tool{},
		},
	}
	store := newFakeSkillStore()
	s := &Server{skillStore: store, skillTemplates: []SkillTemplate{tmpl}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/skill-templates/embedded/install", nil)
	req.SetPathValue("slug", "embedded")
	w := httptest.NewRecorder()

	s.InstallSkillTemplateAPI(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusCreated, w.Body.String())
	}
	if len(store.created) != 1 {
		t.Fatalf("created skills = %d, want 1", len(store.created))
	}
	created := store.created[0]
	if created.SourceURL != skillTemplateSourceURL(tmpl.Slug) {
		t.Errorf("source URL = %q, want %q", created.SourceURL, skillTemplateSourceURL(tmpl.Slug))
	}
	if created.SourceChecksum != checksumForSkillTemplate(t, tmpl.Skill) {
		t.Errorf("source checksum = %q, want managed content checksum", created.SourceChecksum)
	}
}

func checksumForSkillTemplate(t *testing.T, tmpl SkillTemplateData) string {
	t.Helper()
	checksum, err := skillTemplateManagedChecksum(tmpl)
	if err != nil {
		t.Fatalf("skillTemplateManagedChecksum: %v", err)
	}
	return checksum
}

func templateOwnedSkill(t *testing.T, id, slug string, managed SkillTemplateData) *service.Skill {
	t.Helper()
	return &service.Skill{
		ID:             id,
		Name:           managed.Name,
		SystemPrompt:   managed.SystemPrompt,
		Tools:          cloneTools(t, managed.Tools),
		SourceURL:      skillTemplateSourceURL(slug),
		SourceChecksum: checksumForSkillTemplate(t, managed),
	}
}

func btoi(value bool) int {
	if value {
		return 1
	}
	return 0
}

func cloneTools(t *testing.T, tools []service.Tool) []service.Tool {
	t.Helper()
	data, err := json.Marshal(tools)
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	var cloned []service.Tool
	if err := json.Unmarshal(data, &cloned); err != nil {
		t.Fatalf("unmarshal tools: %v", err)
	}
	return cloned
}

// TestSeriesProductionTemplates pins the tool contracts the video-series pack
// and Studio UI depend on.
func TestSeriesProductionTemplates(t *testing.T) {
	tests := []struct {
		file  string
		skill string
		tools []string
	}{
		{"series-library.json", "series_library", []string{
			"series_create", "series_get", "series_list", "series_update",
			"episode_create", "episode_get", "episode_update", "shots_set", "shot_update",
		}},
		{"fal-cinema.json", "fal_cinema", []string{
			"scene_video", "scene_video_veo", "scene_video_long", "scene_video_budget",
			"continuity_video", "continuity_video_budget", "transition_video",
			"extract_last_frame", "sora_create_character", "sora_scene_video",
		}},
		{"ltx-video.json", "ltx_video", []string{"scene_video_ltx25"}},
		{"fal-avatar.json", "fal_avatar", []string{
			"create_avatar", "character_sheet", "update_character", "save_avatar", "list_avatars",
		}},
		{"fal-image.json", "fal_image", []string{"edit_image_nano_banana"}},
		{"video-composer.json", "video_composer", []string{"generate_subtitles", "burn_subtitles"}},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			data, err := skillTemplateFS.ReadFile("skill_templates/" + tt.file)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			var tmpl SkillTemplate
			if err := json.Unmarshal(data, &tmpl); err != nil {
				t.Fatalf("parse: %v", err)
			}
			if tmpl.Skill.Name != tt.skill {
				t.Fatalf("skill name = %q, want %q", tmpl.Skill.Name, tt.skill)
			}
			checksum := checksumForSkillTemplate(t, tmpl.Skill)
			if len(checksum) != sha256.Size*2 || checksum != checksumForSkillTemplate(t, tmpl.Skill) {
				t.Fatalf("managed checksum is not stable SHA-256: %q", checksum)
			}
			have := map[string]bool{}
			for _, tool := range tmpl.Skill.Tools {
				have[tool.Name] = true
			}
			for _, name := range tt.tools {
				if !have[name] {
					t.Errorf("missing tool %q", name)
				}
			}
		})
	}
}
