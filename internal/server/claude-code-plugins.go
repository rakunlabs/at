package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"unicode"

	"github.com/rakunlabs/at/internal/service"
)

const claudeMarketplaceVersion = "1.0.0"

const (
	claudePluginKindMCPServer   = "mcp_server"
	claudePluginKindMarketplace = "marketplace"
)

var errMarketplaceNotFound = errors.New("marketplace not found")

type claudeMarketplaceFile struct {
	Name        string                    `json:"name"`
	Owner       claudeMarketplaceOwner    `json:"owner"`
	Description string                    `json:"description,omitempty"`
	Version     string                    `json:"version,omitempty"`
	Plugins     []claudeMarketplacePlugin `json:"plugins"`
}

type claudeMarketplaceOwner struct {
	Name string `json:"name"`
}

type claudeMarketplacePlugin struct {
	Name        string                 `json:"name"`
	DisplayName string                 `json:"displayName,omitempty"`
	Source      string                 `json:"source"`
	Description string                 `json:"description,omitempty"`
	Version     string                 `json:"version,omitempty"`
	Author      claudeMarketplaceOwner `json:"author,omitempty"`
	Keywords    []string               `json:"keywords,omitempty"`
	Category    string                 `json:"category,omitempty"`
	Homepage    string                 `json:"homepage,omitempty"`
}

type claudePluginManifest struct {
	Name        string                     `json:"name"`
	DisplayName string                     `json:"displayName,omitempty"`
	Version     string                     `json:"version,omitempty"`
	Description string                     `json:"description,omitempty"`
	Author      claudeMarketplaceOwner     `json:"author,omitempty"`
	Homepage    string                     `json:"homepage,omitempty"`
	Keywords    []string                   `json:"keywords,omitempty"`
	MCPServers  map[string]claudeMCPServer `json:"mcpServers,omitempty"`
}

type claudeMCPServer struct {
	Type    string            `json:"type,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type claudePluginItem struct {
	MCPServer  service.MCPServer
	Market     service.Marketplace
	Kind       string
	Skills     []service.Skill
	PluginSlug string
	MCPName    string
	MCPURL     string
	PluginURL  string
	MCPConfigs map[string]claudeMCPServer
}

func (s *Server) ClaudeCodeMarketplaceAPI(w http.ResponseWriter, r *http.Request) {
	marketName := strings.TrimSpace(r.URL.Query().Get("market"))
	items, market, err := s.claudeMarketplaceItems(r.Context(), r, marketName, true)
	if err != nil {
		if errors.Is(err, errMarketplaceNotFound) {
			httpResponse(w, fmt.Sprintf("marketplace %q not found", marketName), http.StatusNotFound)
			return
		}
		httpResponse(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	opts := claudeMarketplaceOptions{DirectSources: true}
	if market != nil {
		opts.Name = market.Name
		opts.Description = market.Description
	}
	httpResponseJSON(w, claudeMarketplaceFromItems(items, opts), http.StatusOK)
}

func (s *Server) ClaudeCodeMarketplaceZipAPI(w http.ResponseWriter, r *http.Request) {
	items, err := s.claudePluginItems(r.Context(), r, true)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := writeJSONZip(zw, ".claude-plugin/marketplace.json", claudeMarketplaceFromItems(items, claudeMarketplaceOptions{})); err != nil {
		slog.Error("claude marketplace zip: write marketplace failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to create marketplace zip: %v", err), http.StatusInternalServerError)
		return
	}
	if err := writeBytesZip(zw, "README.md", []byte(claudeMarketplaceReadme())); err != nil {
		slog.Error("claude marketplace zip: write readme failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to create marketplace zip: %v", err), http.StatusInternalServerError)
		return
	}
	for _, item := range items {
		if err := writeClaudePluginZipEntries(zw, path.Join("plugins", item.PluginSlug), item); err != nil {
			slog.Error("claude marketplace zip: write plugin failed", "plugin", item.displayName(), "error", err)
			httpResponse(w, fmt.Sprintf("failed to create plugin %q: %v", item.displayName(), err), http.StatusInternalServerError)
			return
		}
	}
	if err := zw.Close(); err != nil {
		slog.Error("claude marketplace zip: close failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to create marketplace zip: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="at-claude-marketplace.zip"`)
	w.WriteHeader(http.StatusOK)
	w.Write(buf.Bytes())
}

