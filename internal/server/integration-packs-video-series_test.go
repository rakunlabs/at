package server

import (
	"io/fs"
	"testing"
)

// TestVideoSeriesPackLoads guards the embedded video-series integration pack:
// it must parse, carry all five agents with their skill references, and
// define the org hierarchy with Showrunner as head.
func TestVideoSeriesPackLoads(t *testing.T) {
	embedded, err := fs.Sub(integrationPackFS, "integration_packs")
	if err != nil {
		t.Fatalf("sub fs: %v", err)
	}

	pack, err := loadPackFolder(embedded, "video-series", true)
	if err != nil {
		t.Fatalf("loadPackFolder: %v", err)
	}

	if pack.Slug != "video-series" {
		t.Errorf("slug: got %q", pack.Slug)
	}
	if len(pack.Components.Agents) != 5 {
		t.Fatalf("agents: got %d, want 5", len(pack.Components.Agents))
	}

	byName := map[string]IntegrationAgent{}
	for _, a := range pack.Components.Agents {
		byName[a.Name] = a
	}

	wantAgentSkills := map[string][]string{
		"Showrunner":         {},
		"Script Writer":      {"series_library"},
		"Character Designer": {"fal_avatar", "elevenlabs_voice", "series_library"},
		"Scene Director":     {"fal_cinema", "ltx_video", "fal_image", "series_library"},
		"Episode Editor":     {"video_composer", "elevenlabs_voice", "series_library"},
	}
	for name, wantSkills := range wantAgentSkills {
		agent, ok := byName[name]
		if !ok {
			t.Errorf("agent %q missing", name)
			continue
		}
		have := map[string]bool{}
		for _, s := range agent.Config.Skills {
			have[s.ID] = true
		}
		for _, sk := range wantSkills {
			if !have[sk] {
				t.Errorf("%s missing skill ref %q", name, sk)
			}
		}
	}

	org := pack.Components.Organization
	if org == nil {
		t.Fatal("organization.json missing")
	}
	if org.Name != "Series Studio" {
		t.Errorf("org name: got %q", org.Name)
	}
	var headCount int
	for _, rel := range org.Relationships {
		if rel.IsHead {
			headCount++
			if rel.AgentName != "Showrunner" {
				t.Errorf("head agent: got %q", rel.AgentName)
			}
		} else if rel.ParentAgentName != "Showrunner" {
			t.Errorf("agent %q parent: got %q, want Showrunner", rel.AgentName, rel.ParentAgentName)
		}
	}
	if headCount != 1 {
		t.Errorf("head count: got %d, want 1", headCount)
	}
	if len(org.Relationships) != 5 {
		t.Errorf("relationships: got %d, want 5", len(org.Relationships))
	}
}
