package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

type featureGroupDefinition struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type featureDefinition struct {
	Key         string
	Name        string
	Description string
	Group       string
}

type featureResponse struct {
	Key              string `json:"key"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	Group            string `json:"group"`
	GroupName        string `json:"group_name"`
	GroupDescription string `json:"group_description"`
	Enabled          bool   `json:"enabled"`
	CreatedAt        string `json:"created_at,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
	CreatedBy        string `json:"created_by,omitempty"`
	UpdatedBy        string `json:"updated_by,omitempty"`
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
}

var featureGroupDefinitions = []featureGroupDefinition{
	{
		Key:         "llm_gateway",
		Name:        "LLM Gateway",
		Description: "Provider setup and model access controls.",
	},
	{
		Key:         "workspace",
		Name:        "Workspace",
		Description: "Interactive chat and reusable agent workspace features.",
	},
	{
		Key:         "automation",
		Name:        "Automation",
		Description: "Workflow builder, runs, node configs, webhooks, and cron triggers.",
	},
	{
		Key:         "data_integrations",
		Name:        "Data & Integrations",
		Description: "RAG, file browser, external connections, and integration packs.",
	},
	{
		Key:         "operations",
		Name:        "Operations",
		Description: "Organization, task, delegation, and governance features.",
	},
}

var featureDefinitions = []featureDefinition{
	{
		Key:         service.FeatureProviderSetup,
		Name:        "Provider Setup",
		Description: "Allow creating, editing, deleting, authorizing, and discovering models for providers. Existing providers remain usable by the gateway and dependent UI screens.",
		Group:       "llm_gateway",
	},
	{
		Key:         service.FeatureChatWorkbench,
		Name:        "Chat Workbench",
		Description: "Show and enable the Chat and Sessions UI, admin chat completions, chat-session storage, and chat tool endpoints.",
		Group:       "workspace",
	},
	{
		Key:         service.FeatureAgents,
		Name:        "Agents",
		Description: "Show and enable agent definitions, import/export, runtime state, wakeups, heartbeat runs, and agent-related API endpoints.",
		Group:       "workspace",
	},
	{
		Key:         service.FeatureAutomation,
		Name:        "Automation",
		Description: "Show and enable workflows, workflow runs, node configs, webhooks, cron triggers, and workflow/trigger built-in tools.",
		Group:       "automation",
	},
	{
		Key:         service.FeatureRAG,
		Name:        "RAG",
		Description: "Show and enable RAG collections, document ingestion, search, embedding tests, and RAG chat tools.",
		Group:       "data_integrations",
	},
	{
		Key:         service.FeatureFiles,
		Name:        "Files",
		Description: "Show and enable the workspace file browser and file_* built-in tools.",
		Group:       "data_integrations",
	},
	{
		Key:         service.FeatureConnections,
		Name:        "Connections & Integrations",
		Description: "Show and enable external-service connections, connector management, OAuth flows, integration packs, and pack sources.",
		Group:       "data_integrations",
	},
	{
		Key:         service.FeatureOrganizationWorkflows,
		Name:        "Organization Workflows",
		Description: "Show and enable organizations, org-agent membership, task delegation, goals, projects, approvals, labels, comments, and cost event screens.",
		Group:       "operations",
	},
	{
		Key:         service.FeatureLLMAudit,
		Name:        "LLM Call Audit",
		Description: "Record full request/response bodies of every gateway LLM call for tracing and debugging (Langfuse-style). Emits OTEL gen-ai spans when telemetry is configured. Bodies are retained for 7 days. Disable to stop capturing request/response content.",
		Group:       "operations",
	},
}

func featureDefinitionForKey(key string) (featureDefinition, bool) {
	for _, def := range featureDefinitions {
		if def.Key == key {
			return def, true
		}
	}

	return featureDefinition{}, false
}