func (s *Server) ClaudeCodeMarketplacePluginZipAPI(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpResponse(w, "marketplace name is required", http.StatusBadRequest)
		return
	}
	if s.marketplaceStore == nil {
		httpResponse(w, "marketplace store not configured", http.StatusServiceUnavailable)
		return
	}

	market, err := s.marketplaceStore.GetMarketplaceByName(r.Context(), name)
	if err != nil {
		slog.Error("claude marketplace plugin zip: get marketplace failed", "name", name, "error", err)
		httpResponse(w, "internal error looking up marketplace", http.StatusInternalServerError)
		return
	}
	if market == nil {
		httpResponse(w, fmt.Sprintf("marketplace %q not found", name), http.StatusNotFound)
		return
	}

	item, err := s.claudeMarketplacePluginItem(r.Context(), r, market)
	if err != nil {
		slog.Error("claude marketplace plugin zip: build plugin failed", "marketplace", market.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create marketplace plugin: %v", err), http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := writeClaudePluginZipEntries(zw, "", item); err != nil {
		slog.Error("claude marketplace plugin zip: write failed", "marketplace", market.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create marketplace plugin zip: %v", err), http.StatusInternalServerError)
		return
	}
	if err := zw.Close(); err != nil {
		slog.Error("claude marketplace plugin zip: close failed", "marketplace", market.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create marketplace plugin zip: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, item.PluginSlug))
	w.WriteHeader(http.StatusOK)
	w.Write(buf.Bytes())
}

func (s *Server) ClaudeCodePluginZipAPI(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpResponse(w, "mcp server name is required", http.StatusBadRequest)
		return
	}
	if s.mcpServerStore == nil {
		httpResponse(w, "mcp server store not configured", http.StatusServiceUnavailable)
		return
	}

	srv, err := s.mcpServerStore.GetMCPServerByName(r.Context(), name)
	if err != nil {
		slog.Error("claude plugin zip: get mcp server failed", "name", name, "error", err)
		httpResponse(w, "internal error looking up mcp server", http.StatusInternalServerError)
		return
	}
	if srv == nil || !srv.Public {
		httpResponse(w, fmt.Sprintf("public mcp server %q not found", name), http.StatusNotFound)
		return
	}

	item, err := s.claudeMCPServerPluginItem(r.Context(), r, *srv, slugifyClaudeName(srv.Name, "mcp-server"))
	if err != nil {
		slog.Error("claude plugin zip: build mcp server plugin failed", "name", name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create plugin zip: %v", err), http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := writeClaudePluginZipEntries(zw, "", item); err != nil {
		slog.Error("claude plugin zip: write failed", "mcp_server", srv.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create plugin zip: %v", err), http.StatusInternalServerError)
		return
	}
	if err := zw.Close(); err != nil {
		slog.Error("claude plugin zip: close failed", "mcp_server", srv.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create plugin zip: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, item.PluginSlug))
	w.WriteHeader(http.StatusOK)
	w.Write(buf.Bytes())
}

func (s *Server) claudePluginItems(ctx context.Context, r *http.Request, uniqueSlugs bool) ([]claudePluginItem, error) {
	if s.mcpServerStore == nil {
		return nil, fmt.Errorf("mcp server store not configured")
	}

	records, err := s.mcpServerStore.ListMCPServers(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list mcp servers: %w", err)
	}
	if records == nil {
		return nil, nil
	}

	servers := make([]service.MCPServer, 0, len(records.Data))
	for _, srv := range records.Data {
		if srv.Public {
			servers = append(servers, srv)
		}
	}
	sort.SliceStable(servers, func(i, j int) bool {
		return strings.ToLower(servers[i].Name) < strings.ToLower(servers[j].Name)
	})

	seen := map[string]int{}
	items := make([]claudePluginItem, 0, len(servers))
	for _, srv := range servers {
		pluginSlug := slugifyClaudeName(srv.Name, "mcp-server")
		if uniqueSlugs {
			pluginSlug = uniqueSlug(pluginSlug, seen)
		}
		item, err := s.claudeMCPServerPluginItem(ctx, r, srv, pluginSlug)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}

	return items, nil
}

func (s *Server) claudeMarketplaceItems(ctx context.Context, r *http.Request, marketName string, uniqueSlugs bool) ([]claudePluginItem, *service.Marketplace, error) {
	if marketName == "" {
		items, err := s.claudePluginItems(ctx, r, uniqueSlugs)
		return items, nil, err
	}

	if s.marketplaceStore == nil {
		return nil, nil, fmt.Errorf("marketplace store not configured")
	}
	market, err := s.marketplaceStore.GetMarketplaceByName(ctx, marketName)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get marketplace %q: %w", marketName, err)
	}
	if market == nil {
		return nil, nil, errMarketplaceNotFound
	}

	item, err := s.claudeMarketplacePluginItem(ctx, r, market)
	if err != nil {
		return nil, nil, err
	}
	return []claudePluginItem{item}, market, nil
}

func (s *Server) claudeMarketplacePluginItem(ctx context.Context, r *http.Request, market *service.Marketplace) (claudePluginItem, error) {
	skills, err := s.resolveMarketplaceSkills(ctx, market)
	if err != nil {
		return claudePluginItem{}, err
	}
	sortSkills(skills)

	mcpConfigs, err := s.resolveMarketplaceMCPConfigs(ctx, r, market)
	if err != nil {
		return claudePluginItem{}, err
	}

	pluginSlug := slugifyClaudeName(market.Name, "at-market")
	return claudePluginItem{
		Market:     *market,
		Kind:       claudePluginKindMarketplace,
		Skills:     skills,
		PluginSlug: pluginSlug,
		PluginURL:  s.publicClaudeMarketplacePluginURL(r, market.Name),
		MCPConfigs: mcpConfigs,
	}, nil
}

func (s *Server) claudeMCPServerPluginItem(ctx context.Context, r *http.Request, srv service.MCPServer, pluginSlug string) (claudePluginItem, error) {
	skills, err := s.resolveMCPServerSkills(ctx, &srv)
	if err != nil {
		return claudePluginItem{}, err
	}
	sortSkills(skills)

	return claudePluginItem{
		MCPServer:  srv,
		Kind:       claudePluginKindMCPServer,
		Skills:     skills,
		PluginSlug: pluginSlug,
		MCPName:    "at-" + pluginSlug,
		MCPURL:     s.publicMCPServerMCPURL(r, srv.Name),
		PluginURL:  s.publicClaudePluginURL(r, srv.Name),
	}, nil
}

func (s *Server) resolveMarketplaceSkills(ctx context.Context, market *service.Marketplace) ([]service.Skill, error) {
	if s.skillStore == nil && len(market.Skills) > 0 {
		return nil, fmt.Errorf("skill store not configured")
	}

	seen := map[string]bool{}
	skills := make([]service.Skill, 0, len(market.Skills))
	for _, ref := range market.Skills {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		skill, err := s.getSkillByIDOrName(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("failed to get skill %q: %w", ref, err)
		}
		if skill == nil {
			continue
		}

		key := skill.ID
		if key == "" {
			key = skill.Name
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		skills = append(skills, *skill)
	}

	return skills, nil
}

func (s *Server) resolveMCPServerSkills(ctx context.Context, srv *service.MCPServer) ([]service.Skill, error) {
	if srv == nil || len(srv.Config.EnabledSkills) == 0 || s.skillStore == nil {
		return nil, nil
	}

	seen := map[string]bool{}
	skills := make([]service.Skill, 0, len(srv.Config.EnabledSkills))
	for _, ref := range srv.Config.EnabledSkills {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		skill, err := s.getSkillByIDOrName(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("failed to get mcp server skill %q: %w", ref, err)
		}
		if skill == nil {
			continue
		}

		key := skill.ID
		if key == "" {
			key = skill.Name
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		skills = append(skills, *skill)
	}

	return skills, nil
}

func (s *Server) resolveMarketplaceMCPServer(ctx context.Context, ref string) (*service.MCPServer, error) {
	if s.mcpServerStore == nil {
		return nil, fmt.Errorf("mcp server store not configured")
	}

	srv, err := s.mcpServerStore.GetMCPServer(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to get mcp server %q: %w", ref, err)
	}
	if srv != nil {
		return srv, nil
	}

	srv, err = s.mcpServerStore.GetMCPServerByName(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to get mcp server by name %q: %w", ref, err)
	}
	return srv, nil
}

func (s *Server) resolveMarketplaceMCPConfigs(ctx context.Context, r *http.Request, market *service.Marketplace) (map[string]claudeMCPServer, error) {
	configs := map[string]claudeMCPServer{}
	seen := map[string]int{}

	for _, ref := range market.MCPServers {
		srv, err := s.resolveMarketplaceMCPServer(ctx, ref)
		if err != nil {
			return nil, err
		}
		if srv == nil || !srv.Public {
			continue
		}
		name := uniqueSlug("at-"+slugifyClaudeName(srv.Name, "mcp-server"), seen)
		configs[name] = claudeMCPServer{Type: "http", URL: s.publicMCPServerMCPURL(r, srv.Name)}
	}

	for _, direct := range market.DirectMCPServers {
		cfg := claudeMCPServer{
			Type:    direct.Type,
			URL:     direct.URL,
			Headers: direct.Headers,
			Command: direct.Command,
			Args:    direct.Args,
			Env:     direct.Env,
		}
		if cfg.Type == "" && cfg.URL != "" {
			cfg.Type = "http"
		}
		if cfg.Type == "" && cfg.Command != "" {
			cfg.Type = "stdio"
		}
		if cfg.URL == "" && cfg.Command == "" {
			continue
		}
		name := uniqueSlug(slugifyClaudeName(direct.Name, "mcp"), seen)
		configs[name] = cfg
	}

	if len(configs) == 0 {
		return nil, nil
	}
	return configs, nil
}

func (item claudePluginItem) displayName() string {
	if item.Kind == claudePluginKindMarketplace {
		return item.Market.Name
	}
	return item.MCPServer.Name
}

func (item claudePluginItem) description() string {
	if item.Kind == claudePluginKindMarketplace {
		return item.Market.Description
	}
	if item.MCPServer.Description != "" {
		return item.MCPServer.Description
	}
	return item.MCPServer.Config.Description
}

func (item claudePluginItem) keywords() []string {
	if item.Kind == claudePluginKindMarketplace {
		return []string{"at", "marketplace", "skills", "mcp"}
	}
	return []string{"at", "mcp-server", "mcp"}
}

func (item claudePluginItem) category() string {
	if item.Kind == claudePluginKindMarketplace {
		return "AT Marketplaces"
	}
	return "AT MCP Servers"
}

type claudeMarketplaceOptions struct {
	Name          string
	Description   string
	DirectSources bool
}

func claudeMarketplaceFromItems(items []claudePluginItem, opts claudeMarketplaceOptions) claudeMarketplaceFile {
	plugins := make([]claudeMarketplacePlugin, 0, len(items))
	for _, item := range items {
		source := "./plugins/" + item.PluginSlug
		if opts.DirectSources && item.PluginURL != "" {
			source = item.PluginURL
		}
		plugins = append(plugins, claudeMarketplacePlugin{
			Name:        item.PluginSlug,
			DisplayName: item.displayName(),
			Source:      source,
			Description: item.description(),
			Version:     claudeMarketplaceVersion,
			Author:      claudeMarketplaceOwner{Name: "AT"},
			Keywords:    item.keywords(),
			Category:    item.category(),
			Homepage:    item.MCPURL,
		})
	}
	if plugins == nil {
		plugins = []claudeMarketplacePlugin{}
	}

	name := opts.Name
	if name == "" {
		name = "at-mcp-servers"
	}
	description := opts.Description
	if description == "" {
		description = "Public MCP Servers exported from AT as Claude Code plugins."
	}

	return claudeMarketplaceFile{
		Name:        name,
		Owner:       claudeMarketplaceOwner{Name: "AT"},
		Description: description,
		Version:     claudeMarketplaceVersion,
		Plugins:     plugins,
	}
}

func writeClaudePluginZipEntries(zw *zip.Writer, root string, item claudePluginItem) error {
	files, err := claudePluginFiles(item)
	if err != nil {
		return err
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := writeBytesZip(zw, path.Join(root, name), files[name]); err != nil {
			return err
		}
	}

	return nil
}

func claudePluginFiles(item claudePluginItem) (map[string][]byte, error) {
	mcpServers := item.MCPConfigs
	if len(mcpServers) == 0 && item.MCPName != "" {
		mcpServers = map[string]claudeMCPServer{
			item.MCPName: {Type: "http", URL: item.MCPURL},
		}
	}

	manifest := claudePluginManifest{
		Name:        item.PluginSlug,
		DisplayName: item.displayName(),
		Description: item.description(),
		Author:      claudeMarketplaceOwner{Name: "AT"},
		Homepage:    item.MCPURL,
		Keywords:    item.keywords(),
		MCPServers:  mcpServers,
	}
	manifestData, err := marshalJSONBytes(".claude-plugin/plugin.json", manifest)
	if err != nil {
		return nil, err
	}

	files := map[string][]byte{
		".claude-plugin/plugin.json": manifestData,
		"README.md":                  []byte(claudePluginReadme(item)),
	}

	seenSkills := map[string]int{}
	for _, skill := range item.Skills {
		skillSlug := uniqueSlug(slugifyClaudeName(skill.Name, "skill"), seenSkills)
		data, err := skillToMarkdown(&skill)
		if err != nil {
			return nil, fmt.Errorf("generate skill %q: %w", skill.Name, err)
		}
		files[path.Join("skills", skillSlug, "SKILL.md")] = data
	}

	return files, nil
}

func claudePluginReadme(item claudePluginItem) string {
	if item.Kind == claudePluginKindMarketplace {
		return fmt.Sprintf("# %s\n\n"+
			"Generated from AT Marketplace `%s`.\n\n"+
			"- Published skills: %d\n"+
			"- MCP servers: %d\n\n"+
			"Install this marketplace from AT with the JSON URL shown in the AT Marketplaces page.\n",
			item.displayName(), item.Market.Name, len(item.Skills), len(item.MCPConfigs))
	}

	if item.Kind == claudePluginKindMCPServer {
		return fmt.Sprintf("# %s\n\n"+
			"Generated from AT MCP Server `%s`.\n\n"+
			"- MCP endpoint: `%s`\n"+
			"- Claude MCP server name: `%s`\n\n"+
			"- Included skill docs: %d\n\n"+
			"Install this ZIP for a single Claude Code session with:\n\n"+
			"```sh\n"+
			"claude --plugin-url %s\n"+
			"```\n",
			item.displayName(), item.MCPServer.Name, item.MCPURL, item.MCPName, len(item.Skills), item.PluginURL)
	}

	return fmt.Sprintf("# %s\n\nGenerated from AT plugin item.\n", item.displayName())
}

func claudeMarketplaceReadme() string {
	return "# AT Claude Code Marketplace\n\n" +
		"This directory is generated by AT from public MCP Servers.\n\n" +
		"To test locally:\n\n" +
		"```sh\n" +
		"unzip at-claude-marketplace.zip -d at-claude-marketplace\n" +
		"```\n\n" +
		"Then in Claude Code:\n\n" +
		"```text\n" +
		"/plugin marketplace add ./at-claude-marketplace\n" +
		"/plugin install <plugin-name>@at-mcp-servers\n" +
		"/reload-plugins\n" +
		"```\n\n" +
		"To share it with a team, commit this directory to a Git repository and ask users to add that repository as a Claude Code plugin marketplace.\n"
}

func writeJSONZip(zw *zip.Writer, name string, v any) error {
	data, err := marshalJSONBytes(name, v)
	if err != nil {
		return err
	}
	return writeBytesZip(zw, name, data)
}

func marshalJSONBytes(name string, v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", name, err)
	}
	data = append(data, '\n')
	return data, nil
}

func writeBytesZip(zw *zip.Writer, name string, data []byte) error {
	f, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("create zip entry %s: %w", name, err)
	}
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write zip entry %s: %w", name, err)
	}
	return nil
}

