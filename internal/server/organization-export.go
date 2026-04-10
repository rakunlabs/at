package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/agentmd"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/skillmd"
)

// ─── Bundle Types ───

// bundleManifest holds metadata about the exported bundle.
type bundleManifest struct {
	Version    string `json:"version"`
	ExportedAt string `json:"exported_at"`
	ExportedBy string `json:"exported_by"`
}

// bundleOrganization is the portable org config (no IDs, no timestamps).
type bundleOrganization struct {
	Name                 string                   `json:"name"`
	Description          string                   `json:"description"`
	IssuePrefix          string                   `json:"issue_prefix,omitempty"`
	BudgetMonthlyCents   int64                    `json:"budget_monthly_cents,omitempty"`
	RequireBoardApproval bool                     `json:"require_board_approval_for_new_agents"`
	MaxDelegationDepth   int                      `json:"max_delegation_depth,omitempty"`
	ContainerConfig      *service.ContainerConfig `json:"container_config,omitempty"`
}

// bundleRelationship captures an org-agent membership by agent name (not ID).
type bundleRelationship struct {
	AgentName         string `json:"agent_name"`
	Role              string `json:"role,omitempty"`
	Title             string `json:"title,omitempty"`
	ParentAgentName   string `json:"parent_agent_name,omitempty"`
	Status            string `json:"status,omitempty"`
	HeartbeatSchedule string `json:"heartbeat_schedule,omitempty"`
	MemoryModel       string `json:"memory_model,omitempty"`
	MemoryProvider    string `json:"memory_provider,omitempty"`
	MemoryMethod      string `json:"memory_method,omitempty"`
	IsHead            bool   `json:"is_head,omitempty"`
}

// bundlePreview is the response for the import preview endpoint.
type bundlePreview struct {
	Organization  *bundlePreviewItem   `json:"organization"`
	Agents        []bundlePreviewItem  `json:"agents"`
	Skills        []bundlePreviewItem  `json:"skills"`
	MCPSets       []bundlePreviewItem  `json:"mcp_sets"`
	MCPServers    []bundlePreviewItem  `json:"mcp_servers"`
	Relationships []bundleRelationship `json:"relationships"`
}

// bundlePreviewItem represents a single entity in the import preview.
type bundlePreviewItem struct {
	Name       string `json:"name"`
	Conflict   string `json:"conflict,omitempty"` // "exists" or ""
	ExistingID string `json:"existing_id,omitempty"`
}

// bundleImportRequest is the request body for confirming an import.
type bundleImportRequest struct {
	Actions map[string]string `json:"actions"` // entity key -> "create_new" | "skip" | "overwrite"
}

// ─── Export ───

