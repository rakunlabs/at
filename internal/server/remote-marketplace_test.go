package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

type fakeMarketplaceSourceStore struct {
	sources map[string]*service.MarketplaceSource
}

func (f *fakeMarketplaceSourceStore) ListMarketplaceSources(context.Context) ([]service.MarketplaceSource, error) {
	out := []service.MarketplaceSource{}
	for _, src := range f.sources {
		out = append(out, *src)
	}
	return out, nil
}

func (f *fakeMarketplaceSourceStore) GetMarketplaceSource(_ context.Context, id string) (*service.MarketplaceSource, error) {
	if src, ok := f.sources[id]; ok {
		copy := *src
		return &copy, nil
	}
	return nil, nil
}

func (f *fakeMarketplaceSourceStore) CreateMarketplaceSource(_ context.Context, src service.MarketplaceSource) (*service.MarketplaceSource, error) {
	src.ID = "src-" + src.Name
	f.sources[src.ID] = &src
	return &src, nil
}

func (f *fakeMarketplaceSourceStore) UpdateMarketplaceSource(_ context.Context, id string, src service.MarketplaceSource) (*service.MarketplaceSource, error) {
	src.ID = id
	f.sources[id] = &src
	return &src, nil
}

func (f *fakeMarketplaceSourceStore) DeleteMarketplaceSource(_ context.Context, id string) error {
	delete(f.sources, id)
	return nil
}

// newMarketplaceRepo builds a git marketplace with one relative plugin holding
// two skills, an HTTP MCP server with a placeholder and a stdio server.
func newMarketplaceRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeTestFile(t, filepath.Join(repo, ".claude-plugin", "marketplace.json"), `{
  "name": "team-tools",
  "owner": {"name": "Team"},
  "plugins": [
    {"name": "writing", "source": "./plugins/writing", "description": "Writing helpers"},
    {"name": "remote-npm", "source": {"source": "npm", "package": "x"}}
  ]
}`)
	writeTestFile(t, filepath.Join(repo, "plugins", "writing", ".claude-plugin", "plugin.json"), `{"name": "writing"}`)
	writeTestFile(t, filepath.Join(repo, "plugins", "writing", "skills", "editor", "SKILL.md"), "---\nname: editor\ndescription: Edits copy\n---\nEdit carefully.\n")
	writeTestFile(t, filepath.Join(repo, "plugins", "writing", "skills", "editor", "references", "style.md"), "Style guide\n")
	writeTestFile(t, filepath.Join(repo, "plugins", "writing", "skills", "titles", "SKILL.md"), "---\nname: titles\n---\nWrite titles.\n")
	writeTestFile(t, filepath.Join(repo, "plugins", "writing", ".mcp.json"), `{"mcpServers": {
  "docs": {"type": "http", "url": "https://docs.example/mcp", "headers": {"Authorization": "Bearer ${DOCS_TOKEN}"}},
  "local": {"command": "npx", "args": ["-y", "docs-mcp@1"], "env": {"REGION": "${REGION:-eu}"}},
  "bundled": {"command": "${CLAUDE_PLUGIN_ROOT}/bin/server"}
}}`)
	commitAll(t, repo)
	return repo
}

func commitAll(t *testing.T, repo string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		runGit(t, repo, "init", "-b", "main")
		runGit(t, repo, "config", "user.email", "test@example.com")
		runGit(t, repo, "config", "user.name", "Test")
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "update")
}

// gitSource stores a source pointing at a local test repository, bypassing
// normalizeMarketplaceSource (which refuses local paths by design).
func newRemoteMarketplaceServer(t *testing.T, src service.MarketplaceSource) (*Server, *fakeSkillStore, *fakeMCPSetStore) {
	t.Helper()
	skills := newFakeSkillStore()
	sets := newFakeMCPSetStore()
	sources := &fakeMarketplaceSourceStore{sources: map[string]*service.MarketplaceSource{src.ID: &src}}
	return &Server{skillStore: skills, mcpSetStore: sets, marketplaceSourceStore: sources}, skills, sets
}

func callMarketplace(t *testing.T, handler http.HandlerFunc, method, target string, body any, values map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, reader)
	req = req.WithContext(service.WithLegacyWorkspaceAccess(req.Context()))
	for k, v := range values {
		req.SetPathValue(k, v)
	}
	rr := httptest.NewRecorder()
	handler(rr, req)
	return rr
}

