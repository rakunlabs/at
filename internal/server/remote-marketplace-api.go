package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// SkillSourceMarketplace marks a skill installed from a remote marketplace:
// SourceURL is the marketplace source ID, SourceRef the plugin name and
// SourcePath the package directory inside the plugin.
const SkillSourceMarketplace = "marketplace"

const remoteMarketplaceTimeout = 2 * time.Minute

func (s *Server) marketplaceSourceForRequest(w http.ResponseWriter, r *http.Request) (*service.MarketplaceSource, bool) {
	if s.marketplaceSourceStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return nil, false
	}
	id := r.PathValue("id")
	src, err := s.marketplaceSourceStore.GetMarketplaceSource(r.Context(), id)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to get marketplace source: %v", err), http.StatusInternalServerError)
		return nil, false
	}
	if src == nil {
		httpResponse(w, fmt.Sprintf("marketplace source %q not found", id), http.StatusNotFound)
		return nil, false
	}
	return src, true
}

// MarketplaceSourceCatalogAPI handles GET /api/v1/marketplace/sources/{id}/catalog.
func (s *Server) MarketplaceSourceCatalogAPI(w http.ResponseWriter, r *http.Request) {
	src, ok := s.marketplaceSourceForRequest(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), remoteMarketplaceTimeout)
	defer cancel()
	cat, err := s.loadRemoteCatalog(ctx, *src)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer cat.Close()
	httpResponseJSON(w, cat.summary(*src), http.StatusOK)
}

// MarketplacePluginAPI handles GET /api/v1/marketplace/sources/{id}/plugins/{plugin}.
// It fetches the plugin and lists the skills and MCP servers it contains,
// marking those already installed and whether they changed upstream.
func (s *Server) MarketplacePluginAPI(w http.ResponseWriter, r *http.Request) {
	src, ok := s.marketplaceSourceForRequest(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), remoteMarketplaceTimeout)
	defer cancel()
	detail, _, cleanup, err := s.fetchRemotePlugin(ctx, *src, r.PathValue("plugin"))
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadGateway)
		return
	}
	cleanup()
	s.annotateInstalledSkills(ctx, src.ID, &detail)
	httpResponseJSON(w, detail, http.StatusOK)
}

func (s *Server) fetchRemotePlugin(ctx context.Context, src service.MarketplaceSource, name string) (RemotePluginDetail, []fetchedSkillPackage, func(), error) {
	noop := func() {}
	cat, err := s.loadRemoteCatalog(ctx, src)
	if err != nil {
		return RemotePluginDetail{}, nil, noop, err
	}
	entry, found := cat.entry(name)
	if !found {
		cat.Close()
		return RemotePluginDetail{}, nil, noop, fmt.Errorf("plugin %q is not listed by %s", name, src.Name)
	}
	dir, cleanupPlugin, err := s.materializePlugin(ctx, cat, src, entry)
	if err != nil {
		cat.Close()
		return RemotePluginDetail{}, nil, noop, err
	}
	cleanup := func() { cleanupPlugin(); cat.Close() }
	detail, packages, err := inspectPluginDir(dir, entry)
	if err != nil {
		cleanup()
		return RemotePluginDetail{}, nil, noop, err
	}
	detail.Plugin = cat.pluginSummary(entry)
	return detail, packages, cleanup, nil
}

func (s *Server) annotateInstalledSkills(ctx context.Context, sourceID string, detail *RemotePluginDetail) {
	if s.skillStore == nil {
		return
	}
	installed, err := s.listMarketplaceSkills(ctx, sourceID, detail.Plugin.Name)
	if err != nil {
		return
	}
	for i := range detail.Skills {
		for _, sk := range installed {
			if sk.SourcePath == detail.Skills[i].Path {
				detail.Skills[i].InstalledID = sk.ID
				detail.Skills[i].UpdateAvailable = sk.SourceChecksum != detail.Skills[i].Checksum
			}
		}
	}
}

func (s *Server) listMarketplaceSkills(ctx context.Context, sourceID, plugin string) ([]service.Skill, error) {
	records, err := s.skillStore.ListSkills(ctx, nil)
	if err != nil || records == nil {
		return nil, err
	}
	var out []service.Skill
	for _, sk := range records.Data {
		if sk.SourceType == SkillSourceMarketplace && sk.SourceURL == sourceID && (plugin == "" || sk.SourceRef == plugin) {
			out = append(out, sk)
		}
	}
	return out, nil
}

