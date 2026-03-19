package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rakunlabs/at/internal/service"
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