// ExportOrganizationBundleAPI handles GET /api/v1/organizations/{id}/export.
// Resolves all dependencies and returns a ZIP bundle.
func (s *Server) ExportOrganizationBundleAPI(w http.ResponseWriter, r *http.Request) {
	if s.organizationStore == nil || s.orgAgentStore == nil || s.agentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "organization id is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// 1. Get the organization.
	org, err := s.organizationStore.GetOrganization(ctx, id)
	if err != nil {
		slog.Error("export org bundle: get org failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get organization: %v", err), http.StatusInternalServerError)
		return
	}
	if org == nil {
		httpResponse(w, fmt.Sprintf("organization %q not found", id), http.StatusNotFound)
		return
	}

	// 2. Get all org-agent memberships.
	orgAgents, err := s.orgAgentStore.ListOrganizationAgents(ctx, id)
	if err != nil {
		slog.Error("export org bundle: list org agents failed", "org_id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list org agents: %v", err), http.StatusInternalServerError)
		return
	}

	// 3. Fetch all referenced agents and collect their dependencies.
	agentsByID := make(map[string]*service.Agent)
	skillNames := make(map[string]bool)
	mcpSetNames := make(map[string]bool)

	for _, oa := range orgAgents {
		agent, err := s.agentStore.GetAgent(ctx, oa.AgentID)
		if err != nil {
			slog.Error("export org bundle: get agent failed", "agent_id", oa.AgentID, "error", err)
			continue // skip agents that can't be fetched
		}
		if agent == nil {
			continue
		}
		agentsByID[agent.ID] = agent

		for _, sk := range agent.Config.Skills {
			skillNames[sk] = true
		}
		for _, ms := range agent.Config.MCPSets {
			mcpSetNames[ms] = true
		}
	}

	// 4. Fetch skills by name.
	var skills []service.Skill
	if s.skillStore != nil {
		for name := range skillNames {
			skill, err := s.skillStore.GetSkillByName(ctx, name)
			if err != nil {
				slog.Error("export org bundle: get skill failed", "name", name, "error", err)
				continue
			}
			if skill != nil {
				skills = append(skills, *skill)
			}
		}
	}

	// 5. Fetch MCP sets by name.
	var mcpSets []service.MCPSet
	if s.mcpSetStore != nil {
		for name := range mcpSetNames {
			set, err := s.mcpSetStore.GetMCPSetByName(ctx, name)
			if err != nil {
				slog.Error("export org bundle: get mcp set failed", "name", name, "error", err)
				continue
			}
			if set != nil {
				mcpSets = append(mcpSets, *set)
			}
		}
	}

	// 6. Build the ZIP.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// manifest.json
	userEmail := s.getUserEmail(r)
	manifest := bundleManifest{
		Version:    "1",
		ExportedAt: nowUTC(),
		ExportedBy: userEmail,
	}
	writeJSONToZip(zw, "manifest.json", manifest)

	// organization.json
	orgExport := bundleOrganization{
		Name:                 org.Name,
		Description:          org.Description,
		IssuePrefix:          org.IssuePrefix,
		BudgetMonthlyCents:   org.BudgetMonthlyCents,
		RequireBoardApproval: org.RequireBoardApproval,
		MaxDelegationDepth:   org.MaxDelegationDepth,
		ContainerConfig:      org.ContainerConfig,
	}
	writeJSONToZip(zw, "organization.json", orgExport)

	// agents/*.md
	for _, oa := range orgAgents {
		agent, ok := agentsByID[oa.AgentID]
		if !ok {
			continue
		}
		md := agentToMD(agent)
		data, err := agentmd.Generate(md)
		if err != nil {
			slog.Error("export org bundle: generate agent md failed", "name", agent.Name, "error", err)
			continue
		}
		writeBytesToZip(zw, filepath.Join("agents", agent.Name+".md"), data)
	}

	// skills/*.md
	for _, skill := range skills {
		sm := &skillmd.SkillMD{
			Name:        skill.Name,
			Description: skill.Description,
			Body:        skill.SystemPrompt,
		}
		var tools []skillmd.ToolDef
		for _, t := range skill.Tools {
			tools = append(tools, skillmd.ToolDef{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
				Handler:     t.Handler,
				HandlerType: t.HandlerType,
			})
		}
		data, err := skillmd.Generate(sm, tools)
		if err != nil {
			slog.Error("export org bundle: generate skill md failed", "name", skill.Name, "error", err)
			continue
		}
		writeBytesToZip(zw, filepath.Join("skills", skill.Name+".md"), data)
	}

	// mcp-sets/*.json
	for _, set := range mcpSets {
		export := mcpSetExportData{
			Name:        set.Name,
			Description: set.Description,
			Config:      set.Config,
			Servers:     set.Servers,
			URLs:        set.URLs,
		}
		writeJSONToZip(zw, filepath.Join("mcp-sets", set.Name+".json"), export)
	}

	// relationships.json — map agent IDs to names for portability.
	var relationships []bundleRelationship
	for _, oa := range orgAgents {
		agent, ok := agentsByID[oa.AgentID]
		if !ok {
			continue
		}
		rel := bundleRelationship{
			AgentName:         agent.Name,
			Role:              oa.Role,
			Title:             oa.Title,
			Status:            oa.Status,
			HeartbeatSchedule: oa.HeartbeatSchedule,
			MemoryModel:       oa.MemoryModel,
			MemoryProvider:    oa.MemoryProvider,
			MemoryMethod:      oa.MemoryMethod,
			IsHead:            org.HeadAgentID == agent.ID,
		}
		// Resolve parent agent name.
		if oa.ParentAgentID != "" {
			if parent, ok := agentsByID[oa.ParentAgentID]; ok {
				rel.ParentAgentName = parent.Name
			}
		}
		relationships = append(relationships, rel)
	}
	writeJSONToZip(zw, "relationships.json", relationships)

	if err := zw.Close(); err != nil {
		slog.Error("export org bundle: close zip failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to create zip: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, org.Name))
	w.WriteHeader(http.StatusOK)
	w.Write(buf.Bytes())
}

// ─── Import Preview ───

// PreviewImportBundleAPI handles POST /api/v1/organizations/import/preview.
// Parses the ZIP bundle and detects conflicts without persisting anything.
func (s *Server) PreviewImportBundleAPI(w http.ResponseWriter, r *http.Request) {
	bundle, err := s.parseBundle(r)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	preview := bundlePreview{
		Relationships: bundle.relationships,
	}

	// Check organization conflict.
	if bundle.org != nil {
		item := bundlePreviewItem{Name: bundle.org.Name}
		if s.organizationStore != nil {
			existing := s.findOrgByName(ctx, bundle.org.Name)
			if existing != nil {
				item.Conflict = "exists"
				item.ExistingID = existing.ID
			}
		}
		preview.Organization = &item
	}

	// Check agent conflicts.
	for _, a := range bundle.agents {
		item := bundlePreviewItem{Name: a.Name}
		if s.agentStore != nil {
			existing := s.findAgentByName(ctx, a.Name)
			if existing != nil {
				item.Conflict = "exists"
				item.ExistingID = existing.ID
			}
		}
		preview.Agents = append(preview.Agents, item)
	}

	// Check skill conflicts.
	for _, sk := range bundle.skills {
		item := bundlePreviewItem{Name: sk.Name}
		if s.skillStore != nil {
			existing, _ := s.skillStore.GetSkillByName(ctx, sk.Name)
			if existing != nil {
				item.Conflict = "exists"
				item.ExistingID = existing.ID
			}
		}
		preview.Skills = append(preview.Skills, item)
	}

	// Check MCP set conflicts.
	for _, ms := range bundle.mcpSets {
		item := bundlePreviewItem{Name: ms.Name}
		if s.mcpSetStore != nil {
			existing, _ := s.mcpSetStore.GetMCPSetByName(ctx, ms.Name)
			if existing != nil {
				item.Conflict = "exists"
				item.ExistingID = existing.ID
			}
		}
		preview.MCPSets = append(preview.MCPSets, item)
	}

	httpResponseJSON(w, preview, http.StatusOK)
}

// ─── Import ───

// ImportOrganizationBundleAPI handles POST /api/v1/organizations/import.
// Accepts a multipart form with:
//   - file: the ZIP bundle
//   - actions: JSON object with per-entity conflict resolution decisions
func (s *Server) ImportOrganizationBundleAPI(w http.ResponseWriter, r *http.Request) {
	if s.organizationStore == nil || s.orgAgentStore == nil || s.agentStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	bundle, err := s.parseBundle(r)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Parse actions from query or form field.
	actionsJSON := r.URL.Query().Get("actions")
	if actionsJSON == "" {
		actionsJSON = r.FormValue("actions")
	}
	actions := make(map[string]string)
	if actionsJSON != "" {
		if err := json.Unmarshal([]byte(actionsJSON), &actions); err != nil {
			httpResponse(w, fmt.Sprintf("invalid actions JSON: %v", err), http.StatusBadRequest)
			return
		}
	}

	ctx := r.Context()
	userEmail := s.getUserEmail(r)

	// getAction returns the action for an entity, defaulting to "create_new".
	getAction := func(entityType, name string) string {
		key := entityType + ":" + name
		if a, ok := actions[key]; ok {
			return a
		}
		return "create_new"
	}

	// 1. Import skills first (agents depend on them).
	skillNameMap := make(map[string]string) // old name -> new/existing name
	if s.skillStore != nil {
		for _, sk := range bundle.skills {
			action := getAction("skill", sk.Name)
			switch action {
			case "skip":
				skillNameMap[sk.Name] = sk.Name
				continue
			case "overwrite":
				existing, _ := s.skillStore.GetSkillByName(ctx, sk.Name)
				if existing != nil {
					sk.UpdatedBy = userEmail
					_, err := s.skillStore.UpdateSkill(ctx, existing.ID, sk)
					if err != nil {
						slog.Error("import bundle: overwrite skill failed", "name", sk.Name, "error", err)
					}
					skillNameMap[sk.Name] = sk.Name
					continue
				}
				// If not found, fall through to create.
				fallthrough
			default: // "create_new"
				sk.CreatedBy = userEmail
				sk.UpdatedBy = userEmail
				_, err := s.skillStore.CreateSkill(ctx, sk)
				if err != nil {
					slog.Error("import bundle: create skill failed", "name", sk.Name, "error", err)
				}
				skillNameMap[sk.Name] = sk.Name
			}
		}
	}

	// 2. Import MCP sets.
	mcpSetNameMap := make(map[string]string)
	if s.mcpSetStore != nil {
		for _, ms := range bundle.mcpSets {
			action := getAction("mcp_set", ms.Name)
			switch action {
			case "skip":
				mcpSetNameMap[ms.Name] = ms.Name
				continue
			case "overwrite":
				existing, _ := s.mcpSetStore.GetMCPSetByName(ctx, ms.Name)
				if existing != nil {
					ms.UpdatedBy = userEmail
					_, err := s.mcpSetStore.UpdateMCPSet(ctx, existing.ID, ms)
					if err != nil {
						slog.Error("import bundle: overwrite mcp set failed", "name", ms.Name, "error", err)
					}
					mcpSetNameMap[ms.Name] = ms.Name
					continue
				}
				fallthrough
			default:
				ms.CreatedBy = userEmail
				ms.UpdatedBy = userEmail
				if ms.Servers == nil {
					ms.Servers = []string{}
				}
				if ms.URLs == nil {
					ms.URLs = []string{}
				}
				_, err := s.mcpSetStore.CreateMCPSet(ctx, ms)
				if err != nil {
					slog.Error("import bundle: create mcp set failed", "name", ms.Name, "error", err)
				}
				mcpSetNameMap[ms.Name] = ms.Name
			}
		}
	}

	// 3. Import agents.
	agentNameToID := make(map[string]string) // agent name -> created/existing agent ID
	for _, agent := range bundle.agents {
		action := getAction("agent", agent.Name)
		switch action {
		case "skip":
			existing := s.findAgentByName(ctx, agent.Name)
			if existing != nil {
				agentNameToID[agent.Name] = existing.ID
			}
			continue
		case "overwrite":
			existing := s.findAgentByName(ctx, agent.Name)
			if existing != nil {
				agent.UpdatedBy = userEmail
				updated, err := s.agentStore.UpdateAgent(ctx, existing.ID, agent)
				if err != nil {
					slog.Error("import bundle: overwrite agent failed", "name", agent.Name, "error", err)
				} else if updated != nil {
					agentNameToID[agent.Name] = updated.ID
				}
				continue
			}
			fallthrough
		default:
			if agent.Config.MaxIterations == 0 {
				agent.Config.MaxIterations = 10
			}
			if agent.Config.ToolTimeout == 0 {
				agent.Config.ToolTimeout = 60
			}
			agent.CreatedBy = userEmail
			agent.UpdatedBy = userEmail
			created, err := s.agentStore.CreateAgent(ctx, agent)
			if err != nil {
				slog.Error("import bundle: create agent failed", "name", agent.Name, "error", err)
			} else if created != nil {
				agentNameToID[agent.Name] = created.ID
			}
		}
	}

	// 4. Import organization.
	var orgID string
	if bundle.org != nil {
		action := getAction("organization", bundle.org.Name)
		switch action {
		case "skip":
			existing := s.findOrgByName(ctx, bundle.org.Name)
			if existing != nil {
				orgID = existing.ID
			}
		case "overwrite":
			existing := s.findOrgByName(ctx, bundle.org.Name)
			if existing != nil {
				bundle.org.UpdatedBy = userEmail
				updated, err := s.organizationStore.UpdateOrganization(ctx, existing.ID, *bundle.org)
				if err != nil {
					slog.Error("import bundle: overwrite org failed", "name", bundle.org.Name, "error", err)
				} else if updated != nil {
					orgID = updated.ID
				}
			}
		default:
			bundle.org.CreatedBy = userEmail
			bundle.org.UpdatedBy = userEmail
			created, err := s.organizationStore.CreateOrganization(ctx, *bundle.org)
			if err != nil {
				slog.Error("import bundle: create org failed", "name", bundle.org.Name, "error", err)
			} else if created != nil {
				orgID = created.ID
			}
		}
	}

	// 5. Create org-agent memberships and set head agent.
	var headAgentID string
	if orgID != "" {
		for _, rel := range bundle.relationships {
			agentID, ok := agentNameToID[rel.AgentName]
			if !ok {
				continue
			}

			oa := service.OrganizationAgent{
				OrganizationID:    orgID,
				AgentID:           agentID,
				Role:              rel.Role,
				Title:             rel.Title,
				Status:            rel.Status,
				HeartbeatSchedule: rel.HeartbeatSchedule,
				MemoryModel:       rel.MemoryModel,
				MemoryProvider:    rel.MemoryProvider,
				MemoryMethod:      rel.MemoryMethod,
			}

			// Resolve parent agent ID.
			if rel.ParentAgentName != "" {
				if parentID, ok := agentNameToID[rel.ParentAgentName]; ok {
					oa.ParentAgentID = parentID
				}
			}

			_, err := s.orgAgentStore.CreateOrganizationAgent(ctx, oa)
			if err != nil {
				slog.Error("import bundle: create org-agent failed",
					"org_id", orgID, "agent", rel.AgentName, "error", err)
			}

			if rel.IsHead {
				headAgentID = agentID
			}
		}

		// Set head agent on the org.
		if headAgentID != "" {
			org, _ := s.organizationStore.GetOrganization(ctx, orgID)
			if org != nil {
				org.HeadAgentID = headAgentID
				org.UpdatedBy = userEmail
				s.organizationStore.UpdateOrganization(ctx, orgID, *org)
			}
		}
	}

	result := map[string]any{
		"organization_id":   orgID,
		"agents_imported":   len(agentNameToID),
		"skills_imported":   len(skillNameMap),
		"mcp_sets_imported": len(mcpSetNameMap),
	}

	httpResponseJSON(w, result, http.StatusCreated)
}

// ─── Internal Helpers ───

// parsedBundle holds all entities extracted from a ZIP bundle.
type parsedBundle struct {
	org           *service.Organization
	agents        []service.Agent
	skills        []service.Skill
	mcpSets       []service.MCPSet
	mcpServers    []service.MCPServer
	relationships []bundleRelationship
}

// parseBundle reads a ZIP from the request body and extracts all entities.
func (s *Server) parseBundle(r *http.Request) (*parsedBundle, error) {
	// Support both direct body and multipart form.
	var zipData []byte
	var err error

	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/") {
		if err := r.ParseMultipartForm(50 << 20); err != nil { // 50 MB limit
			return nil, fmt.Errorf("failed to parse multipart form: %w", err)
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			return nil, fmt.Errorf("file field is required: %w", err)
		}
		defer file.Close()
		zipData, err = io.ReadAll(io.LimitReader(file, 50<<20))
		if err != nil {
			return nil, fmt.Errorf("failed to read file: %w", err)
		}
	} else {
		zipData, err = io.ReadAll(io.LimitReader(r.Body, 50<<20))
		if err != nil {
			return nil, fmt.Errorf("failed to read body: %w", err)
		}
	}

	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("invalid zip file: %w", err)
	}

	bundle := &parsedBundle{}

	for _, f := range reader.File {
		data, err := readZipFile(f)
		if err != nil {
			slog.Error("parse bundle: read file failed", "name", f.Name, "error", err)
			continue
		}

		switch {
		case f.Name == "organization.json":
			var org bundleOrganization
			if err := json.Unmarshal(data, &org); err != nil {
				return nil, fmt.Errorf("invalid organization.json: %w", err)
			}
			bundle.org = &service.Organization{
				Name:                 org.Name,
				Description:          org.Description,
				IssuePrefix:          org.IssuePrefix,
				BudgetMonthlyCents:   org.BudgetMonthlyCents,
				RequireBoardApproval: org.RequireBoardApproval,
				MaxDelegationDepth:   org.MaxDelegationDepth,
				ContainerConfig:      org.ContainerConfig,
			}

		case f.Name == "relationships.json":
			if err := json.Unmarshal(data, &bundle.relationships); err != nil {
				return nil, fmt.Errorf("invalid relationships.json: %w", err)
			}

		case strings.HasPrefix(f.Name, "agents/") && strings.HasSuffix(f.Name, ".md"):
			parsed, err := agentmd.Parse(data)
			if err != nil {
				slog.Error("parse bundle: parse agent md failed", "file", f.Name, "error", err)
				continue
			}
			agent := mdToAgent(parsed)
			bundle.agents = append(bundle.agents, agent)

		case strings.HasPrefix(f.Name, "skills/") && strings.HasSuffix(f.Name, ".md"):
			parsed, tools, err := skillmd.ParseWithTools(data)
			if err != nil {
				slog.Error("parse bundle: parse skill md failed", "file", f.Name, "error", err)
				continue
			}
			skill := service.Skill{
				Name:         parsed.Name,
				Description:  parsed.Description,
				SystemPrompt: parsed.Body,
			}
			for _, t := range tools {
				skill.Tools = append(skill.Tools, service.Tool{
					Name:        t.Name,
					Description: t.Description,
					InputSchema: t.InputSchema,
					Handler:     t.Handler,
					HandlerType: t.HandlerType,
				})
			}
			bundle.skills = append(bundle.skills, skill)

		case strings.HasPrefix(f.Name, "mcp-sets/") && strings.HasSuffix(f.Name, ".json"):
			var export mcpSetExportData
			if err := json.Unmarshal(data, &export); err != nil {
				slog.Error("parse bundle: parse mcp set failed", "file", f.Name, "error", err)
				continue
			}
			bundle.mcpSets = append(bundle.mcpSets, service.MCPSet{
				Name:        export.Name,
				Description: export.Description,
				Config:      export.Config,
				Servers:     export.Servers,
				URLs:        export.URLs,
			})

		case strings.HasPrefix(f.Name, "mcp-servers/") && strings.HasSuffix(f.Name, ".json"):
			var export mcpServerExportData
			if err := json.Unmarshal(data, &export); err != nil {
				slog.Error("parse bundle: parse mcp server failed", "file", f.Name, "error", err)
				continue
			}
			bundle.mcpServers = append(bundle.mcpServers, service.MCPServer{
				Name:        export.Name,
				Description: export.Description,
				Config:      export.Config,
				Servers:     export.Servers,
				URLs:        export.URLs,
			})
		}
	}

	return bundle, nil
}

