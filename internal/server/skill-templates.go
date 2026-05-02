package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

//go:embed skill_templates/*.json
var skillTemplateFS embed.FS

// SkillTemplate is a predefined skill that ships with AT.
type SkillTemplate struct {
	Slug              string             `json:"slug"`
	Name              string             `json:"name"`
	Description       string             `json:"description"`
	Category          string             `json:"category"`
	Tags              []string           `json:"tags"`
	RequiredVariables []RequiredVariable `json:"required_variables"`
	OAuth             string             `json:"oauth,omitempty"` // OAuth provider name (e.g. "google") — signals frontend to show connect flow
	Skill             SkillTemplateData  `json:"skill"`
}

// SkillTemplateData holds the skill payload to be installed.
type SkillTemplateData struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	SystemPrompt string         `json:"system_prompt"`
	Tools        []service.Tool `json:"tools"`
}

// RequiredVariable describes a variable the skill needs at runtime.
type RequiredVariable struct {
	Key         string `json:"key"`
	Description string `json:"description"`
	Secret      bool   `json:"secret"`
}

// loadSkillTemplates reads all embedded JSON template files.
func (s *Server) loadSkillTemplates() {
	entries, err := skillTemplateFS.ReadDir("skill_templates")
	if err != nil {
		slog.Warn("failed to read skill_templates dir", "error", err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, err := skillTemplateFS.ReadFile("skill_templates/" + entry.Name())
		if err != nil {
			slog.Warn("failed to read skill template", "file", entry.Name(), "error", err)
			continue
		}

		var tmpl SkillTemplate
		if err := json.Unmarshal(data, &tmpl); err != nil {
			slog.Warn("failed to parse skill template", "file", entry.Name(), "error", err)
			continue
		}

		s.skillTemplates = append(s.skillTemplates, tmpl)
	}

	slog.Info("loaded skill templates", "count", len(s.skillTemplates))
}

// syncInstalledSkillHandlers updates installed skills whose tool handlers
// differ from the current embedded templates. This ensures that bug fixes
// in skill handlers (e.g. mktemp → uuid, workspace dir support) are applied
// to already-installed skills without requiring manual reinstallation.
func (s *Server) syncInstalledSkillHandlers(ctx context.Context) {
	if s.skillStore == nil || len(s.skillTemplates) == 0 {
		return
	}

	for _, tmpl := range s.skillTemplates {
		installed, err := s.skillStore.GetSkillByName(ctx, tmpl.Skill.Name)
		if err != nil {
			continue // lookup error — skip
		}
		if installed == nil {
			continue // not installed — skip
		}

		// Check if any tool handler differs, or the system prompt drifted.
		// (SystemPrompt sync was added so the ffmpeg-guide skill — which is
		// almost entirely system-prompt hints to the model — picks up the
		// CPU-discipline addendum baked into newer template versions.)
		needsUpdate := false
		if installed.SystemPrompt != tmpl.Skill.SystemPrompt {
			needsUpdate = true
		}
		if len(installed.Tools) != len(tmpl.Skill.Tools) {
			needsUpdate = true
		} else {
			for i := range tmpl.Skill.Tools {
				if i >= len(installed.Tools) {
					needsUpdate = true
					break
				}
				if installed.Tools[i].Handler != tmpl.Skill.Tools[i].Handler {
					needsUpdate = true
					break
				}
			}
		}

		if !needsUpdate {
			continue
		}

		// Update the installed skill with the template's tools and prompt.
		// Keep all other existing fields (name, description, etc.).
		_, err = s.skillStore.UpdateSkill(ctx, installed.ID, service.Skill{
			Name:         installed.Name,
			Description:  installed.Description,
			SystemPrompt: tmpl.Skill.SystemPrompt,
			Tools:        tmpl.Skill.Tools,
			UpdatedBy:    "system",
		})
		if err != nil {
			slog.Warn("skill-templates: failed to sync skill handlers",
				"skill", tmpl.Skill.Name, "id", installed.ID, "error", err)
		} else {
			slog.Info("skill-templates: synced skill handlers from template",
				"skill", tmpl.Skill.Name, "id", installed.ID)
		}
	}
}

// ListSkillTemplatesAPI handles GET /api/v1/skill-templates.
func (s *Server) ListSkillTemplatesAPI(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")

	var result []SkillTemplate
	for _, t := range s.skillTemplates {
		if category != "" && !strings.EqualFold(t.Category, category) {
			continue
		}
		result = append(result, t)
	}

	if result == nil {
		result = []SkillTemplate{}
	}

	httpResponseJSON(w, result, http.StatusOK)
}

// GetSkillTemplateAPI handles GET /api/v1/skill-templates/{slug}.
func (s *Server) GetSkillTemplateAPI(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	for _, t := range s.skillTemplates {
		if t.Slug == slug {
			httpResponseJSON(w, t, http.StatusOK)
			return
		}
	}
	httpResponse(w, fmt.Sprintf("template %q not found", slug), http.StatusNotFound)
}

// InstallSkillTemplateAPI handles POST /api/v1/skill-templates/{slug}/install.
func (s *Server) InstallSkillTemplateAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	slug := r.PathValue("slug")
	var tmpl *SkillTemplate
	for i := range s.skillTemplates {
		if s.skillTemplates[i].Slug == slug {
			tmpl = &s.skillTemplates[i]
			break
		}
	}
	if tmpl == nil {
		httpResponse(w, fmt.Sprintf("template %q not found", slug), http.StatusNotFound)
		return
	}

	userEmail := s.getUserEmail(r)

	skill := service.Skill{
		Name:         tmpl.Skill.Name,
		Description:  tmpl.Skill.Description,
		Category:     tmpl.Category,
		Tags:         tmpl.Tags,
		SystemPrompt: tmpl.Skill.SystemPrompt,
		Tools:        tmpl.Skill.Tools,
		CreatedBy:    userEmail,
		UpdatedBy:    userEmail,
	}

	record, err := s.skillStore.CreateSkill(r.Context(), skill)
	if err != nil {
		slog.Error("install skill template failed", "slug", slug, "error", err)
		httpResponse(w, fmt.Sprintf("failed to install template: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}
