package server

import (
	"io/fs"
	"testing"
)

// TestAvatarStudioPackLoads guards the embedded avatar-studio integration
// pack: it must parse, carry all three agents with their skill references,
// and define the org hierarchy with Studio Director as head.
func TestAvatarStudioPackLoads(t *testing.T) {
	embedded, err := fs.Sub(integrationPackFS, "integration_packs")
	if err != nil {
		t.Fatalf("sub fs: %v", err)
	}

	pack, err := loadPackFolder(embedded, "avatar-studio", true)
	if err != nil {
		t.Fatalf("loadPackFolder: %v", err)
	}

	if pack.Slug != "avatar-studio" {
		t.Errorf("slug: got %q", pack.Slug)
	}
	if len(pack.Components.Agents) != 3 {
		t.Fatalf("agents: got %d, want 3", len(pack.Components.Agents))
	}

	byName := map[string]IntegrationAgent{}
	for _, a := range pack.Components.Agents {
		byName[a.Name] = a
	}
	producer, ok := byName["Video Producer"]
	if !ok {
		t.Fatalf("Video Producer agent missing; have %v", func() []string {
			names := make([]string, 0, len(byName))
			for n := range byName {
				names = append(names, n)
			}
			return names
		}())
	}
	wantSkills := map[string]bool{"fal_avatar": false, "elevenlabs_voice": false}
	for _, s := range producer.Config.Skills {
		if _, ok := wantSkills[s.ID]; ok {
			wantSkills[s.ID] = true
		}
	}
	for name, found := range wantSkills {
		if !found {
			t.Errorf("Video Producer missing skill ref %q", name)
		}
	}

	org := pack.Components.Organization
	if org == nil {
		t.Fatal("organization.json missing")
	}
	if org.Name != "Avatar Studio" {
		t.Errorf("org name: got %q", org.Name)
	}
	var headCount int
	for _, rel := range org.Relationships {
		if rel.IsHead {
			headCount++
			if rel.AgentName != "Studio Director" {
				t.Errorf("head agent: got %q", rel.AgentName)
			}
		}
	}
	if headCount != 1 {
		t.Errorf("head count: got %d, want 1", headCount)
	}
}
