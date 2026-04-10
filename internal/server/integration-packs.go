package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/rakunlabs/at/internal/agentmd"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/skillmd"
)

//go:embed all:integration_packs
var integrationPackFS embed.FS

// ─── Types ───

// IntegrationPack is a folder-based bundle of skills, MCP sets, agents, and
// optionally an organization — all installed together.
type IntegrationPack struct {
	Slug        string                `json:"slug"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Category    string                `json:"category"`
	Icon        string                `json:"icon,omitempty"`
	Author      string                `json:"author,omitempty"`
	Version     string                `json:"version"`
	ReadOnly    bool                  `json:"read_only"`            // true for embedded and git packs
	Source      string                `json:"source"`               // "embedded", "user", "git"
	SourceURL   string                `json:"source_url,omitempty"` // Git repo URL
	SourceID    string                `json:"source_id,omitempty"`  // Pack source DB ID
	Variables   []RequiredVariable    `json:"variables,omitempty"`
	Components  IntegrationComponents `json:"components"`
}

// IntegrationComponents holds all installable entities in a pack.
type IntegrationComponents struct {
	Skills       []IntegrationSkill       `json:"skills,omitempty"`
	MCPSets      []IntegrationMCPSet      `json:"mcp_sets,omitempty"`
	Agents       []IntegrationAgent       `json:"agents,omitempty"`
	Organization *IntegrationOrganization `json:"organization,omitempty"`
}

// IntegrationSkill is a skill definition inside a pack.
type IntegrationSkill struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	Category     string         `json:"category"`
	Tags         []string       `json:"tags,omitempty"`
	SystemPrompt string         `json:"system_prompt"`
	Tools        []service.Tool `json:"tools,omitempty"`
}

// IntegrationMCPSet is an MCP set definition inside a pack.
type IntegrationMCPSet struct {
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Category    string                  `json:"category"`
	Tags        []string                `json:"tags,omitempty"`
	Config      service.MCPServerConfig `json:"config"`
	Servers     []string                `json:"servers,omitempty"`
	URLs        []string                `json:"urls,omitempty"`
}

// IntegrationAgent is an agent definition inside a pack.
type IntegrationAgent struct {
	Name   string              `json:"name"`
	Config service.AgentConfig `json:"config"`
}

// IntegrationOrganization is an org definition inside a pack.
type IntegrationOrganization struct {
	Name          string                    `json:"name"`
	Description   string                    `json:"description"`
	Relationships []IntegrationRelationship `json:"relationships,omitempty"`
}

// IntegrationRelationship maps agent names to org roles.
type IntegrationRelationship struct {
	AgentName       string `json:"agent_name"`
	Role            string `json:"role,omitempty"`
	Title           string `json:"title,omitempty"`
	ParentAgentName string `json:"parent_agent_name,omitempty"`
	IsHead          bool   `json:"is_head,omitempty"`
}

// PackInstallRequest specifies which components to install.
type PackInstallRequest struct {
	Skills       bool     `json:"skills"`
	MCPSets      bool     `json:"mcp_sets"`
	Agents       []string `json:"agents,omitempty"` // agent names to install, empty = all
	Organization bool     `json:"organization"`
}

// PackInstallResult reports what was installed.
type PackInstallResult struct {
	SkillsCreated  int    `json:"skills_created"`
	MCPSetsCreated int    `json:"mcp_sets_created"`
	AgentsCreated  int    `json:"agents_created"`
	OrganizationID string `json:"organization_id,omitempty"`
}

// ─── Folder-Based Pack Loading ───

// loadIntegrationPacks loads packs from embedded FS, user directory, and git sources.
func (s *Server) loadIntegrationPacks() {
	// 1. Load embedded packs.
	embeddedFS, err := fs.Sub(integrationPackFS, "integration_packs")
	if err != nil {
		slog.Warn("failed to get embedded integration_packs sub-FS", "error", err)
	} else {
		s.loadPacksFromFS(embeddedFS, true, "embedded", "", "")
	}

	// 2. Load user packs from ~/.config/at/packs/.
	packsDir := s.getPacksDir()
	if packsDir != "" {
		if info, err := os.Stat(packsDir); err == nil && info.IsDir() {
			s.loadPacksFromFS(os.DirFS(packsDir), false, "user", "", "")
		}
	}

	// 3. Load packs from git sources (cloned repos).
	if packsDir != "" && s.packSourceStore != nil {
		reposDir := filepath.Join(packsDir, "_repos")
		if info, err := os.Stat(reposDir); err == nil && info.IsDir() {
			// Get source metadata from DB.
			sources, err := s.packSourceStore.ListPackSources(s.ctx, nil)
			if err == nil && sources != nil {
				for _, src := range sources.Data {
					if src.Status != "synced" {
						continue
					}
					srcDir := filepath.Join(reposDir, src.ID)
					if info, err := os.Stat(srcDir); err == nil && info.IsDir() {
						s.loadPacksFromFS(os.DirFS(srcDir), true, "git", src.URL, src.ID)
					}
				}
			}
		}
	}

	slog.Info("loaded integration packs", "count", len(s.integrationPacks))
}

// loadPacksFromFS scans a filesystem for pack folders (containing pack.json).
func (s *Server) loadPacksFromFS(fsys fs.FS, readOnly bool, source, sourceURL, sourceID string) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		slog.Warn("failed to read packs directory", "error", err)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		slug := entry.Name()
		if strings.HasPrefix(slug, ".") || strings.HasPrefix(slug, "_") {
			continue
		}

		pack, err := loadPackFolder(fsys, slug, readOnly)
		if err != nil {
			slog.Warn("failed to load integration pack", "slug", slug, "error", err)
			continue
		}

		pack.Source = source
		pack.SourceURL = sourceURL
		pack.SourceID = sourceID
		s.integrationPacks = append(s.integrationPacks, *pack)
	}
}

// loadPackFolder reads a single pack folder: pack.json + skills/ + agents/ + mcp_sets/ + organization.json.
func loadPackFolder(fsys fs.FS, slug string, readOnly bool) (*IntegrationPack, error) {
	// Read pack.json metadata.
	metaData, err := fs.ReadFile(fsys, filepath.Join(slug, "pack.json"))
	if err != nil {
		return nil, fmt.Errorf("read pack.json: %w", err)
	}

	var pack IntegrationPack
	if err := json.Unmarshal(metaData, &pack); err != nil {
		return nil, fmt.Errorf("parse pack.json: %w", err)
	}

	if pack.Slug == "" {
		pack.Slug = slug
	}
	pack.ReadOnly = readOnly

	// Load skills from skills/ subdirectory.
	pack.Components.Skills = loadPackSkills(fsys, slug)

	// Load agents from agents/ subdirectory.
	pack.Components.Agents = loadPackAgents(fsys, slug)

	// Load MCP sets from mcp_sets/ subdirectory.
	pack.Components.MCPSets = loadPackMCPSets(fsys, slug)

	// Load organization.json if present.
	if orgData, err := fs.ReadFile(fsys, filepath.Join(slug, "organization.json")); err == nil {
		var org IntegrationOrganization
		if err := json.Unmarshal(orgData, &org); err == nil {
			pack.Components.Organization = &org
		}
	}

	return &pack, nil
}

// loadPackSkills reads .md and .json files from the skills/ subdirectory.
func loadPackSkills(fsys fs.FS, slug string) []IntegrationSkill {
	dir := filepath.Join(slug, "skills")
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil
	}

	var skills []IntegrationSkill
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		data, err := fs.ReadFile(fsys, filepath.Join(dir, entry.Name()))
		if err != nil {
			slog.Warn("failed to read pack skill", "file", entry.Name(), "error", err)
			continue
		}

		name := entry.Name()
		switch {
		case strings.HasSuffix(name, ".md"):
			parsed, tools, err := skillmd.ParseWithTools(data)
			if err != nil {
				slog.Warn("failed to parse skill markdown", "file", name, "error", err)
				continue
			}
			sk := IntegrationSkill{
				Name:         parsed.Name,
				Description:  parsed.Description,
				Category:     parsed.Category,
				Tags:         parsed.Tags,
				SystemPrompt: parsed.Body,
			}
			for _, t := range tools {
				sk.Tools = append(sk.Tools, service.Tool{
					Name:        t.Name,
					Description: t.Description,
					InputSchema: t.InputSchema,
					Handler:     t.Handler,
					HandlerType: t.HandlerType,
				})
			}
			skills = append(skills, sk)

		case strings.HasSuffix(name, ".json"):
			var sk IntegrationSkill
			if err := json.Unmarshal(data, &sk); err != nil {
				slog.Warn("failed to parse skill JSON", "file", name, "error", err)
				continue
			}
			skills = append(skills, sk)
		}
	}

	return skills
}

// loadPackAgents reads .md and .json files from the agents/ subdirectory.
func loadPackAgents(fsys fs.FS, slug string) []IntegrationAgent {
	dir := filepath.Join(slug, "agents")
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil
	}

	var agents []IntegrationAgent
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		data, err := fs.ReadFile(fsys, filepath.Join(dir, entry.Name()))
		if err != nil {
			slog.Warn("failed to read pack agent", "file", entry.Name(), "error", err)
			continue
		}

		name := entry.Name()
		switch {
		case strings.HasSuffix(name, ".md"):
			parsed, err := agentmd.Parse(data)
			if err != nil {
				slog.Warn("failed to parse agent markdown", "file", name, "error", err)
				continue
			}
			agent := IntegrationAgent{
				Name: parsed.Name,
				Config: service.AgentConfig{
					Description:               parsed.Description,
					Provider:                  parsed.Provider,
					Model:                     parsed.Model,
					SystemPrompt:              parsed.SystemPrompt,
					Skills:                    parsed.Skills,
					MCPSets:                   parsed.MCPSets,
					MCPs:                      parsed.MCPs,
					BuiltinTools:              parsed.BuiltinTools,
					MaxIterations:             parsed.MaxIterations,
					ToolTimeout:               parsed.ToolTimeout,
					ConfirmationRequiredTools: parsed.ConfirmationRequiredTools,
					AvatarSeed:                parsed.AvatarSeed,
				},
			}
			agents = append(agents, agent)

		case strings.HasSuffix(name, ".json"):
			var agent IntegrationAgent
			if err := json.Unmarshal(data, &agent); err != nil {
				slog.Warn("failed to parse agent JSON", "file", name, "error", err)
				continue
			}
			agents = append(agents, agent)
		}
	}

	return agents
}

// loadPackMCPSets reads .json files from the mcp_sets/ subdirectory.
func loadPackMCPSets(fsys fs.FS, slug string) []IntegrationMCPSet {
	dir := filepath.Join(slug, "mcp_sets")
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil
	}

	var sets []IntegrationMCPSet
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, err := fs.ReadFile(fsys, filepath.Join(dir, entry.Name()))
		if err != nil {
			slog.Warn("failed to read pack mcp set", "file", entry.Name(), "error", err)
			continue
		}

		var ms IntegrationMCPSet
		if err := json.Unmarshal(data, &ms); err != nil {
			slog.Warn("failed to parse mcp set JSON", "file", entry.Name(), "error", err)
			continue
		}
		sets = append(sets, ms)
	}

	return sets
}

// ─── API Handlers ───

// ListIntegrationPacksAPI handles GET /api/v1/integration-packs.
func (s *Server) ListIntegrationPacksAPI(w http.ResponseWriter, r *http.Request) {
	type packSummary struct {
		Slug        string `json:"slug"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Category    string `json:"category"`
		Icon        string `json:"icon,omitempty"`
		Author      string `json:"author,omitempty"`
		Version     string `json:"version"`
		ReadOnly    bool   `json:"read_only"`
		Source      string `json:"source"`
		SourceURL   string `json:"source_url,omitempty"`
		SourceID    string `json:"source_id,omitempty"`
		Counts      struct {
			Skills       int  `json:"skills"`
			MCPSets      int  `json:"mcp_sets"`
			Agents       int  `json:"agents"`
			Organization bool `json:"organization"`
		} `json:"counts"`
		Variables []RequiredVariable `json:"variables,omitempty"`
	}

	summaries := make([]packSummary, 0, len(s.integrationPacks))
	for _, p := range s.integrationPacks {
		ps := packSummary{
			Slug:        p.Slug,
			Name:        p.Name,
			Description: p.Description,
			Category:    p.Category,
			Icon:        p.Icon,
			Author:      p.Author,
			Version:     p.Version,
			ReadOnly:    p.ReadOnly,
			Source:      p.Source,
			SourceURL:   p.SourceURL,
			SourceID:    p.SourceID,
			Variables:   p.Variables,
		}
		ps.Counts.Skills = len(p.Components.Skills)
		ps.Counts.MCPSets = len(p.Components.MCPSets)
		ps.Counts.Agents = len(p.Components.Agents)
		ps.Counts.Organization = p.Components.Organization != nil
		summaries = append(summaries, ps)
	}

	httpResponseJSON(w, summaries, http.StatusOK)
}