// findOrgByName searches for an organization by name.
func (s *Server) findOrgByName(ctx context.Context, name string) *service.Organization {
	if s.organizationStore == nil {
		return nil
	}
	result, err := s.organizationStore.ListOrganizations(ctx, nil)
	if err != nil || result == nil {
		return nil
	}
	for i := range result.Data {
		if result.Data[i].Name == name {
			return &result.Data[i]
		}
	}
	return nil
}

// findAgentByName searches for an agent by name.
func (s *Server) findAgentByName(ctx context.Context, name string) *service.Agent {
	if s.agentStore == nil {
		return nil
	}
	result, err := s.agentStore.ListAgents(ctx, nil)
	if err != nil || result == nil {
		return nil
	}
	for i := range result.Data {
		if result.Data[i].Name == name {
			return &result.Data[i]
		}
	}
	return nil
}

// writeJSONToZip adds a JSON file to the zip archive.
func writeJSONToZip(zw *zip.Writer, name string, v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		slog.Error("marshal zip entry failed", "name", name, "error", err)
		return
	}
	writeBytesToZip(zw, name, data)
}

// writeBytesToZip adds a raw file to the zip archive.
func writeBytesToZip(zw *zip.Writer, name string, data []byte) {
	fw, err := zw.Create(name)
	if err != nil {
		slog.Error("create zip entry failed", "name", name, "error", err)
		return
	}
	fw.Write(data)
}

// readZipFile reads the contents of a zip file entry.
func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, 10<<20)) // 10 MB per file limit
}

// nowUTC returns the current time in UTC as an RFC3339 string.
func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}
