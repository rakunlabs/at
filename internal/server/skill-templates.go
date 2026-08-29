package server

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

//go:embed skill_templates/*.json
var skillTemplateFS embed.FS

const skillTemplateSourcePrefix = "builtin://skill-template/"

// SkillTemplate is a predefined skill that ships with AT.
type SkillTemplate struct {
	Slug              string             `json:"slug"`
	Name              string             `json:"name"`
	Description       string             `json:"description"`
	Category          string             `json:"category"`
	Tags              []string           `json:"tags"`
	RequiredVariables []RequiredVariable `json:"required_variables"`
	OAuth             string             `json:"oauth,omitempty"` // connector slug (e.g. "google") — references an existing connector
	// Connector optionally declares the external-service connection TYPE this
	// skill brings with it. On install it is upserted into the connector
	// registry (if a connector with that slug doesn't already exist) so a
	// user-added skill can define and use its own connection without a code
	// change. Use OAuth to reference an existing connector by slug instead.
	Connector *service.Connector `json:"connector,omitempty"`
	Skill     SkillTemplateData  `json:"skill"`
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

// validateSkillTemplate validates the declarative parts of a built-in skill.
// Callers may provide validateBash to parse bash handlers without executing
// them. Heredoc bodies (including embedded Python) remain data to the shell
// parser; extracting those scripts into standalone assets is a separate
// migration.
func validateSkillTemplate(tmpl SkillTemplate, validateBash func(string) error) error {
	var errs []error
	require := func(path, value string) {
		if strings.TrimSpace(value) == "" {
			errs = append(errs, fmt.Errorf("%s is required", path))
		}
	}

	require("slug", tmpl.Slug)
	require("name", tmpl.Name)
	require("description", tmpl.Description)
	require("category", tmpl.Category)
	require("skill.name", tmpl.Skill.Name)
	require("skill.description", tmpl.Skill.Description)
	require("skill.system_prompt", tmpl.Skill.SystemPrompt)

	variableKeys := make(map[string]struct{}, len(tmpl.RequiredVariables))
	for i, variable := range tmpl.RequiredVariables {
		path := fmt.Sprintf("required_variables[%d]", i)
		require(path+".key", variable.Key)
		require(path+".description", variable.Description)
		if _, exists := variableKeys[variable.Key]; exists && variable.Key != "" {
			errs = append(errs, fmt.Errorf("%s.key %q is duplicated", path, variable.Key))
		}
		variableKeys[variable.Key] = struct{}{}
	}

	toolNames := make(map[string]struct{}, len(tmpl.Skill.Tools))
	for i, tool := range tmpl.Skill.Tools {
		path := fmt.Sprintf("skill.tools[%d]", i)
		require(path+".name", tool.Name)
		require(path+".description", tool.Description)
		require(path+".handler_type", tool.HandlerType)
		require(path+".handler", tool.Handler)
		if _, exists := toolNames[tool.Name]; exists && tool.Name != "" {
			errs = append(errs, fmt.Errorf("%s.name %q is duplicated", path, tool.Name))
		}
		toolNames[tool.Name] = struct{}{}

		if tool.InputSchema == nil {
			errs = append(errs, fmt.Errorf("%s.inputSchema is required", path))
		} else if schemaType, ok := tool.InputSchema["type"].(string); !ok || schemaType != "object" {
			errs = append(errs, fmt.Errorf("%s.inputSchema.type must be %q", path, "object"))
		}

		switch tool.HandlerType {
		case "bash":
			if validateBash != nil && strings.TrimSpace(tool.Handler) != "" {
				if err := validateBash(tool.Handler); err != nil {
					errs = append(errs, fmt.Errorf("%s.handler has invalid bash syntax: %w", path, err))
				}
			}
		case "js":
		case "":
		default:
			errs = append(errs, fmt.Errorf("%s.handler_type %q is unsupported", path, tool.HandlerType))
		}
	}

	return errors.Join(errs...)
}

// skillTemplateManagedChecksum is the deterministic version of content owned
// by template sync. encoding/json sorts map keys, while slices retain order, so
// every Tool field (including future fields) participates without depending on
// map iteration order. User-owned skill metadata is deliberately excluded.
func skillTemplateManagedChecksum(tmpl SkillTemplateData) (string, error) {
	payload := struct {
		SystemPrompt string         `json:"system_prompt"`
		Tools        []service.Tool `json:"tools"`
	}{
		SystemPrompt: tmpl.SystemPrompt,
		Tools:        tmpl.Tools,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal template-managed skill content: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func installedSkillManagedChecksum(installed *service.Skill) (string, error) {
	return skillTemplateManagedChecksum(SkillTemplateData{
		SystemPrompt: installed.SystemPrompt,
		Tools:        installed.Tools,
	})
}

func skillTemplateSourceURL(slug string) string {
	return skillTemplateSourcePrefix + slug
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

// syncInstalledSkillHandlers updates only template-owned skills whose managed
// payload still matches the checksum installed by the previous template
// version. A mismatch means the user changed the prompt or tools, so startup
// preserves that customization rather than treating it as template drift.
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

		templateChecksum, err := skillTemplateManagedChecksum(tmpl.Skill)
		if err != nil {
			slog.Warn("skill-templates: failed to checksum embedded template",
				"skill", tmpl.Skill.Name, "error", err)
			continue
		}
		installedChecksum, err := installedSkillManagedChecksum(installed)
		if err != nil {
			slog.Warn("skill-templates: failed to checksum installed skill",
				"skill", tmpl.Skill.Name, "id", installed.ID, "error", err)
			continue
		}

		sourceURL := skillTemplateSourceURL(tmpl.Slug)
		if installed.SourceURL == "" && installed.SourceChecksum == "" {
			// Legacy installs had no checksum. Exact matches are safe to enrol.
			// "system" is also a reliable historical marker because this sync is
			// the only skill path that uses it; ordinary user/API edits replace it.
			// Other drift could be an old template or a user customization and is
			// intentionally preserved because it cannot be distinguished.
			previouslySynced := installed.UpdatedBy == "system"
			if installedChecksum != templateChecksum && !previouslySynced {
				continue
			}
			updated := *installed
			if previouslySynced {
				updated.SystemPrompt = tmpl.Skill.SystemPrompt
				updated.Tools = tmpl.Skill.Tools
			}
			updated.SourceURL = sourceURL
			updated.SourceChecksum = templateChecksum
			updated.UpdatedBy = "system"
			if _, err = s.skillStore.UpdateSkill(ctx, installed.ID, updated); err != nil {
				slog.Warn("skill-templates: failed to mark legacy template skill",
					"skill", tmpl.Skill.Name, "id", installed.ID, "error", err)
			}
			continue
		}

		if installed.SourceURL != sourceURL || installed.SourceChecksum == "" {
			continue
		}
		if installedChecksum != installed.SourceChecksum {
			slog.Info("skill-templates: preserving customized installed skill",
				"skill", tmpl.Skill.Name, "id", installed.ID)
			continue
		}
		if installed.SourceChecksum == templateChecksum {
			continue
		}

		// Update the installed skill with the template's tools and prompt.
		// Keep all other existing fields (name, description, category,
		// tags, provenance metadata, etc.).
		updated := *installed
		updated.SystemPrompt = tmpl.Skill.SystemPrompt
		updated.Tools = tmpl.Skill.Tools
		updated.SourceChecksum = templateChecksum
		updated.UpdatedBy = "system"
		_, err = s.skillStore.UpdateSkill(ctx, installed.ID, updated)
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
	checksum, err := skillTemplateManagedChecksum(tmpl.Skill)
	if err != nil {
		slog.Error("install skill template checksum failed", "slug", slug, "error", err)
		httpResponse(w, fmt.Sprintf("failed to checksum template: %v", err), http.StatusInternalServerError)
		return
	}

	skill := service.Skill{
		Name:           tmpl.Skill.Name,
		Description:    tmpl.Skill.Description,
		Category:       tmpl.Category,
		Tags:           tmpl.Tags,
		SystemPrompt:   tmpl.Skill.SystemPrompt,
		Tools:          tmpl.Skill.Tools,
		SourceURL:      skillTemplateSourceURL(tmpl.Slug),
		SourceChecksum: checksum,
		CreatedBy:      userEmail,
		UpdatedBy:      userEmail,
	}

	record, err := s.skillStore.CreateSkill(r.Context(), skill)
	if err != nil {
		slog.Error("install skill template failed", "slug", slug, "error", err)
		httpResponse(w, fmt.Sprintf("failed to install template: %v", err), http.StatusInternalServerError)
		return
	}

	// If the skill declares its own connector type, register it so the user can
	// immediately create a connection for it. Best-effort — failures here don't
	// fail the install.
	if tmpl.Connector != nil {
		s.ensureConnectorFromSkill(r.Context(), *tmpl.Connector, userEmail)
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// ensureConnectorFromSkill upserts a skill-declared connector into the registry
// when no connector with that slug already exists (built-in or DB). Best-effort.
func (s *Server) ensureConnectorFromSkill(ctx context.Context, c service.Connector, userEmail string) {
	if s.connectorStore == nil || strings.TrimSpace(c.Slug) == "" {
		return
	}
	if existing, _ := s.resolveConnector(ctx, c.Slug); existing != nil {
		return // already known — don't clobber a built-in or user override
	}
	if c.AuthKind == "" {
		c.AuthKind = service.ConnectorAuthToken
	}
	c.CreatedBy = userEmail
	c.UpdatedBy = userEmail
	if _, err := s.connectorStore.CreateConnector(ctx, c); err != nil {
		slog.Warn("skill-templates: failed to register skill connector", "slug", c.Slug, "error", err)
		return
	}
	slog.Info("skill-templates: registered connector from skill", "slug", c.Slug)
}