type marketplaceInstallRequest struct {
	Plugin string `json:"plugin"`
	// Skills are package paths (RemotePluginSkill.Path) to install.
	Skills []string `json:"skills"`
	// SkillScope is personal (default) or workspace.
	SkillScope string `json:"skill_scope"`
	// MCPServers are server names to add as upstreams.
	MCPServers []string `json:"mcp_servers"`
	// MCPSetID adds the upstreams to an existing set; otherwise a new set
	// named MCPSetName (default: the plugin name) is created.
	MCPSetID   string `json:"mcp_set_id"`
	MCPSetName string `json:"mcp_set_name"`
	MCPScope   string `json:"mcp_scope"`
	// Variables maps ${VAR} placeholders to "var:<key>" or a literal value.
	Variables map[string]string `json:"variables"`
	// AllowStdio must be true to install servers that run a local command.
	AllowStdio bool `json:"allow_stdio"`
}

type marketplaceInstallResult struct {
	Skills   []service.Skill `json:"skills"`
	MCPSet   *service.MCPSet `json:"mcp_set,omitempty"`
	Warnings []string        `json:"warnings,omitempty"`
}

// MarketplaceInstallAPI handles POST /api/v1/marketplace/sources/{id}/install.
func (s *Server) MarketplaceInstallAPI(w http.ResponseWriter, r *http.Request) {
	src, ok := s.marketplaceSourceForRequest(w, r)
	if !ok {
		return
	}
	var req marketplaceInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if req.Plugin == "" || (len(req.Skills) == 0 && len(req.MCPServers) == 0) {
		httpResponse(w, "plugin and at least one skill or MCP server are required", http.StatusBadRequest)
		return
	}
	if len(req.Skills) > 0 && s.skillStore == nil || len(req.MCPServers) > 0 && s.mcpSetStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), remoteMarketplaceTimeout)
	defer cancel()
	detail, packages, cleanup, err := s.fetchRemotePlugin(ctx, *src, req.Plugin)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer cleanup()

	// Resolve and validate the whole selection before writing anything.
	selectedPkgs := make([]fetchedSkillPackage, 0, len(req.Skills))
	for _, path := range req.Skills {
		var found *fetchedSkillPackage
		for i := range packages {
			if packages[i].Path == path {
				found = &packages[i]
			}
		}
		if found == nil {
			httpResponse(w, fmt.Sprintf("plugin %q has no skill at %q", req.Plugin, path), http.StatusBadRequest)
			return
		}
		selectedPkgs = append(selectedPkgs, *found)
	}
	if err := s.validateSkillPackagesForImport(ctx, selectedPkgs); err != nil {
		httpResponse(w, err.Error(), http.StatusConflict)
		return
	}
	upstreams := make([]service.MCPUpstream, 0, len(req.MCPServers))
	for _, name := range req.MCPServers {
		var found *RemotePluginMCP
		for i := range detail.MCPServers {
			if detail.MCPServers[i].Name == name {
				found = &detail.MCPServers[i]
			}
		}
		if found == nil {
			httpResponse(w, fmt.Sprintf("plugin %q has no MCP server %q", req.Plugin, name), http.StatusBadRequest)
			return
		}
		if found.Type == "stdio" && !req.AllowStdio {
			httpResponse(w, fmt.Sprintf("MCP server %q runs a local command; confirm allow_stdio to install it", name), http.StatusBadRequest)
			return
		}
		if found.PluginRelative {
			httpResponse(w, fmt.Sprintf("MCP server %q runs files shipped inside the plugin, which AT does not install", name), http.StatusBadRequest)
			return
		}
		upstreams = append(upstreams, s.marketplaceUpstream(src.ID, req.Plugin, *found, req.Variables))
	}

	result := marketplaceInstallResult{Skills: []service.Skill{}, Warnings: detail.Warnings}
	by := s.getUserEmail(r)
	principal, hasPrincipal := service.AccessPrincipalFromContext(r.Context())
	for _, pkg := range selectedPkgs {
		skill := skillFromExportData(pkg.Export, by)
		skill.SourceType = SkillSourceMarketplace
		skill.SourceURL = src.ID
		skill.SourceRef = req.Plugin
		skill.SourcePath = pkg.Path
		skill.SourceChecksum = pkg.Checksum
		if hasPrincipal {
			if req.SkillScope == "workspace" {
				skill.Scope = "workspace"
			} else {
				skill.Scope, skill.OwnerUserID = "personal", principal.UserID
			}
		}
		record, err := s.skillStore.CreateSkill(ctx, skill)
		if err != nil {
			if workspaceBusinessError(w, err) {
				return
			}
			slog.Error("marketplace install: create skill failed", "source", src.ID, "plugin", req.Plugin, "skill", skill.Name, "error", err)
			httpResponse(w, fmt.Sprintf("failed to install skill %q: %v", skill.Name, err), http.StatusInternalServerError)
			return
		}
		result.Skills = append(result.Skills, *record)
	}

	if len(upstreams) > 0 {
		set, err := s.addMarketplaceUpstreams(ctx, r, req, upstreams, by)
		if err != nil {
			if mcpConfigError(w, err) {
				return
			}
			httpResponse(w, err.Error(), http.StatusBadRequest)
			return
		}
		result.MCPSet = set
	}
	httpResponseJSON(w, result, http.StatusCreated)
}

