package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestClaudeCodeMarketplaceAPI_PublicServersOnly(t *testing.T) {
	skillServers := newFakeSkillServerStore()
	skillServers.servers["public"] = &service.SkillServer{
		ID:          "public",
		Name:        "Public Tools",
		Description: "Shared writing tools",
		Public:      true,
		Mode:        service.SkillServerModeBoth,
		Skills:      []string{"writer"},
	}
	skillServers.servers["private"] = &service.SkillServer{
		ID:     "private",
		Name:   "Private Tools",
		Public: false,
		Skills: []string{"secret"},
	}
	skills := newFakeSkillStore()
	skills.skills["writer-id"] = &service.Skill{
		ID:           "writer-id",
		Name:         "writer",
		Description:  "Write better copy",
		SystemPrompt: "Improve writing.",
	}
	s := &Server{skillServerStore: skillServers, skillStore: skills}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://at.example/gateway/v1/claude-code/marketplace.json", nil)
	s.ClaudeCodeMarketplaceAPI(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var got claudeMarketplaceFile
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.Name != "at-skill-servers" {
		t.Fatalf("marketplace name = %q, want %q", got.Name, "at-skill-servers")
	}
	if len(got.Plugins) != 1 {
		t.Fatalf("plugins len = %d, want 1: %#v", len(got.Plugins), got.Plugins)
	}
	plugin := got.Plugins[0]
	if plugin.Name != "public-tools" {
		t.Fatalf("plugin name = %q, want %q", plugin.Name, "public-tools")
	}
	if plugin.Source != "./plugins/public-tools" {
		t.Fatalf("plugin source = %q, want %q", plugin.Source, "./plugins/public-tools")
	}
	if strings.Contains(rr.Body.String(), "Private Tools") {
		t.Fatalf("private skill server leaked into marketplace: %s", rr.Body.String())
	}
}

func TestClaudeCodeMarketplaceZip_ContainsPluginSkillAndMCP(t *testing.T) {
	skillServers := newFakeSkillServerStore()
	skillServers.servers["public"] = &service.SkillServer{
		ID:          "public",
		Name:        "Public Tools",
		Description: "Shared writing tools",
		Public:      true,
		Mode:        service.SkillServerModeBoth,
		Skills:      []string{"writer"},
	}
	skills := newFakeSkillStore()
	skills.skills["writer-id"] = &service.Skill{
		ID:           "writer-id",
		Name:         "writer",
		Description:  "Write better copy",
		SystemPrompt: "Improve writing.",
	}
	s := &Server{skillServerStore: skillServers, skillStore: skills}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://at.example/gateway/v1/claude-code/marketplace.zip", nil)
	s.ClaudeCodeMarketplaceZipAPI(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("content-type = %q, want application/zip", ct)
	}

	files := readZipEntries(t, rr.Body.Bytes())
	assertZipContains(t, files, ".claude-plugin/marketplace.json")
	assertZipContains(t, files, "plugins/public-tools/.claude-plugin/plugin.json")
	assertZipContains(t, files, "plugins/public-tools/skills/writer/SKILL.md")

	var manifest claudePluginManifest
	if err := json.Unmarshal(files["plugins/public-tools/.claude-plugin/plugin.json"], &manifest); err != nil {
		t.Fatalf("unmarshal plugin manifest: %v", err)
	}
	mcp, ok := manifest.MCPServers["at-public-tools"]
	if !ok {
		t.Fatalf("missing MCP server at-public-tools in manifest: %#v", manifest.MCPServers)
	}
	if mcp.URL != "https://at.example/gateway/v1/skill-servers/Public%20Tools/mcp" {
		t.Fatalf("mcp url = %q", mcp.URL)
	}

	skillMD := string(files["plugins/public-tools/skills/writer/SKILL.md"])
	if !strings.Contains(skillMD, "Improve writing.") {
		t.Fatalf("skill md missing system prompt: %s", skillMD)
	}
	if !strings.Contains(skillMD, "at-public-tools") {
		t.Fatalf("skill md missing MCP server name: %s", skillMD)
	}
}

func TestClaudeCodePluginZip_PrivateServerNotFound(t *testing.T) {
	skillServers := newFakeSkillServerStore()
	skillServers.servers["private"] = &service.SkillServer{ID: "private", Name: "private", Public: false}
	s := &Server{skillServerStore: skillServers}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://at.example/gateway/v1/claude-code/plugins/private/plugin.zip", nil)
	req.SetPathValue("name", "private")
	s.ClaudeCodePluginZipAPI(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusNotFound, rr.Body.String())
	}
}

func TestPublicSkillHubAPI_ReturnsPublicSkillMetadata(t *testing.T) {
	skillServers := newFakeSkillServerStore()
	skillServers.servers["public"] = &service.SkillServer{
		ID:     "public",
		Name:   "public",
		Public: true,
		Mode:   service.SkillServerModePackage,
		Skills: []string{"writer"},
	}
	skillServers.servers["private"] = &service.SkillServer{ID: "private", Name: "private", Public: false}
	skills := newFakeSkillStore()
	skills.skills["writer-id"] = &service.Skill{ID: "writer-id", Name: "writer", Description: "Write better copy"}
	s := &Server{skillServerStore: skillServers, skillStore: skills}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://at.example/gateway/v1/public/skill_hub", nil)
	s.PublicSkillHubAPI(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
	var got publicSkillHubResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(got.Servers) != 1 || got.Servers[0].Name != "public" {
		t.Fatalf("servers = %#v, want only public", got.Servers)
	}
	if len(got.Skills) != 1 || got.Skills[0].Name != "writer" {
		t.Fatalf("skills = %#v, want writer", got.Skills)
	}
	if got.Servers[0].PluginURL != "https://at.example/gateway/v1/claude-code/plugins/public/plugin.zip" {
		t.Fatalf("plugin url = %q", got.Servers[0].PluginURL)
	}
	if strings.Contains(rr.Body.String(), "private") {
		t.Fatalf("private skill server leaked into hub: %s", rr.Body.String())
	}
}

func readZipEntries(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open zip file %s: %v", f.Name, err)
		}
		buf := new(bytes.Buffer)
		if _, err := buf.ReadFrom(rc); err != nil {
			rc.Close()
			t.Fatalf("read zip file %s: %v", f.Name, err)
		}
		if err := rc.Close(); err != nil {
			t.Fatalf("close zip file %s: %v", f.Name, err)
		}
		out[f.Name] = buf.Bytes()
	}
	return out
}

func assertZipContains(t *testing.T, files map[string][]byte, name string) {
	t.Helper()
	if _, ok := files[name]; !ok {
		t.Fatalf("zip missing %s; files: %#v", name, files)
	}
}