// GetIntegrationPackAPI handles GET /api/v1/integration-packs/{slug}.
func (s *Server) GetIntegrationPackAPI(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	for i := range s.integrationPacks {
		if s.integrationPacks[i].Slug == slug {
			httpResponseJSON(w, s.integrationPacks[i], http.StatusOK)
			return
		}
	}
	httpResponse(w, fmt.Sprintf("integration pack %q not found", slug), http.StatusNotFound)
}

// InstallIntegrationPackAPI handles POST /api/v1/integration-packs/{slug}/install.
func (s *Server) InstallIntegrationPackAPI(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	var pack *IntegrationPack
	for i := range s.integrationPacks {
		if s.integrationPacks[i].Slug == slug {
			pack = &s.integrationPacks[i]
			break
		}
	}
	if pack == nil {
		httpResponse(w, fmt.Sprintf("integration pack %q not found", slug), http.StatusNotFound)
		return
	}

	var req PackInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	userEmail := s.getUserEmail(r)
	result := PackInstallResult{}

	// 1. Install skills.
	if req.Skills && s.skillStore != nil {
		for _, sk := range pack.Components.Skills {
			skill := service.Skill{
				Name:         sk.Name,
				Description:  sk.Description,
				Category:     sk.Category,
				Tags:         sk.Tags,
				SystemPrompt: sk.SystemPrompt,
				Tools:        sk.Tools,
				CreatedBy:    userEmail,
				UpdatedBy:    userEmail,
			}
			if _, err := s.skillStore.CreateSkill(ctx, skill); err != nil {
				slog.Warn("install pack: create skill failed", "name", sk.Name, "error", err)
				continue
			}
			result.SkillsCreated++
		}
	}

	// 2. Install MCP sets.
	if req.MCPSets && s.mcpSetStore != nil {
		for _, ms := range pack.Components.MCPSets {
			mcpSet := service.MCPSet{
				Name:        ms.Name,
				Description: ms.Description,
				Category:    ms.Category,
				Tags:        ms.Tags,
				Config:      ms.Config,
				Servers:     ms.Servers,
				URLs:        ms.URLs,
				CreatedBy:   userEmail,
				UpdatedBy:   userEmail,
			}
			if mcpSet.Servers == nil {
				mcpSet.Servers = []string{}
			}
			if mcpSet.URLs == nil {
				mcpSet.URLs = []string{}
			}
			if _, err := s.mcpSetStore.CreateMCPSet(ctx, mcpSet); err != nil {
				slog.Warn("install pack: create mcp set failed", "name", ms.Name, "error", err)
				continue
			}
			result.MCPSetsCreated++
		}
	}

	// 3. Install agents.
	agentNameToID := make(map[string]string)
	if len(req.Agents) > 0 || (req.Organization && pack.Components.Organization != nil) {
		installAgents := pack.Components.Agents
		if len(req.Agents) > 0 {
			wantSet := make(map[string]bool)
			for _, name := range req.Agents {
				wantSet[name] = true
			}
			var filtered []IntegrationAgent
			for _, a := range installAgents {
				if wantSet[a.Name] {
					filtered = append(filtered, a)
				}
			}
			installAgents = filtered
		}

		if s.agentStore != nil {
			for _, a := range installAgents {
				agent := service.Agent{
					Name:      a.Name,
					Config:    a.Config,
					CreatedBy: userEmail,
					UpdatedBy: userEmail,
				}
				if agent.Config.MaxIterations == 0 {
					agent.Config.MaxIterations = 10
				}
				if agent.Config.ToolTimeout == 0 {
					agent.Config.ToolTimeout = 60
				}
				created, err := s.agentStore.CreateAgent(ctx, agent)
				if err != nil {
					slog.Warn("install pack: create agent failed", "name", a.Name, "error", err)
					continue
				}
				if created != nil {
					agentNameToID[a.Name] = created.ID
					result.AgentsCreated++
				}
			}
		}
	}

	// 4. Install organization.
	if req.Organization && pack.Components.Organization != nil && s.organizationStore != nil {
		org := service.Organization{
			Name:        pack.Components.Organization.Name,
			Description: pack.Components.Organization.Description,
			CreatedBy:   userEmail,
			UpdatedBy:   userEmail,
		}

		created, err := s.organizationStore.CreateOrganization(ctx, org)
		if err != nil {
			slog.Warn("install pack: create org failed", "name", org.Name, "error", err)
		} else if created != nil {
			result.OrganizationID = created.ID

			if s.orgAgentStore != nil {
				var headAgentID string
				for _, rel := range pack.Components.Organization.Relationships {
					agentID, ok := agentNameToID[rel.AgentName]
					if !ok {
						continue
					}
					oa := service.OrganizationAgent{
						OrganizationID: created.ID,
						AgentID:        agentID,
						Role:           rel.Role,
						Title:          rel.Title,
						Status:         "active",
					}
					if rel.ParentAgentName != "" {
						if parentID, ok := agentNameToID[rel.ParentAgentName]; ok {
							oa.ParentAgentID = parentID
						}
					}
					s.orgAgentStore.CreateOrganizationAgent(ctx, oa)
					if rel.IsHead {
						headAgentID = agentID
					}
				}
				if headAgentID != "" {
					created.HeadAgentID = headAgentID
					created.UpdatedBy = userEmail
					s.organizationStore.UpdateOrganization(ctx, created.ID, *created)
				}
			}
		}
	}

	httpResponseJSON(w, result, http.StatusCreated)
}

