package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/skillmd"
)

// MarketplaceSkill is the normalized skill entry returned by marketplace search.
type MarketplaceSkill struct {
	Source      string   `json:"source"`
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Author      string   `json:"author"`
	Downloads   int      `json:"downloads"`
	License     string   `json:"license"`
	Tags        []string `json:"tags"`
	URL         string   `json:"url"`
}

type marketplaceSearchResponse struct {
	Skills []MarketplaceSkill `json:"skills"`
}

// ─── Source CRUD ───

func (s *Server) ListMarketplaceSourcesAPI(w http.ResponseWriter, r *http.Request) {
	if s.marketplaceSourceStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	sources, err := s.marketplaceSourceStore.ListMarketplaceSources(r.Context())
	if err != nil {
		slog.Error("list marketplace sources failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list marketplace sources: %v", err), http.StatusInternalServerError)
		return
	}

	if sources == nil {
		sources = []service.MarketplaceSource{}
	}

	httpResponseJSON(w, sources, http.StatusOK)
}

func (s *Server) CreateMarketplaceSourceAPI(w http.ResponseWriter, r *http.Request) {
	if s.marketplaceSourceStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.MarketplaceSource
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	record, err := s.marketplaceSourceStore.CreateMarketplaceSource(r.Context(), req)
	if err != nil {
		slog.Error("create marketplace source failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to create marketplace source: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

func (s *Server) UpdateMarketplaceSourceAPI(w http.ResponseWriter, r *http.Request) {
	if s.marketplaceSourceStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "id is required", http.StatusBadRequest)
		return
	}

	var req service.MarketplaceSource
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	record, err := s.marketplaceSourceStore.UpdateMarketplaceSource(r.Context(), id, req)
	if err != nil {
		slog.Error("update marketplace source failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update marketplace source: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("marketplace source %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

func (s *Server) DeleteMarketplaceSourceAPI(w http.ResponseWriter, r *http.Request) {
	if s.marketplaceSourceStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "id is required", http.StatusBadRequest)
		return
	}

	if err := s.marketplaceSourceStore.DeleteMarketplaceSource(r.Context(), id); err != nil {
		slog.Error("delete marketplace source failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete marketplace source: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}

// ─── Search / Top / Preview / Import ───

func (s *Server) MarketplaceSearchAPI(w http.ResponseWriter, r *http.Request) {
	if s.marketplaceSourceStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	query := r.URL.Query().Get("q")
	sourceFilter := r.URL.Query().Get("source") // comma-separated IDs

	sources, err := s.marketplaceSourceStore.ListMarketplaceSources(r.Context())
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to list sources: %v", err), http.StatusInternalServerError)
		return
	}

	// Filter to enabled + requested sources.
	filterSet := map[string]bool{}
	if sourceFilter != "" {
		for _, id := range strings.Split(sourceFilter, ",") {
			filterSet[strings.TrimSpace(id)] = true
		}
	}

	var activeSources []service.MarketplaceSource
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		if len(filterSet) > 0 && !filterSet[src.ID] {
			continue
		}
		activeSources = append(activeSources, src)
	}

	// Fan out search to all active sources.
	var mu sync.Mutex
	var allSkills []MarketplaceSkill
	var wg sync.WaitGroup

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	for _, src := range activeSources {
		wg.Add(1)
		go func(src service.MarketplaceSource) {
			defer wg.Done()
			skills := s.searchMarketplace(ctx, src, query)
			mu.Lock()
			allSkills = append(allSkills, skills...)
			mu.Unlock()
		}(src)
	}

	wg.Wait()

	if allSkills == nil {
		allSkills = []MarketplaceSkill{}
	}

	httpResponseJSON(w, marketplaceSearchResponse{Skills: allSkills}, http.StatusOK)
}

func (s *Server) MarketplaceTopAPI(w http.ResponseWriter, r *http.Request) {
	if s.marketplaceSourceStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	sourceID := r.URL.Query().Get("source")

	sources, err := s.marketplaceSourceStore.ListMarketplaceSources(r.Context())
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to list sources: %v", err), http.StatusInternalServerError)
		return
	}

	var mu sync.Mutex
	var allSkills []MarketplaceSkill
	var wg sync.WaitGroup

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		if sourceID != "" && src.ID != sourceID {
			continue
		}
		if src.TopURL == "" && src.SearchURL == "" {
			continue
		}
		wg.Add(1)
		go func(src service.MarketplaceSource) {
			defer wg.Done()
			skills := s.topMarketplace(ctx, src)
			mu.Lock()
			allSkills = append(allSkills, skills...)
			mu.Unlock()
		}(src)
	}

	wg.Wait()

	if allSkills == nil {
		allSkills = []MarketplaceSkill{}
	}

	httpResponseJSON(w, marketplaceSearchResponse{Skills: allSkills}, http.StatusOK)
}

func (s *Server) MarketplacePreviewAPI(w http.ResponseWriter, r *http.Request) {
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

	export, err := s.fetchAndParseSkill(r.Context(), body.URL)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to fetch/parse skill: %v", err), http.StatusBadRequest)
		return
	}

	httpResponseJSON(w, export, http.StatusOK)
}