func featureGroupDefinitionForKey(key string) featureGroupDefinition {
	for _, group := range featureGroupDefinitions {
		if group.Key == key {
			return group
		}
	}

	return featureGroupDefinition{Key: key, Name: key}
}

func featureResponseFromSetting(def featureDefinition, setting *service.FeatureSetting) featureResponse {
	group := featureGroupDefinitionForKey(def.Group)
	res := featureResponse{
		Key:              def.Key,
		Name:             def.Name,
		Description:      def.Description,
		Group:            group.Key,
		GroupName:        group.Name,
		GroupDescription: group.Description,
		Enabled:          true,
	}
	if setting != nil {
		res.Enabled = setting.Enabled
		res.CreatedAt = setting.CreatedAt
		res.UpdatedAt = setting.UpdatedAt
		res.CreatedBy = setting.CreatedBy
		res.UpdatedBy = setting.UpdatedBy
	}

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

// ListFeaturesAPI handles GET /api/v1/features.
func (s *Server) ListFeaturesAPI(w http.ResponseWriter, r *http.Request) {
	settings, err := s.featureSettingsByKey(r.Context())
	if err != nil {
		slog.Error("list feature settings failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list features: %v", err), http.StatusInternalServerError)
		return
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
		feature := featureResponseFromSetting(def, setting)
		features = append(features, feature)
		idx, ok := groupIndex[def.Group]
		if !ok {
			continue
		}
		groups[idx].Features = append(groups[idx].Features, feature)
	}

	httpResponseJSON(w, featuresResponse{Groups: groups, Features: features}, http.StatusOK)
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
	if key == service.FeatureAutomation && s.scheduler != nil {
		if *req.Enabled {
			if err := s.scheduler.Reload(); err != nil {
				slog.Error("scheduler reload failed after automation feature enable", "error", err)
			}
		} else {
			s.scheduler.Stop()
		}
	}
	if key == service.FeatureChatWorkbench {
		if *req.Enabled {
			s.startBotsFromDB(s.ctx)
		} else {
			s.stopAllBots()
		}
	}

	httpResponseJSON(w, featureResponseFromSetting(def, setting), http.StatusOK)
}

func (s *Server) isFeatureEnabled(ctx context.Context, key string) (bool, error) {
	if key == "" {
		return true, nil
	}
	if _, ok := featureDefinitionForKey(key); !ok {
		return true, nil
	}
	if s.featureStore == nil {
		return true, nil
	}

	setting, err := s.featureStore.GetFeatureSetting(ctx, key)
	if err != nil {
		return false, err
	}
	if setting == nil {
		return true, nil
	}

	return setting.Enabled, nil
}

