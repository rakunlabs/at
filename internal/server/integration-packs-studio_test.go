package server

import (
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

var studioPackSlugs = []string{"avatar-studio", "video-series", "long-form-video"}

// TestStudioPacksAreProviderNeutral keeps the Studio teams independent of any
// generation vendor: generation tools come from the Studio toolkit, so the
// packs may reference only the embedded, local studio skills.
func TestStudioPacksAreProviderNeutral(t *testing.T) {
	embedded, err := fs.Sub(integrationPackFS, "integration_packs")
	if err != nil {
		t.Fatalf("sub fs: %v", err)
	}
	var s Server
	s.loadSkillTemplates()
	vendor := regexp.MustCompile(`(?i)\b(fal|elevenlabs|ltx|kling|sora|omnihuman|nano.banana|pexels|seedance|vidu)\b|_api_key`)

	for _, slug := range studioPackSlugs {
		t.Run(slug, func(t *testing.T) {
			pack, err := loadPackFolder(embedded, slug, true)
			if err != nil {
				t.Fatalf("loadPackFolder: %v", err)
			}
			raw, err := fs.ReadFile(embedded, slug+"/pack.json")
			if err != nil {
				t.Fatalf("read pack.json: %v", err)
			}
			if m := vendor.FindString(string(raw)); m != "" {
				t.Errorf("pack.json names vendor %q", m)
			}
			for _, agent := range pack.Components.Agents {
				if m := vendor.FindString(agent.Config.SystemPrompt + agent.Config.Description); m != "" {
					t.Errorf("%s names vendor %q", agent.Name, m)
				}
				for _, ref := range agent.Config.Skills {
					if !slices.ContainsFunc(s.skillTemplates, func(tmpl SkillTemplate) bool { return tmpl.Skill.Name == ref.ID }) {
						t.Errorf("%s references skill %q with no embedded template", agent.Name, ref.ID)
					}
				}
				if len(agent.Config.Skills) > 0 && !slices.ContainsFunc(agent.Config.Skills, func(ref service.SkillRef) bool { return ref.ID == "studio_library" }) {
					t.Errorf("%s has skills but not studio_library, so it cannot receive the toolkit", agent.Name)
				}
				if len(agent.Config.Skills) > 0 && !strings.Contains(agent.Config.SystemPrompt, "studio_toolkit") &&
					!slices.ContainsFunc(agent.Config.Skills, func(ref service.SkillRef) bool { return ref.ID == "series_library" }) {
					t.Errorf("%s does not tell the model to read studio_toolkit", agent.Name)
				}
			}
		})
	}
}

// TestStudioLibraryHandlers runs the studio_library handlers against a
// temporary asset root: toolkit read, character save/reference/update and
// voice registration.
func TestStudioLibraryHandlers(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	data, err := skillTemplateFS.ReadFile("skill_templates/studio-library.json")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var tmpl SkillTemplate
	if err := json.Unmarshal(data, &tmpl); err != nil {
		t.Fatalf("parse: %v", err)
	}
	handlers := map[string]string{}
	for _, tool := range tmpl.Skill.Tools {
		handlers[tool.Name] = tool.Handler
	}

	assets := t.TempDir()
	work := t.TempDir()
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")
	portrait := filepath.Join(work, "portrait.png")
	if err := os.WriteFile(portrait, png, 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(name string, args map[string]string) map[string]any {
		t.Helper()
		handler, ok := handlers[name]
		if !ok {
			t.Fatalf("tool %q missing", name)
		}
		cmd := exec.Command("bash", "-c", handler)
		cmd.Env = append(os.Environ(), "AT_ASSETS_DIR="+assets, "AT_WORK_DIR="+work)
		for k, v := range args {
			cmd.Env = append(cmd.Env, "ARG_"+strings.ToUpper(k)+"="+v)
		}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var result map[string]any
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatalf("%s: invalid JSON %q", name, out)
		}
		return result
	}

	toolkit := run("studio_toolkit", nil)
	if missing, _ := toolkit["unconfigured"].([]any); len(missing) != 5 {
		t.Fatalf("empty toolkit should report every capability unconfigured: %v", toolkit)
	}
	studio := `{"capabilities":{"video":{"mcp_sets":["my-video"],"notes":"prefer finals"}}}`
	if err := os.WriteFile(filepath.Join(assets, "studio.json"), []byte(studio), 0o600); err != nil {
		t.Fatal(err)
	}
	toolkit = run("studio_toolkit", nil)
	video := toolkit["capabilities"].(map[string]any)["video"].(map[string]any)
	if video["configured"] != true || video["notes"] != "prefer finals" {
		t.Fatalf("video capability not read: %v", video)
	}

	if got := run("character_get", map[string]string{"name": "Maya"}); got["error"] == nil {
		t.Fatalf("unknown character should fail: %v", got)
	}
	run("character_save", map[string]string{"name": "Maya Chen", "image": portrait})
	run("character_add_reference", map[string]string{"name": "maya chen", "image": portrait, "label": "Profile"})
	run("character_add_reference", map[string]string{"name": "maya-chen", "image": portrait, "label": "profile"})
	run("voice_save", map[string]string{"name": "Maya Voice", "voice_id": "v-1", "provider": "any-tts"})
	run("character_update", map[string]string{"name": "maya-chen", "voice": "maya voice", "wardrobe": "red coat"})

	got := run("character_get", map[string]string{"name": "Maya Chen"})["character"].(map[string]any)
	if got["voice"] != "maya-voice" || got["wardrobe"] != "red coat" {
		t.Fatalf("bible not updated: %v", got)
	}
	refs, _ := got["reference_files"].([]any)
	if len(refs) != 2 {
		t.Fatalf("want portrait + one profile reference, got %v", refs)
	}
	for _, ref := range refs {
		if !strings.HasPrefix(ref.(string), filepath.Join(assets, "avatars")) {
			t.Errorf("reference outside the library: %v", ref)
		}
	}
}