// ─── User Pack CRUD ───

// CreatePackAPI handles POST /api/v1/integration-packs.
// Creates a new user pack folder with pack.json.
func (s *Server) CreatePackAPI(w http.ResponseWriter, r *http.Request) {
	packsDir := s.getPacksDir()
	if packsDir == "" {
		httpResponse(w, "packs directory not configured", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Slug        string `json:"slug"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Category    string `json:"category"`
		Version     string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}
	if req.Slug == "" || req.Name == "" {
		httpResponse(w, "slug and name are required", http.StatusBadRequest)
		return
	}
	if req.Version == "" {
		req.Version = "0.1.0"
	}

	packDir := filepath.Join(packsDir, req.Slug)
	if _, err := os.Stat(packDir); err == nil {
		httpResponse(w, fmt.Sprintf("pack %q already exists", req.Slug), http.StatusConflict)
		return
	}

	// Create directory structure.
	for _, sub := range []string{"skills", "agents", "mcp_sets"} {
		if err := os.MkdirAll(filepath.Join(packDir, sub), 0o755); err != nil {
			httpResponse(w, fmt.Sprintf("failed to create pack directory: %v", err), http.StatusInternalServerError)
			return
		}
	}

	// Write pack.json.
	meta := map[string]any{
		"slug":        req.Slug,
		"name":        req.Name,
		"description": req.Description,
		"category":    req.Category,
		"version":     req.Version,
	}
	metaJSON, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), metaJSON, 0o644); err != nil {
		httpResponse(w, fmt.Sprintf("failed to write pack.json: %v", err), http.StatusInternalServerError)
		return
	}

	// Reload packs.
	s.integrationPacks = nil
	s.loadIntegrationPacks()

	// Return the created pack.
	for i := range s.integrationPacks {
		if s.integrationPacks[i].Slug == req.Slug {
			httpResponseJSON(w, s.integrationPacks[i], http.StatusCreated)
			return
		}
	}

	httpResponseJSON(w, meta, http.StatusCreated)
}

// DeletePackAPI handles DELETE /api/v1/integration-packs/{slug}.
func (s *Server) DeletePackAPI(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	// Check it's not an embedded pack.
	for _, p := range s.integrationPacks {
		if p.Slug == slug && p.ReadOnly {
			httpResponse(w, "cannot delete a built-in pack", http.StatusForbidden)
			return
		}
	}

	packsDir := s.getPacksDir()
	if packsDir == "" {
		httpResponse(w, "packs directory not configured", http.StatusServiceUnavailable)
		return
	}

	packDir := filepath.Join(packsDir, slug)
	if _, err := os.Stat(packDir); os.IsNotExist(err) {
		httpResponse(w, fmt.Sprintf("pack %q not found", slug), http.StatusNotFound)
		return
	}

	if err := os.RemoveAll(packDir); err != nil {
		httpResponse(w, fmt.Sprintf("failed to delete pack: %v", err), http.StatusInternalServerError)
		return
	}

	// Reload.
	s.integrationPacks = nil
	s.loadIntegrationPacks()

	httpResponse(w, "deleted", http.StatusOK)
}

// AddSkillToPackAPI handles POST /api/v1/integration-packs/{slug}/skills.
// Accepts markdown or JSON body. Writes a .md file to the pack's skills/ dir.
func (s *Server) AddSkillToPackAPI(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	packDir, err := s.getUserPackDir(slug)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req struct {
		Name         string   `json:"name"`
		Description  string   `json:"description"`
		Category     string   `json:"category"`
		Tags         []string `json:"tags"`
		SystemPrompt string   `json:"system_prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	// Generate markdown file.
	sm := &skillmd.SkillMD{
		Name:        req.Name,
		Description: req.Description,
		Category:    req.Category,
		Tags:        req.Tags,
		Body:        req.SystemPrompt,
	}
	data, err := skillmd.Generate(sm, nil)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to generate skill markdown: %v", err), http.StatusInternalServerError)
		return
	}

	skillsDir := filepath.Join(packDir, "skills")
	os.MkdirAll(skillsDir, 0o755)

	filename := strings.ReplaceAll(req.Name, " ", "-") + ".md"
	if err := os.WriteFile(filepath.Join(skillsDir, filename), data, 0o644); err != nil {
		httpResponse(w, fmt.Sprintf("failed to write skill file: %v", err), http.StatusInternalServerError)
		return
	}

	s.reloadPacks()
	httpResponse(w, "skill added", http.StatusCreated)
}

// AddAgentToPackAPI handles POST /api/v1/integration-packs/{slug}/agents.
func (s *Server) AddAgentToPackAPI(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	packDir, err := s.getUserPackDir(slug)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req IntegrationAgent
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	// Generate markdown file.
	md := &agentmd.AgentMD{
		Name:                      req.Name,
		Description:               req.Config.Description,
		Provider:                  req.Config.Provider,
		Model:                     req.Config.Model,
		Skills:                    req.Config.Skills,
		MCPSets:                   req.Config.MCPSets,
		MCPs:                      req.Config.MCPs,
		BuiltinTools:              req.Config.BuiltinTools,
		MaxIterations:             req.Config.MaxIterations,
		ToolTimeout:               req.Config.ToolTimeout,
		ConfirmationRequiredTools: req.Config.ConfirmationRequiredTools,
		AvatarSeed:                req.Config.AvatarSeed,
		SystemPrompt:              req.Config.SystemPrompt,
	}
	data, err := agentmd.Generate(md)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to generate agent markdown: %v", err), http.StatusInternalServerError)
		return
	}

	agentsDir := filepath.Join(packDir, "agents")
	os.MkdirAll(agentsDir, 0o755)

	filename := strings.ReplaceAll(req.Name, " ", "-") + ".md"
	if err := os.WriteFile(filepath.Join(agentsDir, filename), data, 0o644); err != nil {
		httpResponse(w, fmt.Sprintf("failed to write agent file: %v", err), http.StatusInternalServerError)
		return
	}

	s.reloadPacks()
	httpResponse(w, "agent added", http.StatusCreated)
}