func (s *Server) MarketplaceImportAPI(w http.ResponseWriter, r *http.Request) {
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

	export, err := s.fetchAndParseSkill(r.Context(), body.URL)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to fetch/parse skill: %v", err), http.StatusBadRequest)
		return
	}

	if export.Name == "" {
		httpResponse(w, "parsed skill has no name", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	skill := service.Skill{
		Name:         export.Name,
		Description:  export.Description,
		SystemPrompt: export.SystemPrompt,
		Tools:        export.Tools,
		CreatedBy:    userEmail,
		UpdatedBy:    userEmail,
	}

	record, err := s.skillStore.CreateSkill(r.Context(), skill)
	if err != nil {
		slog.Error("marketplace import failed", "url", body.URL, "error", err)
		httpResponse(w, fmt.Sprintf("failed to import skill: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// ─── Internal helpers ───

// fetchAndParseSkill fetches a URL and tries JSON, then SKILL.md parsing.
func (s *Server) fetchAndParseSkill(ctx context.Context, url string) (*skillExportData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := s.marketplaceClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("URL returned status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024)) // 2MB max
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
		// Try to derive name from URL.
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

// searchMarketplace dispatches search to the appropriate adapter.
func (s *Server) searchMarketplace(ctx context.Context, src service.MarketplaceSource, query string) []MarketplaceSkill {
	if src.SearchURL == "" {
		return nil
	}

	return s.searchGeneric(ctx, src, query)
}

// topMarketplace fetches top/trending skills from a source.
func (s *Server) topMarketplace(ctx context.Context, src service.MarketplaceSource) []MarketplaceSkill {
	topURL := src.TopURL
	if topURL == "" {
		topURL = src.SearchURL
	}
	if topURL == "" {
		return nil
	}

	return s.searchGeneric(ctx, src, "")
}

// ─── Generic adapter ───

func (s *Server) searchGeneric(ctx context.Context, src service.MarketplaceSource, query string) []MarketplaceSkill {
	u := src.SearchURL
	if query != "" {
		encoded := url.QueryEscape(query)
		if strings.Contains(u, "?") {
			u += "&q=" + encoded
		} else {
			u += "?q=" + encoded
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil
	}

	resp, err := s.marketplaceClient.Do(req)
	if err != nil {
		slog.Warn("generic marketplace fetch failed", "url", u, "error", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	// Try to parse as array of MarketplaceSkill.
	var skills []MarketplaceSkill
	if err := json.NewDecoder(resp.Body).Decode(&skills); err != nil {
		return nil
	}

	// Tag with source name.
	for i := range skills {
		if skills[i].Source == "" {
			skills[i].Source = src.Name
		}
	}

	return skills
}
