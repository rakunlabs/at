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
	"github.com/rakunlabs/at/internal/skillmd"
)

const claudeMarketplaceVersion = "1.0.0"

const (
	claudePluginKindSkillServer = "skill_server"
	claudePluginKindMCPServer   = "mcp_server"
	claudePluginKindMarketplace = "marketplace"
)

var errMarketplaceNotFound = errors.New("marketplace not found")

type publicSkillServerBundle struct {
	Server  service.SkillServer
	Skills  []service.Skill
	Missing []string
}

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
	Server     service.SkillServer
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

type publicSkillHubResponse struct {
	Servers []publicSkillHubServer `json:"servers"`
	Skills  []publicSkillHubSkill  `json:"skills"`
}

type publicSkillHubServer struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description,omitempty"`
	Mode        string `json:"mode"`
	SkillCount  int    `json:"skill_count"`
	MCPURL      string `json:"mcp_url"`
	PluginURL   string `json:"plugin_url"`
}

type publicSkillHubSkill struct {
	Name        string   `json:"name"`
	Slug        string   `json:"slug"`
	Description string   `json:"description,omitempty"`
	Category    string   `json:"category,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Server      string   `json:"server"`
	MCPURL      string   `json:"mcp_url"`
	PluginURL   string   `json:"plugin_url"`
}

func (s *Server) PublicSkillHubAPI(w http.ResponseWriter, r *http.Request) {
	items, err := s.claudePluginItems(r.Context(), r, true)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	resp := publicSkillHubResponse{
		Servers: make([]publicSkillHubServer, 0, len(items)),
		Skills:  []publicSkillHubSkill{},
	}
	for _, item := range items {
		resp.Servers = append(resp.Servers, publicSkillHubServer{
			Name:        item.Server.Name,
			Slug:        item.PluginSlug,
			Description: item.Server.Description,
			Mode:        item.Server.Mode,
			SkillCount:  len(item.Skills),
			MCPURL:      item.MCPURL,
			PluginURL:   item.PluginURL,
		})
		for _, skill := range item.Skills {
			resp.Skills = append(resp.Skills, publicSkillHubSkill{
				Name:        skill.Name,
				Slug:        slugifyClaudeName(skill.Name, "skill"),
				Description: skill.Description,
				Category:    skill.Category,
				Tags:        skill.Tags,
				Server:      item.Server.Name,
				MCPURL:      item.MCPURL,
				PluginURL:   item.PluginURL,
			})
		}
	}

	httpResponseJSON(w, resp, http.StatusOK)
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

	opts := claudeMarketplaceOptions{}
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
			slog.Error("claude marketplace zip: write plugin failed", "server", item.Server.Name, "error", err)
			httpResponse(w, fmt.Sprintf("failed to create plugin %q: %v", item.Server.Name, err), http.StatusInternalServerError)
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

func (s *Server) ClaudeCodePluginZipAPI(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpResponse(w, "skill server name is required", http.StatusBadRequest)
		return
	}
	if s.skillServerStore == nil {
		httpResponse(w, "skill server store not configured", http.StatusServiceUnavailable)
		return
	}

	srv, err := s.skillServerStore.GetSkillServerByName(r.Context(), name)
	if err != nil {
		slog.Error("claude plugin zip: get skill server failed", "name", name, "error", err)
		httpResponse(w, "internal error looking up skill server", http.StatusInternalServerError)
		return
	}
	if srv == nil || !srv.Public {
		httpResponse(w, fmt.Sprintf("public skill server %q not found", name), http.StatusNotFound)
		return
	}

	skills, _ := s.resolveSkillServerSkills(r.Context(), srv)
	sortSkills(skills)
	item := claudePluginItem{
		Server:     *srv,
		Kind:       claudePluginKindSkillServer,
		Skills:     skills,
		PluginSlug: slugifyClaudeName(srv.Name, "at-skill-server"),
	}
	item.MCPName = "at-" + item.PluginSlug
	item.MCPURL = s.publicSkillServerMCPURL(r, srv.Name)
	item.PluginURL = s.publicClaudePluginURL(r, srv.Name)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := writeClaudePluginZipEntries(zw, "", item); err != nil {
		slog.Error("claude plugin zip: write failed", "server", srv.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create plugin zip: %v", err), http.StatusInternalServerError)
		return
	}
	if err := zw.Close(); err != nil {
		slog.Error("claude plugin zip: close failed", "server", srv.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create plugin zip: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, item.PluginSlug))
	w.WriteHeader(http.StatusOK)
	w.Write(buf.Bytes())
}

func (s *Server) claudePluginItems(ctx context.Context, r *http.Request, uniqueSlugs bool) ([]claudePluginItem, error) {
	bundles, err := s.publicSkillServerBundles(ctx)
	if err != nil {
		return nil, err
	}

	seen := map[string]int{}
	items := make([]claudePluginItem, 0, len(bundles))
	for _, bundle := range bundles {
		pluginSlug := slugifyClaudeName(bundle.Server.Name, "at-skill-server")
		if uniqueSlugs {
			pluginSlug = uniqueSlug(pluginSlug, seen)
		}
		item := claudePluginItem{
			Server:     bundle.Server,
			Kind:       claudePluginKindSkillServer,
			Skills:     bundle.Skills,
			PluginSlug: pluginSlug,
			MCPName:    "at-" + pluginSlug,
			MCPURL:     s.publicSkillServerMCPURL(r, bundle.Server.Name),
			PluginURL:  s.publicClaudePluginURL(r, bundle.Server.Name),
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

func (s *Server) claudeSkillServerPluginItem(r *http.Request, srv service.SkillServer, skills []service.Skill, pluginSlug string) claudePluginItem {
	return claudePluginItem{
		Server:     srv,
		Kind:       claudePluginKindSkillServer,
		Skills:     skills,
		PluginSlug: pluginSlug,
		MCPName:    "at-" + pluginSlug,
		MCPURL:     s.publicSkillServerMCPURL(r, srv.Name),
		PluginURL:  s.publicClaudePluginURL(r, srv.Name),
	}
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
		MCPConfigs: mcpConfigs,
	}, nil
}

func (s *Server) resolveMarketplaceSkillServer(ctx context.Context, ref string) (*service.SkillServer, error) {
	if s.skillServerStore == nil {
		return nil, fmt.Errorf("skill server store not configured")
	}

	srv, err := s.skillServerStore.GetSkillServer(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to get skill server %q: %w", ref, err)
	}
	if srv != nil {
		return srv, nil
	}

	srv, err = s.skillServerStore.GetSkillServerByName(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to get skill server by name %q: %w", ref, err)
	}
	return srv, nil
}

func (s *Server) resolveMarketplaceSkills(ctx context.Context, market *service.Marketplace) ([]service.Skill, error) {
	if s.skillStore == nil && (len(market.Skills) > 0 || len(market.SkillServers) > 0) {
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

	// Legacy compatibility for marketplaces created before direct skill refs.
	for _, ref := range market.SkillServers {
		srv, err := s.resolveMarketplaceSkillServer(ctx, ref)
		if err != nil {
			return nil, err
		}
		if srv == nil || !srv.Public {
			continue
		}
		serverSkills, _ := s.resolveSkillServerSkills(ctx, srv)
		for _, skill := range serverSkills {
			key := skill.ID
			if key == "" {
				key = skill.Name
			}
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			skills = append(skills, skill)
		}
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

func (s *Server) publicSkillServerBundles(ctx context.Context) ([]publicSkillServerBundle, error) {
	if s.skillServerStore == nil {
		return nil, fmt.Errorf("skill server store not configured")
	}

	records, err := s.skillServerStore.ListSkillServers(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list skill servers: %w", err)
	}
	if records == nil {
		return nil, nil
	}

	var bundles []publicSkillServerBundle
	for _, srv := range records.Data {
		if !srv.Public {
			continue
		}
		srvCopy := srv
		skills, missing := s.resolveSkillServerSkills(ctx, &srvCopy)
		sortSkills(skills)
		bundles = append(bundles, publicSkillServerBundle{Server: srv, Skills: skills, Missing: missing})
	}
	sort.SliceStable(bundles, func(i, j int) bool {
		return strings.ToLower(bundles[i].Server.Name) < strings.ToLower(bundles[j].Server.Name)
	})

	return bundles, nil
}

func (item claudePluginItem) displayName() string {
	if item.Kind == claudePluginKindMarketplace {
		return item.Market.Name
	}
	if item.Kind == claudePluginKindMCPServer {
		return item.MCPServer.Name
	}
	return item.Server.Name
}

func (item claudePluginItem) description() string {
	if item.Kind == claudePluginKindMarketplace {
		return item.Market.Description
	}
	if item.Kind == claudePluginKindMCPServer {
		if item.MCPServer.Description != "" {
			return item.MCPServer.Description
		}
		return item.MCPServer.Config.Description
	}
	return item.Server.Description
}

func (item claudePluginItem) keywords() []string {
	if item.Kind == claudePluginKindMarketplace {
		return []string{"at", "marketplace", "skills", "mcp"}
	}
	if item.Kind == claudePluginKindMCPServer {
		return []string{"at", "mcp-server", "mcp"}
	}
	return []string{"at", "skill-server", "mcp"}
}

func (item claudePluginItem) category() string {
	if item.Kind == claudePluginKindMarketplace {
		return "AT Marketplaces"
	}
	if item.Kind == claudePluginKindMCPServer {
		return "AT MCP Servers"
	}
	return "AT Skill Servers"
}

type claudeMarketplaceOptions struct {
	Name        string
	Description string
}

func claudeMarketplaceFromItems(items []claudePluginItem, opts claudeMarketplaceOptions) claudeMarketplaceFile {
	plugins := make([]claudeMarketplacePlugin, 0, len(items))
	for _, item := range items {
		plugins = append(plugins, claudeMarketplacePlugin{
			Name:        item.PluginSlug,
			DisplayName: item.displayName(),
			Source:      "./plugins/" + item.PluginSlug,
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
		name = "at-skill-servers"
	}
	description := opts.Description
	if description == "" {
		description = "Public Skill Servers exported from AT as Claude Code plugins."
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
		data, err := claudeSkillMarkdown(item, skill, skillSlug)
		if err != nil {
			return nil, fmt.Errorf("generate skill %q: %w", skill.Name, err)
		}
		files[path.Join("skills", skillSlug, "SKILL.md")] = data
	}

	return files, nil
}

func claudeSkillMarkdown(item claudePluginItem, skill service.Skill, skillSlug string) ([]byte, error) {
	if item.Kind == claudePluginKindMarketplace {
		return skillToMarkdown(&skill)
	}

	body := claudeSkillBody(item, skill)
	return skillmd.Generate(&skillmd.SkillMD{
		Name:        skillSlug,
		Description: skill.Description,
		Category:    skill.Category,
		Tags:        skill.Tags,
		Body:        body,
	}, nil)
}

func claudeSkillBody(item claudePluginItem, skill service.Skill) string {
	var b strings.Builder
	if strings.TrimSpace(skill.SystemPrompt) != "" {
		b.WriteString(strings.TrimSpace(skill.SystemPrompt))
		b.WriteString("\n\n")
	}
	b.WriteString("## AT Skill Server\n\n")
	b.WriteString(fmt.Sprintf("This skill is published from AT Skill Server `%s`. ", item.Server.Name))
	b.WriteString(fmt.Sprintf("The plugin also registers remote MCP server `%s` at `%s`. ", item.MCPName, item.MCPURL))
	b.WriteString("Use that MCP server when executable AT-hosted tools or package export tools are needed.\n")

	return b.String()
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
			"Install this ZIP for a single Claude Code session with:\n\n"+
			"```sh\n"+
			"claude --plugin-url %s\n"+
			"```\n",
			item.displayName(), item.MCPServer.Name, item.MCPURL, item.MCPName, item.PluginURL)
	}

	return fmt.Sprintf("# %s\n\n"+
		"Generated from AT Skill Server `%s`.\n\n"+
		"- MCP endpoint: `%s`\n"+
		"- Claude MCP server name: `%s`\n"+
		"- Published skills: %d\n\n"+
		"Install this ZIP for a single Claude Code session with:\n\n"+
		"```sh\n"+
		"claude --plugin-url %s\n"+
		"```\n\n"+
		"For persistent marketplace installation, use the `at-claude-marketplace.zip` export, unzip it, host the directory in a Git repository, then add it with `/plugin marketplace add <repo-or-path>`.\n",
		item.Server.Name, item.Server.Name, item.MCPURL, item.MCPName, len(item.Skills), item.PluginURL)
}

func claudeMarketplaceReadme() string {
	return "# AT Claude Code Marketplace\n\n" +
		"This directory is generated by AT from public Skill Servers.\n\n" +
		"To test locally:\n\n" +
		"```sh\n" +
		"unzip at-claude-marketplace.zip -d at-claude-marketplace\n" +
		"```\n\n" +
		"Then in Claude Code:\n\n" +
		"```text\n" +
		"/plugin marketplace add ./at-claude-marketplace\n" +
		"/plugin install <plugin-name>@at-skill-servers\n" +
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

func (s *Server) publicSkillServerMCPURL(r *http.Request, name string) string {
	return s.publicBaseURL(r) + "/gateway/v1/skill-servers/" + url.PathEscape(name) + "/mcp"
}

func (s *Server) publicMCPServerMCPURL(r *http.Request, name string) string {
	return s.publicBaseURL(r) + "/gateway/v1/mcp/" + url.PathEscape(name)
}

func (s *Server) publicClaudePluginURL(r *http.Request, name string) string {
	return s.publicBaseURL(r) + "/gateway/v1/claude-code/plugins/" + url.PathEscape(name) + "/plugin.zip"
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