func (s *Server) marketplaceUpstream(sourceID, plugin string, mcp RemotePluginMCP, variables map[string]string) service.MCPUpstream {
	mapping := map[string]string{}
	for _, v := range mcp.Variables {
		if val, ok := variables[v]; ok {
			mapping[v] = val
		}
	}
	up := applyMCPVariables(mcp.Upstream, mapping)
	up.Origin = &service.MCPUpstreamOrigin{
		SourceID: sourceID, Plugin: plugin, Server: mcp.Name,
		Checksum: remoteMCPChecksum(mcp.Upstream), Variables: mapping,
	}
	return up
}

func remoteMCPChecksum(up service.MCPUpstream) string {
	up.Origin = nil
	data, _ := json.Marshal(up)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Server) addMarketplaceUpstreams(ctx context.Context, r *http.Request, req marketplaceInstallRequest, upstreams []service.MCPUpstream, by string) (*service.MCPSet, error) {
	if req.MCPSetID != "" {
		set, err := s.mcpSetStore.GetMCPSet(ctx, req.MCPSetID)
		if err != nil {
			return nil, fmt.Errorf("failed to get MCP set: %w", err)
		}
		if set == nil {
			return nil, fmt.Errorf("MCP set %q not found", req.MCPSetID)
		}
		for _, up := range upstreams {
			for _, existing := range set.Config.MCPUpstreams {
				if existing.Origin != nil && existing.Origin.SourceID == up.Origin.SourceID && existing.Origin.Plugin == up.Origin.Plugin && existing.Origin.Server == up.Origin.Server {
					return nil, fmt.Errorf("MCP server %q from %q is already in set %q", up.Origin.Server, up.Origin.Plugin, set.Name)
				}
			}
		}
		set.Config.MCPUpstreams = append(set.Config.MCPUpstreams, upstreams...)
		set.UpdatedBy = by
		return s.mcpSetStore.UpdateMCPSet(ctx, set.ID, *set)
	}

	name := strings.TrimSpace(req.MCPSetName)
	if name == "" {
		name = req.Plugin
	}
	set := service.MCPSet{
		Name: name, Description: fmt.Sprintf("Installed from marketplace plugin %s", req.Plugin),
		Config:  service.MCPServerConfig{Description: "Installed from marketplace plugin " + req.Plugin, MCPUpstreams: upstreams},
		Servers: []string{}, URLs: []string{}, CreatedBy: by, UpdatedBy: by,
	}
	principal, ok := service.AccessPrincipalFromContext(r.Context())
	switch {
	case !ok && service.LegacyWorkspaceAccessFromContext(r.Context()):
		set.Scope = "workspace"
	case !ok || principal.UserID == "":
		return nil, fmt.Errorf("personal MCP sets require a signed-in account")
	case req.MCPScope == "workspace":
		set.Scope = "workspace"
	default:
		set.Scope, set.OwnerUserID = "personal", principal.UserID
	}
	return s.mcpSetStore.CreateMCPSet(ctx, set)
}

// marketplaceSkillUpdate refetches a marketplace-installed skill's package.
func (s *Server) marketplaceSkillUpdate(ctx context.Context, skill *service.Skill) (*fetchedSkillPackage, error) {
	if s.marketplaceSourceStore == nil {
		return nil, fmt.Errorf("marketplace store not configured")
	}
	src, err := s.marketplaceSourceStore.GetMarketplaceSource(ctx, skill.SourceURL)
	if err != nil {
		return nil, fmt.Errorf("get marketplace source: %w", err)
	}
	if src == nil {
		return nil, fmt.Errorf("the marketplace this skill came from was removed")
	}
	ctx, cancel := context.WithTimeout(ctx, remoteMarketplaceTimeout)
	defer cancel()
	_, packages, cleanup, err := s.fetchRemotePlugin(ctx, *src, skill.SourceRef)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	for i := range packages {
		if packages[i].Path == skill.SourcePath {
			return &packages[i], nil
		}
	}
	return nil, fmt.Errorf("plugin %q no longer contains %q", skill.SourceRef, skill.SourcePath)
}

// MarketplaceMCPUpdatesAPI handles GET /api/v1/marketplace/mcp-updates?set_id=.
// It reports, per marketplace-installed upstream of the set, whether the
// published configuration changed.
func (s *Server) MarketplaceMCPUpdatesAPI(w http.ResponseWriter, r *http.Request) {
	set, ok := s.mcpSetForMarketplace(w, r, r.URL.Query().Get("set_id"))
	if !ok {
		return
	}
	updates, err := s.marketplaceMCPUpdates(r.Context(), set)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadGateway)
		return
	}
	httpResponseJSON(w, map[string]any{"updates": updates}, http.StatusOK)
}

