package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
	"github.com/rakunlabs/at/internal/skillmd"
	"github.com/rakunlabs/query"
)

// ─── Skill CRUD API ───

// skillsResponse wraps a list of skill records for JSON output.
type skillsResponse struct {
	Skills []service.Skill `json:"skills"`
}

// ListSkillsAPI handles GET /api/v1/skills.
func (s *Server) ListSkillsAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.skillStore.ListSkills(r.Context(), q)
	if err != nil {
		slog.Error("list skills failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list skills: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.Skill]{Data: []service.Skill{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetSkillAPI handles GET /api/v1/skills/:id.
func (s *Server) GetSkillAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "skill id is required", http.StatusBadRequest)
		return
	}

	record, err := s.skillStore.GetSkill(r.Context(), id)
	if err != nil {
		slog.Error("get skill failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get skill: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("skill %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateSkillAPI handles POST /api/v1/skills.
func (s *Server) CreateSkillAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.Skill
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	req.CreatedBy = userEmail
	req.UpdatedBy = userEmail

	record, err := s.skillStore.CreateSkill(r.Context(), req)
	if err != nil {
		slog.Error("create skill failed", "name", req.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create skill: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateSkillAPI handles PUT /api/v1/skills/:id.
func (s *Server) UpdateSkillAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "skill id is required", http.StatusBadRequest)
		return
	}

	var req service.Skill
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	req.UpdatedBy = userEmail

	record, err := s.skillStore.UpdateSkill(r.Context(), id, req)
	if err != nil {
		slog.Error("update skill failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update skill: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("skill %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteSkillAPI handles DELETE /api/v1/skills/:id.
func (s *Server) DeleteSkillAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "skill id is required", http.StatusBadRequest)
		return
	}

	if err := s.skillStore.DeleteSkill(r.Context(), id); err != nil {
		slog.Error("delete skill failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete skill: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}

// ─── Import / Export API ───

// skillExportData is the portable representation of a skill (no id/timestamps).
type skillExportData struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	SystemPrompt string         `json:"system_prompt"`
	Tools        []service.Tool `json:"tools"`
}

// ExportSkillAPI handles GET /api/v1/skills/:id/export.
func (s *Server) ExportSkillAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "skill id is required", http.StatusBadRequest)
		return
	}

	record, err := s.skillStore.GetSkill(r.Context(), id)
	if err != nil {
		slog.Error("export skill failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to export skill: %v", err), http.StatusInternalServerError)
		return
	}
	if record == nil {
		httpResponse(w, fmt.Sprintf("skill %q not found", id), http.StatusNotFound)
		return
	}

	export := skillExportData{
		Name:         record.Name,
		Description:  record.Description,
		SystemPrompt: record.SystemPrompt,
		Tools:        record.Tools,
	}

	httpResponseJSON(w, export, http.StatusOK)
}

