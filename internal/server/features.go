package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

type featureResponse struct {
	Key              string `json:"key"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	Group            string `json:"group"`
	GroupName        string `json:"group_name"`
	GroupDescription string `json:"group_description"`
	Parent           string `json:"parent,omitempty"`
	// Enabled is this feature's own override. Effective additionally accounts
	// for the ancestor chain, so the UI can show a child as unavailable while
	// still remembering the switch position it will return to.
	Enabled   bool   `json:"enabled"`
	Effective bool   `json:"effective"`
	BlockedBy string `json:"blocked_by,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

type featureGroupResponse struct {
	Key         string            `json:"key"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Features    []featureResponse `json:"features"`
}

type featuresResponse struct {
	Groups   []featureGroupResponse `json:"groups"`
	Features []featureResponse      `json:"features"`
	Presets  []featurePreset        `json:"presets"`
}

func featureResponseFromSetting(def featureDefinition, setting *service.FeatureSetting, flags map[string]bool) featureResponse {
	group := featureGroupDefinitionForKey(def.Group)
	res := featureResponse{
		Key:              def.Key,
		Name:             def.Name,
		Description:      def.Description,
		Group:            group.Key,
		GroupName:        group.Name,
		GroupDescription: group.Description,
		Parent:           def.Parent,
		Enabled:          true,
	}
	if setting != nil {
		res.Enabled = setting.Enabled
		res.CreatedAt = setting.CreatedAt
		res.UpdatedAt = setting.UpdatedAt
		res.CreatedBy = setting.CreatedBy
		res.UpdatedBy = setting.UpdatedBy
	}
	res.BlockedBy = featureBlockedBy(def.Key, flags)
	res.Effective = res.Enabled && res.BlockedBy == ""

	return res
}

func (s *Server) featureSettingsByKey(ctx context.Context) (map[string]service.FeatureSetting, error) {
	settings := make(map[string]service.FeatureSetting)
	if s.featureStore == nil {
		return settings, nil
	}

	items, err := s.featureStore.ListFeatureSettings(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		settings[item.Key] = item
	}

	return settings, nil
}

func (s *Server) featuresPayload(ctx context.Context) (featuresResponse, error) {
	settings, err := s.featureSettingsByKey(ctx)
	if err != nil {
		return featuresResponse{}, err
	}

	flags := make(map[string]bool, len(settings))
	for key, item := range settings {
		flags[key] = item.Enabled
	}

	groups := make([]featureGroupResponse, 0, len(featureGroupDefinitions))
	groupIndex := make(map[string]int, len(featureGroupDefinitions))
	for _, group := range featureGroupDefinitions {
		groupIndex[group.Key] = len(groups)
		groups = append(groups, featureGroupResponse{
			Key:         group.Key,
			Name:        group.Name,
			Description: group.Description,
			Features:    []featureResponse{},
		})
	}

	features := make([]featureResponse, 0, len(featureDefinitions))
	for _, def := range featureDefinitions {
		var setting *service.FeatureSetting
		if item, ok := settings[def.Key]; ok {
			setting = &item
		}
		feature := featureResponseFromSetting(def, setting, flags)
		features = append(features, feature)
		idx, ok := groupIndex[def.Group]
		if !ok {
			continue
		}
		groups[idx].Features = append(groups[idx].Features, feature)
	}

	presets := make([]featurePreset, 0, len(featurePresets))
	presets = append(presets, featurePresets...)

	return featuresResponse{Groups: groups, Features: features, Presets: presets}, nil
}

// ListFeaturesAPI handles GET /api/v1/features.
func (s *Server) ListFeaturesAPI(w http.ResponseWriter, r *http.Request) {
	payload, err := s.featuresPayload(r.Context())
	if err != nil {
		slog.Error("list feature settings failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list features: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, payload, http.StatusOK)
}

// UpdateFeatureAPI handles PUT /api/v1/features/{key}.
func (s *Server) UpdateFeatureAPI(w http.ResponseWriter, r *http.Request) {
	if s.featureStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	key := r.PathValue("key")
	def, ok := featureDefinitionForKey(key)
	if !ok {
		httpResponse(w, fmt.Sprintf("feature %q not found", key), http.StatusNotFound)
		return
	}

	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if req.Enabled == nil {
		httpResponse(w, "enabled is required", http.StatusBadRequest)
		return
	}

	setting, err := s.featureStore.UpsertFeatureSetting(r.Context(), key, *req.Enabled, s.getUserEmail(r))
	if err != nil {
		slog.Error("update feature setting failed", "key", key, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update feature: %v", err), http.StatusInternalServerError)
		return
	}
	s.afterFeatureChange(r.Context())

	flags, err := s.featureFlags(r.Context())
	if err != nil {
		slog.Error("reload feature settings failed", "error", err)
		flags = map[string]bool{}
	}

	httpResponseJSON(w, featureResponseFromSetting(def, setting, flags), http.StatusOK)
}

// UpdateFeaturesAPI handles PUT /api/v1/features — a bulk write of
// {"features": {"<key>": bool, ...}}. Toggling a preset-sized change one
// request at a time makes the intermediate states observable to other replicas
// (a half-applied "gateway only" briefly has bots running); this applies the
// whole set before the runtime is resynchronised once.
func (s *Server) UpdateFeaturesAPI(w http.ResponseWriter, r *http.Request) {
	if s.featureStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Features map[string]bool `json:"features"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if len(req.Features) == 0 {
		httpResponse(w, "features is required", http.StatusBadRequest)
		return
	}
	for key := range req.Features {
		if _, ok := featureDefinitionForKey(key); !ok {
			httpResponse(w, fmt.Sprintf("feature %q not found", key), http.StatusBadRequest)
			return
		}
	}

	s.applyFeatureTargets(w, r, req.Features)
}

// ApplyFeaturePresetAPI handles POST /api/v1/features/presets/{preset}.
func (s *Server) ApplyFeaturePresetAPI(w http.ResponseWriter, r *http.Request) {
	if s.featureStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	key := r.PathValue("preset")
	preset, ok := featurePresetForKey(key)
	if !ok {
		httpResponse(w, fmt.Sprintf("preset %q not found", key), http.StatusNotFound)
		return
	}

	s.applyFeatureTargets(w, r, featurePresetTargets(preset))
}

// applyFeatureTargets writes every requested key, then answers with the full
// catalog so the caller does not have to reason about which descendants a
// parent change made unreachable.
func (s *Server) applyFeatureTargets(w http.ResponseWriter, r *http.Request, targets map[string]bool) {
	actor := s.getUserEmail(r)
	// Deterministic order: catalog order, parents before children.
	for _, def := range featureDefinitions {
		enabled, ok := targets[def.Key]
		if !ok {
			continue
		}
		if _, err := s.featureStore.UpsertFeatureSetting(r.Context(), def.Key, enabled, actor); err != nil {
			slog.Error("update feature setting failed", "key", def.Key, "error", err)
			httpResponse(w, fmt.Sprintf("failed to update feature %q: %v", def.Key, err), http.StatusInternalServerError)
			return
		}
	}
	s.afterFeatureChange(r.Context())

	payload, err := s.featuresPayload(r.Context())
	if err != nil {
		slog.Error("list feature settings failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list features: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, payload, http.StatusOK)
}

// afterFeatureChange drops the cached snapshot and brings the long-running
// subsystems back in line with the new state. It is driven off the effective
// state rather than off "which key was written", so a parent toggle and a child
// toggle converge on the same result.
func (s *Server) afterFeatureChange(ctx context.Context) {
	s.invalidateFeatureCache()

	if s.scheduler != nil {
		enabled, err := s.isFeatureEnabled(ctx, service.FeatureCronTriggers)
		switch {
		case err != nil:
			slog.Error("scheduler feature check failed after feature change", "error", err)
		case enabled:
			if err := s.scheduler.Reload(); err != nil {
				slog.Error("scheduler reload failed after feature change", "error", err)
			}
		default:
			s.scheduler.Stop()
		}
	}

	enabled, err := s.isFeatureEnabled(ctx, service.FeatureBots)
	switch {
	case err != nil:
		slog.Error("bot feature check failed after feature change", "error", err)
	case enabled:
		s.startBotsFromDB(s.ctx)
	default:
		s.stopAllBots()
	}
}
