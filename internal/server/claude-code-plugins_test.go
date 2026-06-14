package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

func TestClaudeCodeMarketplaceAPI_PublicMCPServersOnly(t *testing.T) {
	mcpServers := newFakeMCPServerStore()
	mcpServers.servers["public"] = &service.MCPServer{
		ID:          "public",
		Name:        "Public Tools",
		Description: "Shared writing tools",
		Public:      true,
		Config: service.MCPServerConfig{
			EnabledSkills: []string{"writer"},
		},
	}
	mcpServers.servers["private"] = &service.MCPServer{
		ID:     "private",
		Name:   "Private Tools",
		Public: false,
	}
	skills := newFakeSkillStore()
	skills.skills["writer-id"] = &service.Skill{
		ID:           "writer-id",
		Name:         "writer",
		Description:  "Write better copy",
		SystemPrompt: "Improve writing.",
	}
	s := &Server{mcpServerStore: mcpServers, skillStore: skills}

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
	if got.Name != "at-mcp-servers" {
		t.Fatalf("marketplace name = %q, want %q", got.Name, "at-mcp-servers")
	}
	if len(got.Plugins) != 1 {
		t.Fatalf("plugins len = %d, want 1: %#v", len(got.Plugins), got.Plugins)
	}
	plugin := got.Plugins[0]
	if plugin.Name != "public-tools" {
		t.Fatalf("plugin name = %q, want %q", plugin.Name, "public-tools")
	}
	wantSource := "https://at.example/gateway/v1/claude-code/plugins/Public%20Tools/plugin.zip"
	if plugin.Source != wantSource {
		t.Fatalf("plugin source = %q, want %q", plugin.Source, wantSource)
	}
	if strings.Contains(rr.Body.String(), "Private Tools") {
		t.Fatalf("private mcp server leaked into marketplace: %s", rr.Body.String())
	}
}