func TestRemoteMarketplaceGitCatalogPluginAndInstall(t *testing.T) {
	repo := newMarketplaceRepo(t)
	s, skills, sets := newRemoteMarketplaceServer(t, service.MarketplaceSource{ID: "team", Name: "team", Type: service.MarketplaceSourceGit, URL: repo, Enabled: true})

	rr := callMarketplace(t, s.MarketplaceSourceCatalogAPI, http.MethodGet, "/", nil, map[string]string{"id": "team"})
	if rr.Code != http.StatusOK {
		t.Fatalf("catalog status %d: %s", rr.Code, rr.Body)
	}
	var cat RemoteMarketplace
	_ = json.Unmarshal(rr.Body.Bytes(), &cat)
	if cat.Name != "team-tools" || len(cat.Plugins) != 2 {
		t.Fatalf("catalog = %+v", cat)
	}
	for _, p := range cat.Plugins {
		if p.Name == "remote-npm" && p.Installable {
			t.Fatal("npm plugin reported installable")
		}
	}

	rr = callMarketplace(t, s.MarketplacePluginAPI, http.MethodGet, "/", nil, map[string]string{"id": "team", "plugin": "writing"})
	if rr.Code != http.StatusOK {
		t.Fatalf("plugin status %d: %s", rr.Code, rr.Body)
	}
	var detail RemotePluginDetail
	_ = json.Unmarshal(rr.Body.Bytes(), &detail)
	if len(detail.Skills) != 2 || detail.Skills[0].Path != "skills/editor" || detail.Skills[0].Resources != 1 {
		t.Fatalf("skills = %+v", detail.Skills)
	}
	if len(detail.MCPServers) != 3 {
		t.Fatalf("mcp servers = %+v", detail.MCPServers)
	}
	byName := map[string]RemotePluginMCP{}
	for _, m := range detail.MCPServers {
		byName[m.Name] = m
	}
	if byName["docs"].Type != "http" || strings.Join(byName["docs"].Variables, ",") != "DOCS_TOKEN" {
		t.Fatalf("docs = %+v", byName["docs"])
	}
	if !byName["bundled"].PluginRelative {
		t.Fatal("bundled server not marked plugin-relative")
	}

	// stdio requires explicit confirmation.
	rr = callMarketplace(t, s.MarketplaceInstallAPI, http.MethodPost, "/", marketplaceInstallRequest{Plugin: "writing", MCPServers: []string{"local"}}, map[string]string{"id": "team"})
	if rr.Code != http.StatusBadRequest || len(sets.created) != 0 {
		t.Fatalf("stdio without confirmation: %d %s", rr.Code, rr.Body)
	}
	// Plugin-bundled executables are refused even when confirmed.
	rr = callMarketplace(t, s.MarketplaceInstallAPI, http.MethodPost, "/", marketplaceInstallRequest{Plugin: "writing", MCPServers: []string{"bundled"}, AllowStdio: true}, map[string]string{"id": "team"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("plugin-relative server: %d %s", rr.Code, rr.Body)
	}

	rr = callMarketplace(t, s.MarketplaceInstallAPI, http.MethodPost, "/", marketplaceInstallRequest{
		Plugin: "writing", Skills: []string{"skills/editor"}, MCPServers: []string{"docs", "local"},
		Variables: map[string]string{"DOCS_TOKEN": "var:docs_token"}, AllowStdio: true, MCPScope: "workspace",
	}, map[string]string{"id": "team"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("install status %d: %s", rr.Code, rr.Body)
	}
	if len(skills.created) != 1 {
		t.Fatalf("skills created = %d", len(skills.created))
	}
	sk := skills.created[0]
	if sk.Name != "editor" || sk.SourceType != SkillSourceMarketplace || sk.SourceURL != "team" || sk.SourceRef != "writing" || sk.SourcePath != "skills/editor" || sk.SourceChecksum == "" {
		t.Fatalf("skill provenance = %+v", sk)
	}
	if len(sets.created) != 1 || sets.created[0].Name != "writing" {
		t.Fatalf("sets = %+v", sets.created)
	}
	ups := sets.created[0].Config.MCPUpstreams
	if len(ups) != 2 {
		t.Fatalf("upstreams = %+v", ups)
	}
	if ups[0].Headers["Authorization"] != "Bearer {{var:docs_token}}" || ups[0].Origin == nil || ups[0].Origin.Server != "docs" {
		t.Fatalf("docs upstream = %+v", ups[0])
	}
	if ups[1].Command != "npx" || ups[1].Env["REGION"] != "eu" {
		t.Fatalf("local upstream = %+v", ups[1])
	}

	// Installing the same skill twice is refused.
	rr = callMarketplace(t, s.MarketplaceInstallAPI, http.MethodPost, "/", marketplaceInstallRequest{Plugin: "writing", Skills: []string{"skills/editor"}}, map[string]string{"id": "team"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("duplicate skill: %d %s", rr.Code, rr.Body)
	}

	// Upstream change: the skill and the MCP server both report updates.
	writeTestFile(t, filepath.Join(repo, "plugins", "writing", "skills", "editor", "SKILL.md"), "---\nname: editor\ndescription: Edits copy v2\n---\nEdit even more carefully.\n")
	writeTestFile(t, filepath.Join(repo, "plugins", "writing", ".mcp.json"), `{"mcpServers": {
  "docs": {"type": "http", "url": "https://docs.example/v2/mcp", "headers": {"Authorization": "Bearer ${DOCS_TOKEN}"}},
  "local": {"command": "npx", "args": ["-y", "docs-mcp@1"], "env": {"REGION": "${REGION:-eu}"}}
}}`)
	commitAll(t, repo)

	rr = callMarketplace(t, s.MarketplacePluginAPI, http.MethodGet, "/", nil, map[string]string{"id": "team", "plugin": "writing"})
	_ = json.Unmarshal(rr.Body.Bytes(), &detail)
	if detail.Skills[0].InstalledID == "" || !detail.Skills[0].UpdateAvailable || detail.Skills[1].InstalledID != "" {
		t.Fatalf("installed annotations = %+v", detail.Skills)
	}

	installed := skills.skills[skillIDFor(skills, "editor")]
	pkg, err := s.fetchSkillSourcePackage(context.Background(), installed)
	if err != nil || pkg.Checksum == installed.SourceChecksum || !strings.Contains(pkg.Export.SystemPrompt, "even more") {
		t.Fatalf("skill update package = %+v, %v", pkg, err)
	}

	var setID string
	for id := range sets.sets {
		setID = id
	}
	rr = callMarketplace(t, s.MarketplaceMCPUpdatesAPI, http.MethodGet, "/?set_id="+setID, nil, nil)
	var updates struct{ Updates []marketplaceMCPUpdate }
	_ = json.Unmarshal(rr.Body.Bytes(), &updates)
	if len(updates.Updates) != 2 || !updates.Updates[0].UpdateAvailable || updates.Updates[1].UpdateAvailable {
		t.Fatalf("mcp updates = %+v (%s)", updates.Updates, rr.Body)
	}
	rr = callMarketplace(t, s.MarketplaceMCPApplyUpdatesAPI, http.MethodPost, "/", map[string]any{"set_id": setID}, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("apply updates: %d %s", rr.Code, rr.Body)
	}
	got := sets.sets[setID].Config.MCPUpstreams[0]
	if got.URL != "https://docs.example/v2/mcp" || got.Headers["Authorization"] != "Bearer {{var:docs_token}}" {
		t.Fatalf("updated upstream = %+v", got)
	}
}

func skillIDFor(store *fakeSkillStore, name string) string {
	for id, sk := range store.skills {
		if sk.Name == name {
			return id
		}
	}
	return ""
}

func TestRemoteMarketplacePlainSkillsRepository(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, filepath.Join(repo, "alpha", "SKILL.md"), "---\nname: alpha\n---\nA\n")
	writeTestFile(t, filepath.Join(repo, "beta", "SKILL.md"), "---\nname: beta\n---\nB\n")
	commitAll(t, repo)
	s, _, _ := newRemoteMarketplaceServer(t, service.MarketplaceSource{ID: "plain", Name: "plain", Type: service.MarketplaceSourceGit, URL: repo})

	cat, err := s.loadRemoteCatalog(context.Background(), service.MarketplaceSource{Name: "plain", Type: service.MarketplaceSourceGit, URL: repo})
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	if len(cat.File.Plugins) != 1 {
		t.Fatalf("plugins = %+v", cat.File.Plugins)
	}
	detail, _, cleanup, err := s.fetchRemotePlugin(context.Background(), service.MarketplaceSource{Name: "plain", Type: service.MarketplaceSourceGit, URL: repo}, cat.File.Plugins[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if len(detail.Skills) != 2 {
		t.Fatalf("skills = %+v", detail.Skills)
	}
}

func TestRemoteMarketplaceURLSourceWithArchivePlugin(t *testing.T) {
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	for name, body := range map[string]string{
		"plugin/.claude-plugin/plugin.json": `{"name":"zipped","mcpServers":{"api":{"url":"https://api.example/mcp"}}}`,
		"plugin/skills/notes/SKILL.md":      "---\nname: notes\n---\nTake notes.\n",
	} {
		f, _ := zw.Create(name)
		f.Write([]byte(body))
	}
	zw.Close()
	sum := sha256.Sum256(zipBuf.Bytes())

	var srvURL string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/marketplace.json":
			json.NewEncoder(w).Encode(map[string]any{
				"name": "zips", "owner": map[string]string{"name": "x"},
				"plugins": []any{
					map[string]any{"name": "zipped", "source": map[string]any{"source": "archive", "url": srvURL + "/p.zip", "sha256": hex.EncodeToString(sum[:])}},
					map[string]any{"name": "tampered", "source": map[string]any{"source": "archive", "url": srvURL + "/p.zip", "sha256": strings.Repeat("0", 64)}},
					map[string]any{"name": "relative", "source": "./plugins/x"},
				},
			})
		case "/p.zip":
			w.Write(zipBuf.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL
	previous := remoteMarketplaceClient
	remoteMarketplaceClient = srv.Client()
	defer func() { remoteMarketplaceClient = previous }()

	src := service.MarketplaceSource{ID: "zips", Name: "zips", Type: service.MarketplaceSourceURL, URL: srv.URL + "/marketplace.json"}
	s, _, _ := newRemoteMarketplaceServer(t, src)

	rr := callMarketplace(t, s.MarketplaceSourceCatalogAPI, http.MethodGet, "/", nil, map[string]string{"id": "zips"})
	var cat RemoteMarketplace
	_ = json.Unmarshal(rr.Body.Bytes(), &cat)
	for _, p := range cat.Plugins {
		if p.Name == "relative" && p.Installable {
			t.Fatal("relative plugin installable from a URL marketplace")
		}
	}

	detail, _, cleanup, err := s.fetchRemotePlugin(context.Background(), src, "zipped")
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if len(detail.Skills) != 1 || len(detail.MCPServers) != 1 || detail.MCPServers[0].Upstream.URL != "https://api.example/mcp" {
		t.Fatalf("detail = %+v", detail)
	}
	if _, _, _, err := s.fetchRemotePlugin(context.Background(), src, "tampered"); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("tampered archive: %v", err)
	}
}

func TestNormalizeMarketplaceSource(t *testing.T) {
	tests := []struct {
		name    string
		in      service.MarketplaceSource
		want    service.MarketplaceSource
		wantErr bool
	}{
		{"github shorthand", service.MarketplaceSource{URL: "anthropics/claude-plugins-official"},
			service.MarketplaceSource{Name: "claude-plugins-official", Type: "git", URL: "https://github.com/anthropics/claude-plugins-official.git"}, false},
		{"github shorthand with ref", service.MarketplaceSource{URL: "acme/tools@v2"},
			service.MarketplaceSource{Name: "tools", Type: "git", URL: "https://github.com/acme/tools.git", Ref: "v2"}, false},
		{"json url", service.MarketplaceSource{Name: "x", URL: "https://example.com/m/marketplace.json"},
			service.MarketplaceSource{Name: "x", Type: "url", URL: "https://example.com/m/marketplace.json"}, false},
		{"http json refused", service.MarketplaceSource{Type: "url", URL: "http://example.com/marketplace.json"}, service.MarketplaceSource{}, true},
		{"local path refused", service.MarketplaceSource{Type: "git", URL: "/etc"}, service.MarketplaceSource{}, true},
		{"file url refused", service.MarketplaceSource{Type: "git", URL: "file:///etc"}, service.MarketplaceSource{}, true},
		{"option injection refused", service.MarketplaceSource{Type: "git", URL: "--upload-pack=x"}, service.MarketplaceSource{}, true},
		{"path traversal refused", service.MarketplaceSource{Type: "git", URL: "https://g.example/r.git", Path: "../x"}, service.MarketplaceSource{}, true},
		{"scp url", service.MarketplaceSource{Name: "r", URL: "git@g.example:org/r.git"},
			service.MarketplaceSource{Name: "r", Type: "git", URL: "git@g.example:org/r.git"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.in
			err := normalizeMarketplaceSource(&got)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if !tt.wantErr && (got.Name != tt.want.Name || got.Type != tt.want.Type || got.URL != tt.want.URL || got.Ref != tt.want.Ref) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRemoteMarketplaceRelativeSourceCannotEscape(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, filepath.Join(repo, ".claude-plugin", "marketplace.json"), `{"name":"m","owner":{"name":"x"},"plugins":[{"name":"evil","source":"./../../"}]}`)
	commitAll(t, repo)
	src := service.MarketplaceSource{ID: "m", Name: "m", Type: service.MarketplaceSourceGit, URL: repo}
	s, _, _ := newRemoteMarketplaceServer(t, src)
	if _, _, _, err := s.fetchRemotePlugin(context.Background(), src, "evil"); err == nil {
		t.Fatal("relative source escaped the marketplace")
	}
}

func TestExtractPluginZipRejectsTraversal(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("../escape.txt")
	f.Write([]byte("x"))
	zw.Close()
	if _, _, err := extractPluginZip(buf.Bytes()); err == nil {
		t.Fatal("zip traversal admitted")
	}
}