func (s *Server) publicMCPServerMCPURL(r *http.Request, name string) string {
	return s.publicBaseURL(r) + "/gateway/v1/mcp/" + url.PathEscape(name)
}

func (s *Server) publicClaudePluginURL(r *http.Request, name string) string {
	return s.publicBaseURL(r) + "/gateway/v1/claude-code/plugins/" + url.PathEscape(name) + "/plugin.zip"
}

func (s *Server) publicClaudeMarketplacePluginURL(r *http.Request, name string) string {
	return s.publicBaseURL(r) + "/gateway/v1/claude-code/marketplaces/" + url.PathEscape(name) + "/plugin.zip"
}

func (s *Server) publicBaseURL(r *http.Request) string {
	if s.config.ExternalURL != "" {
		return strings.TrimSuffix(s.config.ExternalURL, "/") + strings.TrimSuffix(s.config.BasePath, "/")
	}

	scheme := firstHeaderValue(r.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := firstHeaderValue(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}

	return scheme + "://" + host + strings.TrimSuffix(s.config.BasePath, "/")
}

func firstHeaderValue(v string) string {
	parts := strings.Split(v, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			return part
		}
	}
	return ""
}

func slugifyClaudeName(s, fallback string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case unicode.IsLetter(r), unicode.IsDigit(r):
			// Claude plugin names should be kebab-case ASCII. Non-ASCII letters are
			// treated as separators rather than transliterated unpredictably.
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = fallback
	}
	if len(out) > 64 {
		out = strings.Trim(out[:64], "-")
		if out == "" {
			out = fallback
		}
	}
	return out
}

func uniqueSlug(base string, seen map[string]int) string {
	if seen[base] == 0 {
		seen[base] = 1
		return base
	}
	for i := seen[base] + 1; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if seen[candidate] == 0 {
			seen[base] = i
			seen[candidate] = 1
			return candidate
		}
	}
}

func sortSkills(skills []service.Skill) {
	sort.SliceStable(skills, func(i, j int) bool {
		return strings.ToLower(skills[i].Name) < strings.ToLower(skills[j].Name)
	})
}
