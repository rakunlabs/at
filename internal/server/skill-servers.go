package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/skillmd"
	"github.com/rakunlabs/query"
)

// ─── Skill Server CRUD API ───

func (s *Server) ListSkillServersAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillServerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.skillServerStore.ListSkillServers(r.Context(), q)
	if err != nil {
		slog.Error("list skill servers failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list skill servers: %v", err), http.StatusInternalServerError)
		return
	}
	if records == nil {
		records = &service.ListResult[service.SkillServer]{Data: []service.SkillServer{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

func (s *Server) GetSkillServerAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillServerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "skill server id is required", http.StatusBadRequest)
		return
	}

	record, err := s.skillServerStore.GetSkillServer(r.Context(), id)
	if err != nil {
		slog.Error("get skill server failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get skill server: %v", err), http.StatusInternalServerError)
		return
	}
	if record == nil {
		httpResponse(w, fmt.Sprintf("skill server %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

func (s *Server) CreateSkillServerAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillServerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.SkillServer
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if err := normalizeSkillServer(&req); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	req.CreatedBy = userEmail
	req.UpdatedBy = userEmail

	record, err := s.skillServerStore.CreateSkillServer(r.Context(), req)
	if err != nil {
		slog.Error("create skill server failed", "name", req.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create skill server: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

func (s *Server) UpdateSkillServerAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillServerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "skill server id is required", http.StatusBadRequest)
		return
	}

	var req service.SkillServer
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if err := normalizeSkillServer(&req); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}

	req.UpdatedBy = s.getUserEmail(r)

	record, err := s.skillServerStore.UpdateSkillServer(r.Context(), id, req)
	if err != nil {
		slog.Error("update skill server failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update skill server: %v", err), http.StatusInternalServerError)
		return
	}
	if record == nil {
		httpResponse(w, fmt.Sprintf("skill server %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

func (s *Server) DeleteSkillServerAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillServerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "skill server id is required", http.StatusBadRequest)
		return
	}

	if err := s.skillServerStore.DeleteSkillServer(r.Context(), id); err != nil {
		slog.Error("delete skill server failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete skill server: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, map[string]string{"status": "deleted"}, http.StatusOK)
}

func normalizeSkillServer(srv *service.SkillServer) error {
	srv.Name = strings.TrimSpace(srv.Name)
	if srv.Name == "" {
		return fmt.Errorf("name is required")
	}

	srv.Mode = strings.TrimSpace(srv.Mode)
	if srv.Mode == "" {
		srv.Mode = service.SkillServerModePackage
	}
	switch srv.Mode {
	case service.SkillServerModePackage, service.SkillServerModeTools, service.SkillServerModeBoth:
	default:
		return fmt.Errorf("mode must be one of: package, tools, both")
	}

	seen := map[string]bool{}
	out := make([]string, 0, len(srv.Skills))
	for _, ref := range srv.Skills {
		ref = strings.TrimSpace(ref)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	if out == nil {
		out = []string{}
	}
	srv.Skills = out

	return nil
}

// ─── Skill Server MCP Endpoint ───

func (s *Server) SkillServerMCPSSEHandler(w http.ResponseWriter, r *http.Request) {
	if auth, errMsg := s.authenticateRequest(r); auth == nil {
		httpResponse(w, errMsg, http.StatusUnauthorized)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		httpResponse(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", r.URL.Path)
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) SkillServerMCPHandler(w http.ResponseWriter, r *http.Request) {
	if s.skillServerStore == nil {
		httpResponse(w, "skill server store not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		httpResponse(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	auth, errMsg := s.authenticateRequest(r)
	if auth == nil {
		httpResponse(w, errMsg, http.StatusUnauthorized)
		return
	}

	name := r.PathValue("name")
	if name == "" {
		httpResponse(w, "skill server name is required", http.StatusBadRequest)
		return
	}
	if auth.token != nil {
		mcpMode := service.ResolveAccessMode(auth.token.AllowedRAGMCPsMode, auth.token.AllowedRAGMCPs)
		if mcpMode == service.AccessModeNone {
			httpResponse(w, "token does not have access to any skill servers", http.StatusForbidden)
			return
		}
		if mcpMode == service.AccessModeList && !slices.Contains(auth.token.AllowedRAGMCPs, name) {
			httpResponse(w, fmt.Sprintf("token does not have access to skill server %q", name), http.StatusForbidden)
			return
		}
	}

	srv, err := s.skillServerStore.GetSkillServerByName(r.Context(), name)
	if err != nil {
		slog.Error("get skill server failed", "name", name, "error", err)
		httpResponse(w, "internal error looking up skill server", http.StatusInternalServerError)
		return
	}
	if srv == nil {
		httpResponse(w, fmt.Sprintf("skill server %q not found", name), http.StatusNotFound)
		return
	}

	var req service.MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	switch req.Method {
	case "initialize":
		s.skillServerMCPInitialize(w, req, srv)
	case "notifications/initialized":
		w.WriteHeader(http.StatusOK)
	case "tools/list":
		s.skillServerMCPListTools(w, r.Context(), req, srv)
	case "tools/call":
		s.skillServerMCPCallTool(w, r, req, srv)
	default:
		mcpError(w, req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func (s *Server) skillServerMCPInitialize(w http.ResponseWriter, req service.MCPRequest, srv *service.SkillServer) {
	mcpResult(w, req.ID, map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]string{
			"name":    fmt.Sprintf("at-skill-server-%s", srv.Name),
			"version": "1.0.0",
		},
	})
}

func (s *Server) skillServerMCPListTools(w http.ResponseWriter, ctx context.Context, req service.MCPRequest, srv *service.SkillServer) {
	var tools []service.Tool
	seen := map[string]bool{}
	if skillServerAllowsPackage(srv) {
		for _, t := range skillServerPackageTools() {
			seen[t.Name] = true
			tools = append(tools, t)
		}
	}

	if skillServerAllowsTools(srv) {
		skills, missing := s.resolveSkillServerSkills(ctx, srv)
		for _, ref := range missing {
			slog.Warn("skill server: referenced skill not found", "server", srv.Name, "skill", ref)
		}
		for _, sk := range skills {
			for _, t := range sk.Tools {
				if t.Name == "" || seen[t.Name] {
					continue
				}
				seen[t.Name] = true
				tools = append(tools, service.Tool{
					Name:        t.Name,
					Description: t.Description,
					InputSchema: t.InputSchema,
				})
			}
		}
	}

	mcpResult(w, req.ID, map[string]any{"tools": tools})
}

func (s *Server) skillServerMCPCallTool(w http.ResponseWriter, r *http.Request, req service.MCPRequest, srv *service.SkillServer) {
	paramsRaw, err := json.Marshal(req.Params)
	if err != nil {
		mcpError(w, req.ID, -32602, "invalid params")
		return
	}

	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil {
		mcpError(w, req.ID, -32602, fmt.Sprintf("invalid params: %v", err))
		return
	}
	if params.Name == "" {
		mcpError(w, req.ID, -32602, "tool name is required")
		return
	}

	if skillServerAllowsPackage(srv) && isSkillServerPackageTool(params.Name) {
		result, err := s.callSkillServerPackageTool(r.Context(), srv, params.Name, params.Arguments)
		if err != nil {
			mcpError(w, req.ID, -32000, err.Error())
			return
		}
		mcpTextResult(w, req.ID, result)
		return
	}

	if skillServerAllowsTools(srv) {
		skills, _ := s.resolveSkillServerSkills(r.Context(), srv)
		for _, sk := range skills {
			for i := range sk.Tools {
				if sk.Tools[i].Name != params.Name {
					continue
				}
				result, err := s.executeSkillTool(r.Context(), &sk.Tools[i], params.Arguments)
				if err != nil {
					mcpError(w, req.ID, -32000, fmt.Sprintf("skill tool execution failed: %v", err))
					return
				}
				mcpTextResult(w, req.ID, result)
				return
			}
		}
	}

	mcpError(w, req.ID, -32602, fmt.Sprintf("unknown tool: %s", params.Name))
}

func skillServerPackageTools() []service.Tool {
	return []service.Tool{
		{
			Name:        "skill_server_list",
			Description: "List the curated skills published by this Skill Server. Returns metadata and missing references.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "skill_server_get",
			Description: "Get one published skill's metadata and tool summaries. Does not include handler source; use skill_server_export_json or skill_server_export_skillmd to install it elsewhere.",
			InputSchema: skillServerSkillArgSchema(),
		},
		{
			Name:        "skill_server_export_json",
			Description: "Export one published skill as AT portable JSON, including tool handlers. Use this with AT's skill_import tool on another instance.",
			InputSchema: skillServerSkillArgSchema(),
		},
		{
			Name:        "skill_server_export_skillmd",
			Description: "Export one published skill as an Agent Skills compatible SKILL.md document, including an embedded Tools section when handlers exist.",
			InputSchema: skillServerSkillArgSchema(),
		},
	}
}

func skillServerSkillArgSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"skill": map[string]any{"type": "string", "description": "Published skill name or ID"},
		},
		"required": []string{"skill"},
	}
}

func isSkillServerPackageTool(name string) bool {
	switch name {
	case "skill_server_list", "skill_server_get", "skill_server_export_json", "skill_server_export_skillmd":
		return true
	default:
		return false
	}
}

func skillServerAllowsPackage(srv *service.SkillServer) bool {
	mode := srv.Mode
	if mode == "" {
		mode = service.SkillServerModePackage
	}
	return mode == service.SkillServerModePackage || mode == service.SkillServerModeBoth
}

func skillServerAllowsTools(srv *service.SkillServer) bool {
	return srv.Mode == service.SkillServerModeTools || srv.Mode == service.SkillServerModeBoth
}

func (s *Server) callSkillServerPackageTool(ctx context.Context, srv *service.SkillServer, toolName string, args map[string]any) (string, error) {
	switch toolName {
	case "skill_server_list":
		skills, missing := s.resolveSkillServerSkills(ctx, srv)
		out := map[string]any{
			"server": map[string]any{
				"name":        srv.Name,
				"description": srv.Description,
				"mode":        srv.Mode,
			},
			"skills":  skillServerSkillSummaries(skills),
			"missing": missing,
		}
		return marshalIndentString(out)
	case "skill_server_get":
		skill, err := s.skillServerSkillFromArgs(ctx, srv, args)
		if err != nil {
			return "", err
		}
		return marshalIndentString(skillServerSkillDetail(skill))
	case "skill_server_export_json":
		skill, err := s.skillServerSkillFromArgs(ctx, srv, args)
		if err != nil {
			return "", err
		}
		return marshalIndentString(skillToExportData(skill))
	case "skill_server_export_skillmd":
		skill, err := s.skillServerSkillFromArgs(ctx, srv, args)
		if err != nil {
			return "", err
		}
		data, err := skillToMarkdown(skill)
		if err != nil {
			return "", err
		}
		return string(data), nil
	default:
		return "", fmt.Errorf("unknown package tool: %s", toolName)
	}
}

func (s *Server) resolveSkillServerSkills(ctx context.Context, srv *service.SkillServer) ([]service.Skill, []string) {
	if s.skillStore == nil {
		return nil, srv.Skills
	}

	seen := map[string]bool{}
	var skills []service.Skill
	var missing []string
	for _, ref := range srv.Skills {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		skill, err := s.getSkillByIDOrName(ctx, ref)
		if err != nil || skill == nil {
			missing = append(missing, ref)
			if err != nil {
				slog.Warn("skill server: skill lookup failed", "server", srv.Name, "skill", ref, "error", err)
			}
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

	return skills, missing
}

func (s *Server) getSkillByIDOrName(ctx context.Context, ref string) (*service.Skill, error) {
	skill, err := s.skillStore.GetSkill(ctx, ref)
	if err != nil {
		return nil, err
	}
	if skill != nil {
		return skill, nil
	}
	return s.skillStore.GetSkillByName(ctx, ref)
}

func (s *Server) skillServerSkillFromArgs(ctx context.Context, srv *service.SkillServer, args map[string]any) (*service.Skill, error) {
	want, _ := args["skill"].(string)
	want = strings.TrimSpace(want)
	if want == "" {
		return nil, fmt.Errorf("skill is required")
	}

	skills, _ := s.resolveSkillServerSkills(ctx, srv)
	for i := range skills {
		if skills[i].ID == want || skills[i].Name == want {
			return &skills[i], nil
		}
	}
	return nil, fmt.Errorf("skill %q is not published by skill server %q", want, srv.Name)
}

func skillServerSkillSummaries(skills []service.Skill) []map[string]any {
	out := make([]map[string]any, 0, len(skills))
	for _, skill := range skills {
		out = append(out, map[string]any{
			"id":          skill.ID,
			"name":        skill.Name,
			"description": skill.Description,
			"category":    skill.Category,
			"tags":        skill.Tags,
			"tool_count":  len(skill.Tools),
		})
	}
	return out
}

func skillServerSkillDetail(skill *service.Skill) map[string]any {
	tools := make([]map[string]any, 0, len(skill.Tools))
	for _, tool := range skill.Tools {
		tools = append(tools, map[string]any{
			"name":         tool.Name,
			"description":  tool.Description,
			"input_schema": tool.InputSchema,
			"handler_type": tool.HandlerType,
		})
	}

	return map[string]any{
		"id":            skill.ID,
		"name":          skill.Name,
		"description":   skill.Description,
		"category":      skill.Category,
		"tags":          skill.Tags,
		"system_prompt": skill.SystemPrompt,
		"tools":         tools,
	}
}

func skillToExportData(skill *service.Skill) skillExportData {
	return skillExportData{
		Name:         skill.Name,
		Description:  skill.Description,
		SystemPrompt: skill.SystemPrompt,
		Tools:        skill.Tools,
	}
}

func skillToMarkdown(skill *service.Skill) ([]byte, error) {
	sm := &skillmd.SkillMD{
		Name:        skill.Name,
		Description: skill.Description,
		Category:    skill.Category,
		Tags:        skill.Tags,
		Body:        skill.SystemPrompt,
	}

	tools := make([]skillmd.ToolDef, 0, len(skill.Tools))
	for _, t := range skill.Tools {
		tools = append(tools, skillmd.ToolDef{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
			Handler:     t.Handler,
			HandlerType: t.HandlerType,
		})
	}

	return skillmd.Generate(sm, tools)
}

func marshalIndentString(v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal response: %w", err)
	}
	return string(data), nil
}

func mcpTextResult(w http.ResponseWriter, id int, text string) {
	mcpResult(w, id, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
	})
}
