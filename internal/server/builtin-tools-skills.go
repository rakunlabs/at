package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// ─── Skill Management Tool Executors ───

// execSkillList lists installed skills and available templates.
func (s *Server) execSkillList(ctx context.Context, args map[string]any) (string, error) {
	result := map[string]any{}

	// List installed skills from store.
	if s.skillStore != nil {
		skills, err := s.skillStore.ListSkills(ctx, nil)
		if err != nil {
			return "", fmt.Errorf("failed to list skills: %w", err)
		}

		type skillSummary struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description,omitempty"`
			ToolCount   int    `json:"tool_count"`
			CreatedAt   string `json:"created_at"`
		}

		summaries := make([]skillSummary, len(skills.Data))
		for i, sk := range skills.Data {
			summaries[i] = skillSummary{
				ID:          sk.ID,
				Name:        sk.Name,
				Description: sk.Description,
				ToolCount:   len(sk.Tools),
				CreatedAt:   sk.CreatedAt,
			}
		}
		result["installed_skills"] = summaries
		result["installed_count"] = skills.Meta.Total
	}

	// List available templates.
	category, _ := args["category"].(string)

	type templateSummary struct {
		Slug        string   `json:"slug"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Category    string   `json:"category"`
		Tags        []string `json:"tags,omitempty"`
	}

	var templates []templateSummary
	for _, t := range s.skillTemplates {
		if category != "" && t.Category != category {
			continue
		}
		templates = append(templates, templateSummary{
			Slug:        t.Slug,
			Name:        t.Name,
			Description: t.Description,
			Category:    t.Category,
			Tags:        t.Tags,
		})
	}

	if templates == nil {
		templates = []templateSummary{}
	}

	result["available_templates"] = templates
	result["template_count"] = len(templates)

	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data), nil
}

// execSkillInstallTemplate installs a skill from a built-in template.
func (s *Server) execSkillInstallTemplate(ctx context.Context, args map[string]any) (string, error) {
	if s.skillStore == nil {
		return "", fmt.Errorf("skill store not configured")
	}

	slug, _ := args["slug"].(string)
	if slug == "" {
		return "", fmt.Errorf("slug is required")
	}

	// Find the template.
	var tmpl *SkillTemplate
	for i := range s.skillTemplates {
		if s.skillTemplates[i].Slug == slug {
			tmpl = &s.skillTemplates[i]
			break
		}
	}
	if tmpl == nil {
		return "", fmt.Errorf("template %q not found", slug)
	}

	// Create the skill from the template.
	skill := service.Skill{
		Name:         tmpl.Skill.Name,
		Description:  tmpl.Skill.Description,
		SystemPrompt: tmpl.Skill.SystemPrompt,
		Tools:        tmpl.Skill.Tools,
	}

	record, err := s.skillStore.CreateSkill(ctx, skill)
	if err != nil {
		return "", fmt.Errorf("failed to install skill template: %w", err)
	}

	out := map[string]any{
		"status":  "installed",
		"skill":   record,
		"message": fmt.Sprintf("Skill %q installed from template %q", record.Name, slug),
	}

	if len(tmpl.RequiredVariables) > 0 {
		vars := make([]string, len(tmpl.RequiredVariables))
		for i, v := range tmpl.RequiredVariables {
			vars[i] = v.Key + " - " + v.Description
		}
		out["required_variables"] = vars
		out["setup_note"] = "Configure the required variables in AT Settings > Variables before using this skill"
	}

	data, _ := json.MarshalIndent(out, "", "  ")
	return string(data), nil
}

// ─── Skill Authoring Tool Executors ───
//
// These executors extend the management MCP from "install canned templates"
// to "author and edit skills end-to-end". Underneath they call the same
// SkillStorer methods and skillmd parser the HTTP handlers use, so any
// invariant enforced there (e.g. CreateSkill timestamping) applies here too.

// execSkillGet returns a single skill's full record (system prompt + tools
// with their handlers). The handler source is included verbatim so the
// agent can clone-and-edit a skill before calling skill_update.
func (s *Server) execSkillGet(ctx context.Context, args map[string]any) (string, error) {
	if s.skillStore == nil {
		return "", fmt.Errorf("skill store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	record, err := s.skillStore.GetSkill(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get skill %q: %w", id, err)
	}
	if record == nil {
		return "", fmt.Errorf("skill %q not found", id)
	}
	out, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal skill: %w", err)
	}
	return string(out), nil
}

// decodeSkillTools coerces an args["tools"] value (typically []any from
// a JSON-decoded MCP arguments map) into []service.Tool. Accepts both
// snake_case (input_schema, handler_type) — the tool definition we
// publish — and the field names already used in service.Tool's JSON tags.
func decodeSkillTools(raw any) ([]service.Tool, error) {
	if raw == nil {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	// Intermediate shape that accepts both `inputSchema` and `input_schema`.
	type rawTool struct {
		Name         string         `json:"name"`
		Description  string         `json:"description"`
		InputSchema  map[string]any `json:"inputSchema"`
		InputSchema2 map[string]any `json:"input_schema"`
		Handler      string         `json:"handler"`
		HandlerType  string         `json:"handler_type"`
	}
	var rs []rawTool
	if err := json.Unmarshal(data, &rs); err != nil {
		return nil, fmt.Errorf("must be an array of tool objects: %w", err)
	}
	out := make([]service.Tool, 0, len(rs))
	for _, r := range rs {
		schema := r.InputSchema
		if schema == nil {
			schema = r.InputSchema2
		}
		out = append(out, service.Tool{
			Name:        r.Name,
			Description: r.Description,
			InputSchema: schema,
			Handler:     r.Handler,
			HandlerType: r.HandlerType,
		})
	}
	return out, nil
}

// decodeStringSlice coerces an args[k] value into []string. Returns nil
// (with no error) if raw is nil so callers can distinguish "field not
// provided" (nil) from "field provided as empty list" (zero-length slice).
func decodeStringSlice(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	var out []string
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("must be an array of strings: %w", err)
	}
	return out, nil
}

// execSkillCreate creates a new custom skill from the agent's spec.
func (s *Server) execSkillCreate(ctx context.Context, args map[string]any) (string, error) {
	if s.skillStore == nil {
		return "", fmt.Errorf("skill store not configured")
	}

	name, _ := args["name"].(string)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}

	tools, err := decodeSkillTools(args["tools"])
	if err != nil {
		return "", fmt.Errorf("tools: %w", err)
	}

	tags, err := decodeStringSlice(args["tags"])
	if err != nil {
		return "", fmt.Errorf("tags: %w", err)
	}

	skill := service.Skill{
		Name:         name,
		Description:  stringArg(args, "description"),
		Category:     stringArg(args, "category"),
		Tags:         tags,
		SystemPrompt: stringArg(args, "system_prompt"),
		Tools:        tools,
		CreatedBy:    "mcp",
		UpdatedBy:    "mcp",
	}

	record, err := s.skillStore.CreateSkill(ctx, skill)
	if err != nil {
		return "", fmt.Errorf("create skill: %w", err)
	}

	out, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal skill: %w", err)
	}
	return string(out), nil
}

// execSkillUpdate replaces an existing skill's full state. We use full
// replacement (rather than partial merge) because Skill.Tools is a slice
// of definitions where "remove a tool" is a meaningful operation that
// can't be expressed by a merge. Agents are expected to fetch with
// skill_get, mutate, then submit.
func (s *Server) execSkillUpdate(ctx context.Context, args map[string]any) (string, error) {
	if s.skillStore == nil {
		return "", fmt.Errorf("skill store not configured")
	}

	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	name, _ := args["name"].(string)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}

	tools, err := decodeSkillTools(args["tools"])
	if err != nil {
		return "", fmt.Errorf("tools: %w", err)
	}

	tags, err := decodeStringSlice(args["tags"])
	if err != nil {
		return "", fmt.Errorf("tags: %w", err)
	}

	skill := service.Skill{
		Name:         name,
		Description:  stringArg(args, "description"),
		Category:     stringArg(args, "category"),
		Tags:         tags,
		SystemPrompt: stringArg(args, "system_prompt"),
		Tools:        tools,
		Version:      stringArg(args, "version"),
		Author:       stringArg(args, "author"),
		License:      stringArg(args, "license"),
		UpdatedBy:    "mcp",
	}

	// Preserve system-managed provenance and unprovided metadata so a
	// full-replacement update from an agent doesn't wipe attribution.
	if existing, err := s.skillStore.GetSkill(ctx, id); err == nil && existing != nil {
		if skill.Version == "" {
			skill.Version = existing.Version
		}
		if skill.Author == "" {
			skill.Author = existing.Author
		}
		if skill.License == "" {
			skill.License = existing.License
		}
		skill.SourceURL = existing.SourceURL
		skill.SourceChecksum = existing.SourceChecksum
	}

	record, err := s.skillStore.UpdateSkill(ctx, id, skill)
	if err != nil {
		return "", fmt.Errorf("update skill: %w", err)
	}
	if record == nil {
		return "", fmt.Errorf("skill %q not found", id)
	}

	out, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal skill: %w", err)
	}
	return string(out), nil
}

// execSkillDelete removes a skill. Agents currently referencing the
// skill (by name in agent.skills) will lose its tools on their next run;
// no cascade fixup is performed.
func (s *Server) execSkillDelete(ctx context.Context, args map[string]any) (string, error) {
	if s.skillStore == nil {
		return "", fmt.Errorf("skill store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := s.skillStore.DeleteSkill(ctx, id); err != nil {
		return "", fmt.Errorf("delete skill %q: %w", id, err)
	}
	return fmt.Sprintf(`{"status":"deleted","id":%q}`, id), nil
}

// execSkillTestHandler runs a single tool handler in-process without
// persisting it. Mirrors the TestHandlerAPI HTTP endpoint so agents can
// iterate on a handler before saving the skill. We deliberately do NOT
// allow setting a timeout here; the JS/bash sandboxes already enforce
// their own bounds, and skill handlers shouldn't be long-running.
func (s *Server) execSkillTestHandler(ctx context.Context, args map[string]any) (string, error) {
	handler, _ := args["handler"].(string)
	if handler == "" {
		return "", fmt.Errorf("handler is required")
	}

	handlerType, _ := args["handler_type"].(string)

	var arguments map[string]any
	if raw, ok := args["arguments"].(map[string]any); ok {
		arguments = raw
	} else {
		arguments = map[string]any{}
	}

	start := time.Now()
	var (
		result  string
		execErr error
	)

	if handlerType == "bash" {
		var varLister workflow.VarLister
		if s.variableStore != nil {
			varLister = func() (map[string]string, error) {
				vars, err := s.variableStore.ListVariables(context.Background(), nil)
				if err != nil {
					return nil, err
				}
				m := make(map[string]string, len(vars.Data))
				for _, v := range vars.Data {
					m[v.Key] = v.Value
				}
				return m, nil
			}
		}
		result, execErr = workflow.ExecuteBashHandler(ctx, handler, arguments, varLister, 0)
	} else {
		var varLookup workflow.VarLookup
		if s.variableStore != nil {
			varLookup = func(key string) (string, error) {
				v, err := s.variableStore.GetVariableByKey(context.Background(), key)
				if err != nil {
					return "", err
				}
				if v == nil {
					return "", fmt.Errorf("variable %q not found", key)
				}
				return v.Value, nil
			}
		}
		result, execErr = workflow.ExecuteJSHandler(handler, arguments, varLookup)
	}

	resp := map[string]any{
		"result":      result,
		"duration_ms": time.Since(start).Milliseconds(),
	}
	if execErr != nil {
		resp["error"] = execErr.Error()
	}
	out, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal response: %w", err)
	}
	return string(out), nil
}

// execSkillExport returns a portable JSON document for a skill (no IDs
// or timestamps). Round-trips through skill_import on any AT instance.
func (s *Server) execSkillExport(ctx context.Context, args map[string]any) (string, error) {
	if s.skillStore == nil {
		return "", fmt.Errorf("skill store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	record, err := s.skillStore.GetSkill(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get skill %q: %w", id, err)
	}
	if record == nil {
		return "", fmt.Errorf("skill %q not found", id)
	}

	export := skillToExportData(record)
	out, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal export: %w", err)
	}
	return string(out), nil
}

// execSkillImport creates a skill from a portable export document.
func (s *Server) execSkillImport(ctx context.Context, args map[string]any) (string, error) {
	if s.skillStore == nil {
		return "", fmt.Errorf("skill store not configured")
	}
	name, _ := args["name"].(string)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	tools, err := decodeSkillTools(args["tools"])
	if err != nil {
		return "", fmt.Errorf("tools: %w", err)
	}

	skill := service.Skill{
		Name:         name,
		Description:  stringArg(args, "description"),
		SystemPrompt: stringArg(args, "system_prompt"),
		Tools:        tools,
		Version:      stringArg(args, "version"),
		Author:       stringArg(args, "author"),
		License:      stringArg(args, "license"),
		CreatedBy:    "mcp",
		UpdatedBy:    "mcp",
	}
	record, err := s.skillStore.CreateSkill(ctx, skill)
	if err != nil {
		return "", fmt.Errorf("import skill: %w", err)
	}
	out, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal skill: %w", err)
	}
	return string(out), nil
}

// execSkillImportURL fetches a URL and installs the skill it contains.
// Reuses the HTTP handler's auto-detect logic (JSON vs SKILL.md).
func (s *Server) execSkillImportURL(ctx context.Context, args map[string]any) (string, error) {
	if s.skillStore == nil {
		return "", fmt.Errorf("skill store not configured")
	}
	url, _ := args["url"].(string)
	if url == "" {
		return "", fmt.Errorf("url is required")
	}

	parsed, checksum, err := s.fetchAndParseSkillURL(ctx, url)
	if err != nil {
		return "", fmt.Errorf("fetch/parse skill: %w", err)
	}
	if parsed.Name == "" {
		return "", fmt.Errorf("imported skill has no name")
	}

	skill := skillFromExportData(parsed, "mcp")
	skill.SourceURL = url
	skill.SourceChecksum = checksum
	record, err := s.skillStore.CreateSkill(ctx, skill)
	if err != nil {
		return "", fmt.Errorf("create skill: %w", err)
	}
	out, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal skill: %w", err)
	}
	return string(out), nil
}

// execSkillImportSkillMD parses raw Anthropic SKILL.md content and
// installs the skill. Frontmatter `name` is required; the body becomes
// the skill's system_prompt.
func (s *Server) execSkillImportSkillMD(ctx context.Context, args map[string]any) (string, error) {
	if s.skillStore == nil {
		return "", fmt.Errorf("skill store not configured")
	}
	content, _ := args["content"].(string)
	if content == "" {
		return "", fmt.Errorf("content is required")
	}
	export, err := skillExportFromSkillMD([]byte(content))
	if err != nil {
		return "", fmt.Errorf("parse SKILL.md: %w", err)
	}
	if export.Name == "" {
		return "", fmt.Errorf("SKILL.md frontmatter is missing `name`")
	}

	skill := skillFromExportData(export, "mcp")
	skill.SourceChecksum = sha256Hex(content)
	record, err := s.skillStore.CreateSkill(ctx, skill)
	if err != nil {
		return "", fmt.Errorf("create skill: %w", err)
	}
	out, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal skill: %w", err)
	}
	return string(out), nil
}

// stringArg returns args[k] as a string, or "" if missing/wrong type.
func stringArg(args map[string]any, k string) string {
	v, _ := args[k].(string)
	return v
}