func TestClaudeCodeMarketplaceAPI_MarketFilterIncludesSelectedPublicMCPServers(t *testing.T) {
	skills := newFakeSkillStore()
	skills.skills["writer-id"] = &service.Skill{ID: "writer-id", Name: "writer", Description: "Write better copy"}
	mcpServers := newFakeMCPServerStore()
	mcpServers.servers["mcp-public"] = &service.MCPServer{ID: "mcp-public", Name: "Public MCP", Public: true, Description: "Shared MCP tools"}
	mcpServers.servers["mcp-private"] = &service.MCPServer{ID: "mcp-private", Name: "Private MCP", Public: false}
	mcpServers.servers["mcp-other"] = &service.MCPServer{ID: "mcp-other", Name: "Other MCP", Public: true}
	markets := newFakeMarketplaceStore()
	markets.markets["market"] = &service.Marketplace{
		ID:          "market",
		Name:        "my-market",
		Description: "Selected tools",
		Skills:      []string{"writer-id"},
		MCPServers:  []string{"mcp-public", "mcp-private"},
	}
	s := &Server{skillStore: skills, mcpServerStore: mcpServers, marketplaceStore: markets}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://at.example/gateway/v1/claude-code/marketplace.json?market=my-market", nil)
	s.ClaudeCodeMarketplaceAPI(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var got claudeMarketplaceFile
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.Name != "my-market" {
		t.Fatalf("marketplace name = %q, want my-market", got.Name)
	}
	if len(got.Plugins) != 1 {
		t.Fatalf("plugins len = %d, want 1: %#v", len(got.Plugins), got.Plugins)
	}
	if got.Plugins[0].Name != "my-market" {
		t.Fatalf("plugin name = %q, want my-market", got.Plugins[0].Name)
	}
	if strings.Contains(rr.Body.String(), "Private MCP") || strings.Contains(rr.Body.String(), "Other MCP") {
		t.Fatalf("marketplace leaked unselected/private plugins: %s", rr.Body.String())
	}
	wantSource := "https://at.example/gateway/v1/claude-code/marketplaces/my-market/plugin.zip"
	if got.Plugins[0].Source != wantSource {
		t.Fatalf("plugin source = %q, want %q", got.Plugins[0].Source, wantSource)
	}
}

func TestClaudeCodeMarketplacePluginZip_ContainsSkillsAndMCPConfigs(t *testing.T) {
	skills := newFakeSkillStore()
	skills.skills["writer-id"] = &service.Skill{ID: "writer-id", Name: "writer", Description: "Write better copy", SystemPrompt: "Improve writing."}
	mcpServers := newFakeMCPServerStore()
	mcpServers.servers["mcp-public"] = &service.MCPServer{ID: "mcp-public", Name: "Public MCP", Public: true}
	mcpServers.servers["mcp-private"] = &service.MCPServer{ID: "mcp-private", Name: "Private MCP", Public: false}
	markets := newFakeMarketplaceStore()
	markets.markets["market"] = &service.Marketplace{
		ID:          "market",
		Name:        "my-market",
		Description: "Selected tools",
		Skills:      []string{"writer-id"},
		MCPServers:  []string{"mcp-public", "mcp-private"},
		DirectMCPServers: []service.MarketplaceMCPServer{
			{Name: "upstream-docs", Type: "http", URL: "https://docs.example/mcp"},
			{Name: "local-search", Type: "stdio", Command: "npx", Args: []string{"-y", "local-search"}},
		},
	}
	s := &Server{skillStore: skills, mcpServerStore: mcpServers, marketplaceStore: markets}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://at.example/gateway/v1/claude-code/marketplaces/my-market/plugin.zip", nil)
	req.SetPathValue("name", "my-market")
	s.ClaudeCodeMarketplacePluginZipAPI(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	files := readZipEntries(t, rr.Body.Bytes())
	assertZipContains(t, files, ".claude-plugin/plugin.json")
	assertZipContains(t, files, "skills/writer/SKILL.md")

	var manifest claudePluginManifest
	if err := json.Unmarshal(files[".claude-plugin/plugin.json"], &manifest); err != nil {
		t.Fatalf("unmarshal plugin manifest: %v", err)
	}
	if _, ok := manifest.MCPServers["at-public-mcp"]; !ok {
		t.Fatalf("missing public AT MCP server in manifest: %#v", manifest.MCPServers)
	}
	if _, ok := manifest.MCPServers["private-mcp"]; ok {
		t.Fatalf("private MCP server leaked into manifest: %#v", manifest.MCPServers)
	}
	if got := manifest.MCPServers["upstream-docs"]; got.URL != "https://docs.example/mcp" || got.Type != "http" {
		t.Fatalf("direct http MCP config = %#v", got)
	}
	if got := manifest.MCPServers["local-search"]; got.Command != "npx" || strings.Join(got.Args, " ") != "-y local-search" || got.Type != "stdio" {
		t.Fatalf("direct stdio MCP config = %#v", got)
	}

	skillMD := string(files["skills/writer/SKILL.md"])
	if !strings.Contains(skillMD, "Improve writing.") {
		t.Fatalf("skill md missing system prompt: %s", skillMD)
	}
}

func TestClaudeCodeMarketplaceZip_ContainsPluginSkillAndMCP(t *testing.T) {
	mcpServers := newFakeMCPServerStore()
	mcpServers.servers["public"] = &service.MCPServer{
		ID:          "public",
		Name:        "Public Tools",
		Description: "Shared writing tools",
		Public:      true,
		Config: service.MCPServerConfig{
			EnabledSkills: []string{"writer"},
		},
	}
	skills := newFakeSkillStore()
	skills.skills["writer-id"] = &service.Skill{
		ID:           "writer-id",
		Name:         "writer",
		Description:  "Write better copy",
		SystemPrompt: "Improve writing.",
	}
	s := &Server{mcpServerStore: mcpServers, skillStore: skills}

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
	if mcp.URL != "https://at.example/gateway/v1/mcp/Public%20Tools" {
		t.Fatalf("mcp url = %q", mcp.URL)
	}

	skillMD := string(files["plugins/public-tools/skills/writer/SKILL.md"])
	if !strings.Contains(skillMD, "Improve writing.") {
		t.Fatalf("skill md missing system prompt: %s", skillMD)
	}
}

func TestClaudeCodePluginZip_PrivateMCPServerNotFound(t *testing.T) {
	mcpServers := newFakeMCPServerStore()
	mcpServers.servers["private"] = &service.MCPServer{ID: "private", Name: "private", Public: false}
	s := &Server{mcpServerStore: mcpServers}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://at.example/gateway/v1/claude-code/plugins/private/plugin.zip", nil)
	req.SetPathValue("name", "private")
	s.ClaudeCodePluginZipAPI(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusNotFound, rr.Body.String())
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

type fakeMarketplaceStore struct {
	markets map[string]*service.Marketplace
}

func newFakeMarketplaceStore() *fakeMarketplaceStore {
	return &fakeMarketplaceStore{markets: map[string]*service.Marketplace{}}
}

func (f *fakeMarketplaceStore) ListMarketplaces(_ context.Context, _ *query.Query) (*service.ListResult[service.Marketplace], error) {
	out := make([]service.Marketplace, 0, len(f.markets))
	for _, market := range f.markets {
		out = append(out, *market)
	}
	return &service.ListResult[service.Marketplace]{Data: out}, nil
}

func (f *fakeMarketplaceStore) GetMarketplace(_ context.Context, id string) (*service.Marketplace, error) {
	return f.markets[id], nil
}

func (f *fakeMarketplaceStore) GetMarketplaceByName(_ context.Context, name string) (*service.Marketplace, error) {
	for _, market := range f.markets {
		if market.Name == name {
			return market, nil
		}
	}
	return nil, nil
}

func (f *fakeMarketplaceStore) CreateMarketplace(_ context.Context, market service.Marketplace) (*service.Marketplace, error) {
	if market.ID == "" {
		market.ID = market.Name
	}
	f.markets[market.ID] = &market
	return &market, nil
}

func (f *fakeMarketplaceStore) UpdateMarketplace(_ context.Context, id string, market service.Marketplace) (*service.Marketplace, error) {
	if _, ok := f.markets[id]; !ok {
		return nil, nil
	}
	market.ID = id
	f.markets[id] = &market
	return &market, nil
}

func (f *fakeMarketplaceStore) DeleteMarketplace(_ context.Context, id string) error {
	delete(f.markets, id)
	return nil
}
