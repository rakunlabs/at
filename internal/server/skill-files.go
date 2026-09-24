package server

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	pathpkg "path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/rakunlabs/at/internal/service"
)

const skillMainFile = "SKILL.md"

type skillFile struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	MediaType string `json:"media_type,omitempty"`
}

type putSkillFilesRequest struct {
	Files []skillFile `json:"files"`
}

// ImportSkillFilesAPI creates a skill from a browser-selected directory. The
// directory may include one leading folder (the webkitRelativePath shape); it
// is stripped so SKILL.md becomes the package root.
func (s *Server) ImportSkillFilesAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxSkillPackageSize*6+64*1024)
	var req putSkillFilesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	files, err := normalizeSkillImportFiles(req.Files)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	skill, err := mergeSkillFiles(service.Skill{}, files)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	by := s.getUserEmail(r)
	principal, ok := service.AccessPrincipalFromContext(r.Context())
	if !ok && service.LegacyWorkspaceAccessFromContext(r.Context()) {
		skill.OwnerUserID = ""
		skill.Scope = "workspace"
	} else if !ok || principal.UserID == "" {
		httpResponse(w, "personal skills require a signed-in account", http.StatusBadRequest)
		return
	} else {
		skill.OwnerUserID = principal.UserID
		skill.Scope = "personal"
	}
	skill.CreatedBy = by
	skill.UpdatedBy = by
	record, err := s.skillStore.CreateSkill(r.Context(), skill)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to create skill from folder: %v", err), http.StatusInternalServerError)
		return
	}
	httpResponseJSON(w, record, http.StatusCreated)
}

// ListSkillFilesAPI returns the editable, virtual directory represented by a
// skill record. SKILL.md is generated from the structured skill fields; every
// other entry is backed by Skill.Resources.
func (s *Server) ListSkillFilesAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	record, err := s.skillStore.GetSkill(r.Context(), r.PathValue("id"))
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to get skill: %v", err), http.StatusInternalServerError)
		return
	}
	if record == nil {
		httpResponse(w, "skill not found", http.StatusNotFound)
		return
	}

	main, err := skillToMarkdown(record)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to generate SKILL.md: %v", err), http.StatusInternalServerError)
		return
	}
	files := make([]skillFile, 0, len(record.Resources)+1)
	files = append(files, skillFile{Path: skillMainFile, Content: string(main), MediaType: "text/markdown"})
	for _, resource := range record.Resources {
		files = append(files, skillFile{Path: resource.Path, Content: resource.Content, MediaType: resource.MediaType})
	}
	sort.Slice(files[1:], func(i, j int) bool { return files[i+1].Path < files[j+1].Path })

	httpResponseJSON(w, map[string]any{"files": files}, http.StatusOK)
}

// PutSkillFilesAPI creates or replaces text files in a skill's virtual
// directory. A batch is committed with one store update, so dropping a folder
// cannot leave a half-updated package. Matching paths are overwritten.
func (s *Server) PutSkillFilesAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	// JSON may expand valid text substantially (for example, control characters
	// become six-byte escape sequences). The decoded package is still bounded by
	// maxSkillPackageSize below.
	r.Body = http.MaxBytesReader(w, r.Body, maxSkillPackageSize*6+64*1024)
	var req putSkillFilesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if len(req.Files) == 0 {
		httpResponse(w, "at least one file is required", http.StatusBadRequest)
		return
	}

	record, err := s.skillStore.GetSkill(r.Context(), r.PathValue("id"))
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to get skill: %v", err), http.StatusInternalServerError)
		return
	}
	if record == nil {
		httpResponse(w, "skill not found", http.StatusNotFound)
		return
	}

	updated, err := mergeSkillFiles(*record, req.Files)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	updated.UpdatedBy = s.getUserEmail(r)
	result, err := s.skillStore.UpdateSkill(r.Context(), record.ID, updated)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to update skill files: %v", err), http.StatusInternalServerError)
		return
	}
	if result == nil {
		httpResponse(w, "skill not found", http.StatusNotFound)
		return
	}

	httpResponseJSON(w, result, http.StatusOK)
}

// DeleteSkillFileAPI removes one resource. SKILL.md is required and can only
// be changed, not deleted.
func (s *Server) DeleteSkillFileAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	filePath, err := cleanSkillFilePath(r.URL.Query().Get("path"))
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.EqualFold(filePath, skillMainFile) {
		httpResponse(w, "SKILL.md cannot be deleted", http.StatusBadRequest)
		return
	}

	record, err := s.skillStore.GetSkill(r.Context(), r.PathValue("id"))
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to get skill: %v", err), http.StatusInternalServerError)
		return
	}
	if record == nil {
		httpResponse(w, "skill not found", http.StatusNotFound)
		return
	}

	resources := make([]service.SkillResource, 0, len(record.Resources))
	found := false
	for _, resource := range record.Resources {
		if resource.Path == filePath {
			found = true
			continue
		}
		resources = append(resources, resource)
	}
	if !found {
		httpResponse(w, "skill file not found", http.StatusNotFound)
		return
	}
	record.Resources = resources
	record.UpdatedBy = s.getUserEmail(r)
	if _, err := s.skillStore.UpdateSkill(r.Context(), record.ID, *record); err != nil {
		httpResponse(w, fmt.Sprintf("failed to delete skill file: %v", err), http.StatusInternalServerError)
		return
	}
	httpResponse(w, "deleted", http.StatusOK)
}