// ImportSkillAPI handles POST /api/v1/skills/import.
func (s *Server) ImportSkillAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req skillExportData
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	skill := service.Skill{
		Name:         req.Name,
		Description:  req.Description,
		SystemPrompt: req.SystemPrompt,
		Tools:        req.Tools,
		CreatedBy:    userEmail,
		UpdatedBy:    userEmail,
	}

	record, err := s.skillStore.CreateSkill(r.Context(), skill)
	if err != nil {
		slog.Error("import skill failed", "name", req.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to import skill: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// ImportSkillFromURLAPI handles POST /api/v1/skills/import-url.
// Auto-detects JSON and SKILL.md formats.
func (s *Server) ImportSkillFromURLAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if body.URL == "" {
		httpResponse(w, "url is required", http.StatusBadRequest)
		return
	}

	parsed, err := s.fetchAndParseSkillURL(r.Context(), body.URL)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to fetch/parse skill: %v", err), http.StatusBadRequest)
		return
	}

	if parsed.Name == "" {
		httpResponse(w, "imported skill has no name", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	skill := service.Skill{
		Name:         parsed.Name,
		Description:  parsed.Description,
		SystemPrompt: parsed.SystemPrompt,
		Tools:        parsed.Tools,
		CreatedBy:    userEmail,
		UpdatedBy:    userEmail,
	}

	record, err := s.skillStore.CreateSkill(r.Context(), skill)
	if err != nil {
		slog.Error("import skill from URL failed", "url", body.URL, "error", err)
		httpResponse(w, fmt.Sprintf("failed to import skill: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// PreviewImportURLAPI handles POST /api/v1/skills/import-url/preview.
// Fetches and parses a skill URL without persisting.
func (s *Server) PreviewImportURLAPI(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if body.URL == "" {
		httpResponse(w, "url is required", http.StatusBadRequest)
		return
	}

	parsed, err := s.fetchAndParseSkillURL(r.Context(), body.URL)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to fetch/parse skill: %v", err), http.StatusBadRequest)
		return
	}

	httpResponseJSON(w, parsed, http.StatusOK)
}

// ImportSkillMDAPI handles POST /api/v1/skills/import-skillmd.
// Parses raw SKILL.md content and creates a skill.
func (s *Server) ImportSkillMDAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if body.Content == "" {
		httpResponse(w, "content is required", http.StatusBadRequest)
		return
	}

	parsed, err := skillmd.Parse([]byte(body.Content))
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to parse SKILL.md: %v", err), http.StatusBadRequest)
		return
	}

	if parsed.Name == "" {
		httpResponse(w, "SKILL.md has no name in frontmatter", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	skill := service.Skill{
		Name:         parsed.Name,
		Description:  parsed.Description,
		SystemPrompt: parsed.Body,
		CreatedBy:    userEmail,
		UpdatedBy:    userEmail,
	}

	record, err := s.skillStore.CreateSkill(r.Context(), skill)
	if err != nil {
		slog.Error("import SKILL.md failed", "name", parsed.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to import skill: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// fetchAndParseSkillURL fetches a URL and auto-detects JSON vs SKILL.md format.
func (s *Server) fetchAndParseSkillURL(ctx context.Context, url string) (*skillExportData, error) {
	client := s.marketplaceClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("URL returned status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// Try JSON first (unless URL clearly ends in .md).
	if !strings.HasSuffix(strings.ToLower(url), ".md") {
		var export skillExportData
		if err := json.Unmarshal(data, &export); err == nil && export.Name != "" {
			return &export, nil
		}
	}

	// Try SKILL.md parsing.
	parsed, err := skillmd.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("not valid JSON or SKILL.md: %w", err)
	}

	name := parsed.Name
	if name == "" {
		parts := strings.Split(strings.TrimSuffix(url, "/"), "/")
		for i := len(parts) - 1; i >= 0; i-- {
			if parts[i] != "" && !strings.EqualFold(parts[i], "SKILL.md") {
				name = parts[i]
				break
			}
		}
	}

	return &skillExportData{
		Name:         name,
		Description:  parsed.Description,
		SystemPrompt: parsed.Body,
		Tools:        nil,
	}, nil
}

// ─── Test Handler API ───

// testHandlerRequest is the request body for TestHandlerAPI.
type testHandlerRequest struct {
	Handler     string         `json:"handler"`
	HandlerType string         `json:"handler_type"` // "js" (default) or "bash"
	Arguments   map[string]any `json:"arguments"`
}

// testHandlerResponse is the response body for TestHandlerAPI.
type testHandlerResponse struct {
	Result     string `json:"result"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

// TestHandlerAPI handles POST /api/v1/skills/test-handler.
// It executes a tool handler (JS or bash) server-side with sample arguments
// and returns the result. Used by the Skill Builder AI panel to test handlers.
func (s *Server) TestHandlerAPI(w http.ResponseWriter, r *http.Request) {
	var req testHandlerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Handler == "" {
		httpResponse(w, "handler is required", http.StatusBadRequest)
		return
	}

	if req.Arguments == nil {
		req.Arguments = make(map[string]any)
	}

	start := time.Now()
	var result string
	var execErr error

	if req.HandlerType == "bash" {
		// Build a VarLister from the variable store.
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
		result, execErr = workflow.ExecuteBashHandler(r.Context(), req.Handler, req.Arguments, varLister, 0)
	} else {
		// Default: JS handler.
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
		result, execErr = workflow.ExecuteJSHandler(req.Handler, req.Arguments, varLookup)
	}

	durationMs := time.Since(start).Milliseconds()

	resp := testHandlerResponse{
		Result:     result,
		DurationMs: durationMs,
	}
	if execErr != nil {
		resp.Error = execErr.Error()
		slog.Warn("test handler failed", "handler_type", req.HandlerType, "error", execErr, "duration_ms", durationMs)
	}

	httpResponseJSON(w, resp, http.StatusOK)
}