// AddMCPSetToPackAPI handles POST /api/v1/integration-packs/{slug}/mcp-sets.
func (s *Server) AddMCPSetToPackAPI(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	packDir, err := s.getUserPackDir(slug)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req IntegrationMCPSet
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to marshal mcp set: %v", err), http.StatusInternalServerError)
		return
	}

	mcpDir := filepath.Join(packDir, "mcp_sets")
	os.MkdirAll(mcpDir, 0o755)

	filename := strings.ReplaceAll(req.Name, " ", "-") + ".json"
	if err := os.WriteFile(filepath.Join(mcpDir, filename), data, 0o644); err != nil {
		httpResponse(w, fmt.Sprintf("failed to write mcp set file: %v", err), http.StatusInternalServerError)
		return
	}

	s.reloadPacks()
	httpResponse(w, "mcp set added", http.StatusCreated)
}

// RemoveFromPackAPI handles DELETE /api/v1/integration-packs/{slug}/{type}/{name}.
func (s *Server) RemoveFromPackAPI(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	entityType := r.PathValue("type")
	name := r.PathValue("name")

	packDir, err := s.getUserPackDir(slug)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Map type to subdirectory.
	subDir := entityType
	switch entityType {
	case "skills", "agents":
		// Try .md first, then .json.
		base := strings.ReplaceAll(name, " ", "-")
		mdPath := filepath.Join(packDir, subDir, base+".md")
		jsonPath := filepath.Join(packDir, subDir, base+".json")
		if err := os.Remove(mdPath); err != nil {
			if err := os.Remove(jsonPath); err != nil {
				httpResponse(w, fmt.Sprintf("file not found for %q", name), http.StatusNotFound)
				return
			}
		}
	case "mcp_sets":
		base := strings.ReplaceAll(name, " ", "-")
		jsonPath := filepath.Join(packDir, subDir, base+".json")
		if err := os.Remove(jsonPath); err != nil {
			httpResponse(w, fmt.Sprintf("file not found for %q", name), http.StatusNotFound)
			return
		}
	default:
		httpResponse(w, fmt.Sprintf("unknown type %q", entityType), http.StatusBadRequest)
		return
	}

	s.reloadPacks()
	httpResponse(w, "removed", http.StatusOK)
}

// ─── Helpers ───

// getPacksDir returns the user packs directory path.
func (s *Server) getPacksDir() string {
	if s.config.PacksDir != "" {
		return s.config.PacksDir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "at", "packs")
}

// getUserPackDir verifies a slug belongs to a writable user pack and returns its path.
func (s *Server) getUserPackDir(slug string) (string, error) {
	// Reject embedded packs.
	for _, p := range s.integrationPacks {
		if p.Slug == slug && p.ReadOnly {
			return "", fmt.Errorf("cannot modify built-in pack %q", slug)
		}
	}

	packsDir := s.getPacksDir()
	if packsDir == "" {
		return "", fmt.Errorf("packs directory not configured")
	}

	dir := filepath.Join(packsDir, slug)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return "", fmt.Errorf("pack %q not found", slug)
	}

	return dir, nil
}

// reloadPacks clears and reloads all integration packs.
func (s *Server) reloadPacks() {
	s.integrationPacks = nil
	s.loadIntegrationPacks()
}
