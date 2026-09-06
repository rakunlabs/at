package server

import (
	"io/fs"
	"slices"
	"strings"
	"testing"
)

func TestLongFormVideoPackLoads(t *testing.T) {
	embedded, err := fs.Sub(integrationPackFS, "integration_packs")
	if err != nil {
		t.Fatalf("sub fs: %v", err)
	}

	var s Server
	s.loadPacksFromFS(embedded, true, "embedded", "", "")
	i := slices.IndexFunc(s.integrationPacks, func(p IntegrationPack) bool { return p.Slug == "long-form-video" })
	if i < 0 {
		t.Fatal("long-form-video pack not discovered")
	}
	pack := s.integrationPacks[i]
	if pack.Name != "Long Video Studio" || !pack.ReadOnly || pack.Source != "embedded" {
		t.Fatalf("unexpected pack metadata: %+v", pack)
	}
	if len(pack.Components.Agents) != 1 {
		t.Fatalf("agents: got %d, want 1", len(pack.Components.Agents))
	}
	agent := pack.Components.Agents[0]
	if agent.Name != "Long Video Producer" || agent.Config.Provider != "" || agent.Config.Model != "" {
		t.Errorf("unexpected agent identity/provider/model: %+v", agent)
	}
	wantSkills := []string{"pexels_images", "fal_image", "fal_cinema", "elevenlabs_voice", "openai_tts", "video_composer", "ffmpeg_guide"}
	if len(agent.Config.Skills) != len(wantSkills) {
		t.Errorf("skill count: got %d, want %d", len(agent.Config.Skills), len(wantSkills))
	}
	s.loadSkillTemplates()
	for _, name := range wantSkills {
		found := false
		for _, ref := range agent.Config.Skills {
			if ref.ID == name {
				found = true
			}
		}
		if !found {
			t.Errorf("missing skill ref %q", name)
		}
		if !slices.ContainsFunc(s.skillTemplates, func(tmpl SkillTemplate) bool { return tmpl.Skill.Name == name }) {
			t.Errorf("skill ref %q has no embedded template", name)
		}
	}
	for _, ref := range agent.Config.Skills {
		if ref.ID == "series_library" {
			t.Error("standalone pack must not depend on series_library")
		}
	}
	for _, tool := range []string{"bash_execute", "file_list", "file_read"} {
		if !slices.Contains(agent.Config.BuiltinTools, tool) {
			t.Errorf("missing builtin tool %q", tool)
		}
	}
	for _, contract := range []string{
		"${assetsRoot}/videos/<id>/brief.json",
		"id, title, topic, content_brief, audience, language, duration_minutes, aspect_ratio, visual_style, outline",
		"submission.json containing {task_id}",
		"MUST NOT edit brief.json or submission.json",
		"video.json", "os.replace", "final_video", "duration_s", "updated_at",
		"actual installed tool descriptions and input schemas", "status=blocked", "status=failed",
		"Do NOT request one provider long clip", "ffprobe", "status=completed",
	} {
		if !strings.Contains(agent.Config.SystemPrompt, contract) {
			t.Errorf("prompt missing contract %q", contract)
		}
	}
	org := pack.Components.Organization
	if org == nil {
		t.Fatal("organization.json missing")
	}
	if org.Name != "Long Video Studio" || len(org.Relationships) != 1 {
		t.Fatalf("unexpected organization: %+v", org)
	}
	rel := org.Relationships[0]
	if rel.AgentName != agent.Name || rel.Role != "head" || !rel.IsHead || rel.ParentAgentName != "" {
		t.Errorf("unexpected head relationship: %+v", rel)
	}
}