func mergeSkillFiles(skill service.Skill, files []skillFile) (service.Skill, error) {
	resources := make(map[string]service.SkillResource, len(skill.Resources)+len(files))
	for _, resource := range skill.Resources {
		clean, err := cleanSkillFilePath(resource.Path)
		if err != nil || strings.EqualFold(clean, skillMainFile) {
			continue
		}
		resources[clean] = resource
	}

	for _, file := range files {
		clean, err := cleanSkillFilePath(file.Path)
		if err != nil {
			return skill, err
		}
		if !utf8.ValidString(file.Content) || strings.IndexByte(file.Content, 0) >= 0 {
			return skill, fmt.Errorf("file %q must be UTF-8 text", clean)
		}
		if len(file.Content) > maxSkillResourceSize && !strings.EqualFold(clean, skillMainFile) {
			return skill, fmt.Errorf("file %q exceeds %d bytes", clean, maxSkillResourceSize)
		}

		if strings.EqualFold(clean, skillMainFile) {
			export, err := skillExportFromSkillMD([]byte(file.Content))
			if err != nil {
				return skill, fmt.Errorf("parse SKILL.md: %w", err)
			}
			if strings.TrimSpace(export.Name) == "" {
				return skill, fmt.Errorf("SKILL.md has no name in frontmatter")
			}
			skill.Name = export.Name
			skill.Description = export.Description
			skill.Category = export.Category
			skill.Tags = export.Tags
			skill.Version = export.Version
			skill.Author = export.Author
			skill.License = export.License
			skill.SystemPrompt = export.SystemPrompt
			skill.Tools = export.Tools
			continue
		}

		mediaType := strings.TrimSpace(file.MediaType)
		if mediaType == "" {
			mediaType = mime.TypeByExtension(strings.ToLower(pathpkg.Ext(clean)))
		}
		resources[clean] = service.SkillResource{Path: clean, Content: file.Content, MediaType: mediaType}
	}

	if len(resources) > maxSkillPackageFiles {
		return skill, fmt.Errorf("skill package exceeds %d resource files", maxSkillPackageFiles)
	}
	total := 0
	skill.Resources = make([]service.SkillResource, 0, len(resources))
	for _, resource := range resources {
		if len(resource.Content) > maxSkillResourceSize {
			return skill, fmt.Errorf("file %q exceeds %d bytes", resource.Path, maxSkillResourceSize)
		}
		total += len(resource.Content)
		skill.Resources = append(skill.Resources, resource)
	}
	sort.Slice(skill.Resources, func(i, j int) bool { return skill.Resources[i].Path < skill.Resources[j].Path })
	main, err := skillToMarkdown(&skill)
	if err != nil {
		return skill, fmt.Errorf("generate SKILL.md: %w", err)
	}
	if total+len(main) > maxSkillPackageSize {
		return skill, fmt.Errorf("skill package exceeds %d bytes", maxSkillPackageSize)
	}
	return skill, nil
}

func cleanSkillFilePath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" {
		return "", fmt.Errorf("file path is required")
	}
	clean := pathpkg.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || pathpkg.IsAbs(clean) {
		return "", fmt.Errorf("file path must stay inside the skill directory")
	}
	if len(clean) > 512 {
		return "", fmt.Errorf("file path exceeds 512 bytes")
	}
	for _, part := range strings.Split(clean, "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".") {
			return "", fmt.Errorf("file path contains an unsupported segment")
		}
	}
	return clean, nil
}

func normalizeSkillImportFiles(files []skillFile) ([]skillFile, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("at least one file is required")
	}
	cleaned := make([]skillFile, 0, len(files))
	mainPath := ""
	for _, file := range files {
		if skillImportHiddenPath(file.Path) {
			continue
		}
		clean, err := cleanSkillFilePath(file.Path)
		if err != nil {
			return nil, err
		}
		file.Path = clean
		cleaned = append(cleaned, file)
		if strings.EqualFold(pathpkg.Base(clean), skillMainFile) {
			if mainPath != "" {
				return nil, fmt.Errorf("skill folder must contain exactly one SKILL.md")
			}
			mainPath = clean
		}
	}
	if mainPath == "" {
		return nil, fmt.Errorf("skill folder must contain SKILL.md")
	}

	root := pathpkg.Dir(mainPath)
	if root == "." {
		root = ""
	}
	normalized := make([]skillFile, 0, len(cleaned))
	for _, file := range cleaned {
		if root != "" {
			prefix := root + "/"
			if !strings.HasPrefix(file.Path, prefix) {
				return nil, fmt.Errorf("file %q is outside the SKILL.md directory", file.Path)
			}
			file.Path = strings.TrimPrefix(file.Path, prefix)
		}
		normalized = append(normalized, file)
	}
	return normalized, nil
}

func skillImportHiddenPath(value string) bool {
	value = strings.ReplaceAll(value, "\\", "/")
	for _, part := range strings.Split(value, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}