func (s *Server) featureGateMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			featureKeys := featureKeysForAPIRequest(r.URL.Path, r.Method, s.config.BasePath)
			if len(featureKeys) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			for _, featureKey := range featureKeys {
				enabled, err := s.isFeatureEnabled(r.Context(), featureKey)
				if err != nil {
					slog.Error("check feature setting failed", "feature", featureKey, "error", err)
					httpResponse(w, fmt.Sprintf("failed to check feature %q: %v", featureKey, err), http.StatusInternalServerError)
					return
				}
				if !enabled {
					httpResponse(w, fmt.Sprintf("feature %q is disabled", featureKey), http.StatusNotFound)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

func featureKeyForAPIRequest(path, method, basePath string) string {
	keys := featureKeysForAPIRequest(path, method, basePath)
	if len(keys) == 0 {
		return ""
	}

	return keys[0]
}

func featureKeysForAPIRequest(path, method, basePath string) []string {
	if method == http.MethodOptions {
		return nil
	}

	if basePath != "" {
		path = strings.TrimPrefix(path, strings.TrimRight(basePath, "/"))
	}
	if strings.HasPrefix(path, "/webhooks/") {
		return []string{service.FeatureAutomation}
	}
	if !strings.HasPrefix(path, "/api/v1") {
		return nil
	}

	apiPath := strings.TrimPrefix(path, "/api/v1")
	if apiPath == "" {
		apiPath = "/"
	}

	switch {
	case apiPath == "/providers":
		if method == http.MethodGet {
			return nil
		}
		return []string{service.FeatureProviderSetup}
	case strings.HasPrefix(apiPath, "/providers/"):
		return []string{service.FeatureProviderSetup}
	case strings.HasPrefix(apiPath, "/model-pricing"):
		return []string{service.FeatureProviderSetup}
	case apiPath == "/workflow-node-types" || strings.HasPrefix(apiPath, "/workflows") ||
		strings.HasPrefix(apiPath, "/triggers") || strings.HasPrefix(apiPath, "/runs") ||
		strings.HasPrefix(apiPath, "/node-configs") || strings.HasPrefix(apiPath, "/rag/collections/") && strings.Contains(apiPath, "/triggers"):
		if strings.HasPrefix(apiPath, "/rag/") {
			return []string{service.FeatureAutomation, service.FeatureRAG}
		}
		return []string{service.FeatureAutomation}
	case apiPath == "/chat/completions" || strings.HasPrefix(apiPath, "/chat/sessions"):
		return []string{service.FeatureChatWorkbench}
	case apiPath == "/mcp/list-tools" || apiPath == "/mcp/call-tool" ||
		apiPath == "/mcp/call-skill-tool" || apiPath == "/mcp/builtin-tools" ||
		apiPath == "/mcp/call-builtin-tool" || strings.HasPrefix(apiPath, "/mcp/set-tools/"):
		return []string{service.FeatureChatWorkbench}
	case apiPath == "/mcp/rag-tools" || apiPath == "/mcp/call-rag-tool":
		return []string{service.FeatureChatWorkbench, service.FeatureRAG}
	case apiPath == "/audio/transcribe":
		return []string{service.FeatureChatWorkbench}
	case strings.HasPrefix(apiPath, "/bots"):
		return []string{service.FeatureChatWorkbench}
	case strings.HasPrefix(apiPath, "/rag"):
		return []string{service.FeatureRAG}
	case strings.HasPrefix(apiPath, "/files"):
		return []string{service.FeatureFiles}
	case strings.HasPrefix(apiPath, "/connections") || strings.HasPrefix(apiPath, "/connectors") ||
		strings.HasPrefix(apiPath, "/oauth") || strings.HasPrefix(apiPath, "/integration-packs") ||
		strings.HasPrefix(apiPath, "/pack-sources"):
		return []string{service.FeatureConnections}
	case strings.HasPrefix(apiPath, "/agents") || apiPath == "/heartbeats" || strings.HasPrefix(apiPath, "/heartbeat-runs") || strings.HasPrefix(apiPath, "/wakeup-requests") || strings.HasPrefix(apiPath, "/agent-config-revisions"):
		return []string{service.FeatureAgents}
	case strings.HasPrefix(apiPath, "/organizations") || strings.HasPrefix(apiPath, "/tasks") ||
		apiPath == "/active-delegations" || strings.HasPrefix(apiPath, "/goals") ||
		strings.HasPrefix(apiPath, "/projects") || strings.HasPrefix(apiPath, "/comments") ||
		strings.HasPrefix(apiPath, "/labels") || strings.HasPrefix(apiPath, "/approvals") ||
		strings.HasPrefix(apiPath, "/cost-events"):
		return []string{service.FeatureOrganizationWorkflows}
	case strings.HasPrefix(apiPath, "/llm-calls"):
		return []string{service.FeatureLLMAudit}
	default:
		return nil
	}
}

func builtinToolFeatureKey(name string) string {
	if strings.HasPrefix(name, "workflow_") || strings.HasPrefix(name, "trigger_") {
		return service.FeatureAutomation
	}
	if strings.HasPrefix(name, "file_") {
		return service.FeatureFiles
	}

	return ""
}