type marketplaceMCPUpdate struct {
	Index           int                  `json:"index"`
	Plugin          string               `json:"plugin"`
	Server          string               `json:"server"`
	UpdateAvailable bool                 `json:"update_available"`
	Error           string               `json:"error,omitempty"`
	Upstream        *service.MCPUpstream `json:"-"`
}

func (s *Server) mcpSetForMarketplace(w http.ResponseWriter, r *http.Request, id string) (*service.MCPSet, bool) {
	if s.mcpSetStore == nil || s.marketplaceSourceStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return nil, false
	}
	if id == "" {
		httpResponse(w, "set_id is required", http.StatusBadRequest)
		return nil, false
	}
	set, err := s.mcpSetStore.GetMCPSet(r.Context(), id)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to get MCP set: %v", err), http.StatusInternalServerError)
		return nil, false
	}
	if set == nil {
		httpResponse(w, fmt.Sprintf("MCP set %q not found", id), http.StatusNotFound)
		return nil, false
	}
	return set, true
}

func (s *Server) marketplaceMCPUpdates(ctx context.Context, set *service.MCPSet) ([]marketplaceMCPUpdate, error) {
	ctx, cancel := context.WithTimeout(ctx, remoteMarketplaceTimeout)
	defer cancel()
	type key struct{ source, plugin string }
	details := map[key]*RemotePluginDetail{}
	errs := map[key]error{}
	updates := []marketplaceMCPUpdate{}
	for i, up := range set.Config.MCPUpstreams {
		if up.Origin == nil {
			continue
		}
		k := key{up.Origin.SourceID, up.Origin.Plugin}
		if _, seen := details[k]; !seen && errs[k] == nil {
			src, err := s.marketplaceSourceStore.GetMarketplaceSource(ctx, k.source)
			switch {
			case err != nil:
				errs[k] = err
			case src == nil:
				errs[k] = fmt.Errorf("marketplace removed")
			default:
				detail, _, cleanup, err := s.fetchRemotePlugin(ctx, *src, k.plugin)
				cleanup()
				if err != nil {
					errs[k] = err
				} else {
					details[k] = &detail
				}
			}
		}
		u := marketplaceMCPUpdate{Index: i, Plugin: up.Origin.Plugin, Server: up.Origin.Server}
		if err := errs[k]; err != nil {
			u.Error = err.Error()
		} else {
			u.Error = fmt.Sprintf("plugin no longer declares %q", up.Origin.Server)
			for _, mcp := range details[k].MCPServers {
				if mcp.Name == up.Origin.Server {
					u.Error = ""
					u.UpdateAvailable = remoteMCPChecksum(mcp.Upstream) != up.Origin.Checksum
					next := s.marketplaceUpstream(k.source, k.plugin, mcp, up.Origin.Variables)
					next.Auth, next.Proxy, next.InsecureSkipVerify = up.Auth, up.Proxy, up.InsecureSkipVerify
					u.Upstream = &next
				}
			}
		}
		updates = append(updates, u)
	}
	return updates, nil
}

// MarketplaceMCPApplyUpdatesAPI handles POST /api/v1/marketplace/mcp-updates
// with {set_id, allow_stdio}: every changed marketplace upstream is replaced
// with its published configuration. Local OAuth, proxy and TLS settings and
// the variable mapping chosen at install are kept.
func (s *Server) MarketplaceMCPApplyUpdatesAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SetID      string `json:"set_id"`
		AllowStdio bool   `json:"allow_stdio"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	set, ok := s.mcpSetForMarketplace(w, r, req.SetID)
	if !ok {
		return
	}
	updates, err := s.marketplaceMCPUpdates(r.Context(), set)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadGateway)
		return
	}
	changed := 0
	for _, u := range updates {
		if !u.UpdateAvailable || u.Upstream == nil {
			continue
		}
		if u.Upstream.Command != "" && !req.AllowStdio {
			httpResponse(w, fmt.Sprintf("updated MCP server %q runs a local command; confirm allow_stdio", u.Server), http.StatusBadRequest)
			return
		}
		set.Config.MCPUpstreams[u.Index] = *u.Upstream
		changed++
	}
	if changed == 0 {
		httpResponseJSON(w, set, http.StatusOK)
		return
	}
	set.UpdatedBy = s.getUserEmail(r)
	record, err := s.mcpSetStore.UpdateMCPSet(r.Context(), set.ID, *set)
	if err != nil {
		if mcpConfigError(w, err) {
			return
		}
		httpResponse(w, fmt.Sprintf("failed to update MCP set: %v", err), http.StatusInternalServerError)
		return
	}
	httpResponseJSON(w, record, http.StatusOK)
}
