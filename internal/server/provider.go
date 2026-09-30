package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/gcp"
	"github.com/rakunlabs/at/internal/service/workflow"
	"github.com/rakunlabs/query"
)

// ─── Info API ───

// infoResponse is returned by GET /api/v1/info.
type infoResponse struct {
	Providers     []infoProvider `json:"providers"`
	StoreType     string         `json:"store_type"` // "postgres" or "none"
	Name          string         `json:"name"`       // Server name from config
	User          string         `json:"user,omitempty"`
	Version       string         `json:"version"`
	Commit        string         `json:"commit"`
	BuildDate     string         `json:"build_date"`
	WorkspaceRoot string         `json:"workspace_root"` // Effective task workspace base dir (loopgov.WorkspaceRoot, falls back to /tmp/at-tasks)
	AssetsRoot    string         `json:"assets_root"`    // Persistent asset library root (characters, voices, series) — ./data/assets
	MCPRoot       string         `json:"mcp_root"`       // Persistent MCP program library root (uploaded binaries / config files) — ./data/mcps
}

type infoProvider struct {
	Key          string   `json:"key"`
	Reference    string   `json:"reference,omitempty"`
	Scope        string   `json:"scope,omitempty"`
	Type         string   `json:"type"`
	DefaultModel string   `json:"default_model"`
	Models       []string `json:"models"`
	Shared       bool     `json:"shared,omitempty"`
	// ReasoningEfforts / ModelCapabilities: see service.ProviderCatalogEntry.
	ReasoningEfforts  map[string][]string                  `json:"reasoning_efforts,omitempty"`
	ModelCapabilities map[string]service.ModelCapabilities `json:"model_capabilities,omitempty"`
}

// InfoAPI handles GET /api/v1/info.
// Returns gateway status: registered providers, model counts, store type.
func (s *Server) InfoAPI(w http.ResponseWriter, r *http.Request) {
	s.providerMu.RLock()
	providerList := make([]infoProvider, 0, len(s.providers))
	for key, info := range s.providers {
		models := info.models
		if models == nil {
			models = []string{}
		}
		providerList = append(providerList, infoProvider{
			Key:          key,
			Type:         info.providerType,
			DefaultModel: info.defaultModel,
			Models:       models,
			ReasoningEfforts: service.CatalogReasoningEfforts(info.providerType, models, func(model string) []string {
				return info.modelCapabilities[model].ReasoningEfforts
			}),
			ModelCapabilities: service.CatalogModelCapabilities(info.capabilityConfig(), models),
		})
	}
	s.providerMu.RUnlock()
	if store, ok := s.store.(service.WorkspaceProviderCatalogStorer); ok {
		catalog, err := store.ListWorkspaceProviderCatalog(r.Context())
		if err != nil {
			slog.Error("load workspace provider catalog failed", "error", err)
			if !workspaceBusinessError(w, err) {
				httpResponse(w, "failed to load workspace providers", http.StatusInternalServerError)
			}
			return
		}
		providerList = make([]infoProvider, 0, len(catalog))
		for _, entry := range catalog {
			providerList = append(providerList, infoProvider{Key: entry.Key, Reference: entry.Reference, Scope: entry.Scope, Type: entry.Type, DefaultModel: entry.DefaultModel, Models: entry.Models, Shared: entry.Shared, ReasoningEfforts: entry.ReasoningEfforts, ModelCapabilities: entry.ModelCapabilities})
		}
	}

	storeType := s.storeType

	httpResponseJSON(w, infoResponse{
		Providers:     providerList,
		StoreType:     storeType,
		Name:          s.config.Name,
		User:          s.getUserEmail(r),
		Version:       s.version,
		Commit:        s.commit,
		BuildDate:     s.buildDate,
		WorkspaceRoot: s.taskWorkspaceBase(),
		AssetsRoot:    workflow.AssetsDir(),
		MCPRoot:       workflow.MCPDir(),
	}, http.StatusOK)
}

// ─── Provider CRUD API ───

// providerRequest is the JSON body for creating/updating a provider.
type providerRequest struct {
	Config config.LLMConfig `json:"config"`

	// ClearCredentialsJSON removes a stored Google service-account key and
	// returns the provider to Application Default Credentials. It is a separate
	// flag rather than "an empty credentials_json means delete it", because
	// every writer here — the UI, the MCP provider_update tool, a script —
	// submits the whole config, and a redacted secret it never saw would then
	// be wiped by an edit to an unrelated field.
	ClearCredentialsJSON bool `json:"clear_credentials_json,omitempty"`
}

// validateRateLimitConfig checks that any user-provided rate-limit values
// are sane. Returns a human-readable error message suitable for 400
// responses, or empty string if valid.
func validateRateLimitConfig(rl *config.RateLimitConfig) string {
	if rl == nil {
		return ""
	}
	if rl.RequestsPerMinute < 0 {
		return "rate_limit.requests_per_minute must be >= 0"
	}
	if rl.InputTokensPerMinute < 0 {
		return "rate_limit.input_tokens_per_minute must be >= 0"
	}
	if rl.MaxConcurrent < 0 {
		return "rate_limit.max_concurrent must be >= 0"
	}
	if rl.WaitTimeoutMs < 0 {
		return "rate_limit.wait_timeout_ms must be >= 0"
	}
	// RetryAfterCapMs may be -1 (no cap) or >= 0; reject anything else.
	if rl.RetryAfterCapMs < -1 {
		return "rate_limit.retry_after_cap_ms must be -1 (no cap), 0 (default), or > 0"
	}
	return ""
}

func validateEmbeddingConfig(cfg config.LLMConfig) string {
	if cfg.EmbeddingMaxInputs < 0 {
		return "config.embedding_max_inputs must be >= 0"
	}
	return ""
}

// validateModelLimits checks metadata that is published to gateway clients.
// Both values are required together because discovery clients cannot safely
// infer the missing half of a model's usable token budget.
func validateModelLimits(cfg config.LLMConfig) string {
	advertised := make(map[string]bool, len(cfg.Models)+1)
	if len(cfg.Models) > 0 {
		for _, model := range cfg.Models {
			advertised[model] = true
		}
	} else if cfg.Model != "" {
		advertised[cfg.Model] = true
	}
	for model, limit := range cfg.ModelLimits {
		if strings.TrimSpace(model) == "" {
			return "model_limits keys must not be empty"
		}
		if !advertised[model] {
			return fmt.Sprintf("model_limits.%s does not match an advertised chat model", model)
		}
		if limit.Context <= 0 {
			return fmt.Sprintf("model_limits.%s.context must be > 0", model)
		}
		if limit.Output <= 0 {
			return fmt.Sprintf("model_limits.%s.output must be > 0", model)
		}
		if limit.Output > limit.Context {
			return fmt.Sprintf("model_limits.%s.output must be <= context", model)
		}
	}
	return ""
}

// validateModelCapabilities ensures overrides can only describe chat models
// that the provider actually advertises. An empty entry is ambiguous and is
// rejected; omit it to retain automatic detection.
func validateModelCapabilities(cfg config.LLMConfig) string {
	advertised := make(map[string]bool, len(cfg.Models)+1)
	if len(cfg.Models) > 0 {
		for _, model := range cfg.Models {
			advertised[model] = true
		}
	} else if cfg.Model != "" {
		advertised[cfg.Model] = true
	}
	for model, capability := range cfg.ModelCapabilities {
		if strings.TrimSpace(model) == "" {
			return "model_capabilities keys must not be empty"
		}
		if !advertised[model] {
			return fmt.Sprintf("model_capabilities.%s does not match an advertised chat model", model)
		}
		if capability.ImageInput == nil && capability.InputModalities == nil && capability.OutputModalities == nil && capability.ReasoningEfforts == nil && len(capability.Features) == 0 {
			return fmt.Sprintf("model_capabilities.%s must set image_input, input_modalities, output_modalities, reasoning_efforts or features", model)
		}
		if capability.ImageInput != nil && capability.InputModalities != nil {
			return fmt.Sprintf("model_capabilities.%s: set input_modalities or the legacy image_input, not both", model)
		}
		override := service.CapabilityOverrideFromConfig(cfg.Type, model, capability)
		if capability.ImageInput != nil && override.OutputModalities == nil && override.ReasoningEfforts == nil && len(override.Features) == 0 {
			// Legacy image-only entries are always valid.
			continue
		}
		if err := service.ValidateModelCapabilityOverride(cfg.Type, override); err != nil {
			return fmt.Sprintf("model_capabilities.%s: %v", model, err)
		}
	}
	return ""
}

// providerResponse wraps a single provider record for JSON output.
type providerResponse struct {
	service.ProviderRecord
}

// providersResponse wraps a list of provider records for JSON output.
type providersResponse struct {
	Providers []service.ProviderRecord `json:"providers"`
}

// ListProvidersAPI handles GET /api/v1/providers.
func (s *Server) ListProvidersAPI(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	result, err := s.store.ListProviders(r.Context(), q)
	if err != nil {
		slog.Error("list providers failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list providers: %v", err), http.StatusInternalServerError)
		return
	}

	if result == nil {
		result = &service.ListResult[service.ProviderRecord]{Data: []service.ProviderRecord{}}
	}

	// Redact secrets before sending to the client.
	for i := range result.Data {
		redactProviderRecord(&result.Data[i])
	}

	httpResponseJSON(w, result, http.StatusOK)
}

// GetProviderAPI handles GET /api/v1/providers/:key.
func (s *Server) GetProviderAPI(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	key := r.PathValue("key")
	if key == "" {
		httpResponse(w, "provider key is required", http.StatusBadRequest)
		return
	}

	record, err := s.store.GetProvider(r.Context(), key)
	if err != nil {
		slog.Error("get provider failed", "key", key, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get provider: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("provider %q not found", key), http.StatusNotFound)
		return
	}

	// Redact secrets before sending to the client.
	redactProviderRecord(record)

	httpResponseJSON(w, providerResponse{ProviderRecord: *record}, http.StatusOK)
}

// CreateProviderAPI handles POST /api/v1/providers.
func (s *Server) CreateProviderAPI(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Key    string           `json:"key"`
		Config config.LLMConfig `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Key == "" {
		httpResponse(w, "key is required", http.StatusBadRequest)
		return
	}

	if req.Config.Type == "" {
		httpResponse(w, "config.type is required", http.StatusBadRequest)
		return
	}
	if !service.IsSupportedProviderType(req.Config.Type) {
		httpResponse(w, fmt.Sprintf("unsupported config.type %q (supported: %s)", req.Config.Type, strings.Join(service.SupportedProviderTypes, ", ")), http.StatusBadRequest)
		return
	}

	if msg := validateRateLimitConfig(req.Config.RateLimit); msg != "" {
		httpResponse(w, msg, http.StatusBadRequest)
		return
	}
	if msg := validateEmbeddingConfig(req.Config); msg != "" {
		httpResponse(w, msg, http.StatusBadRequest)
		return
	}
	if msg := validateModelLimits(req.Config); msg != "" {
		httpResponse(w, msg, http.StatusBadRequest)
		return
	}
	if msg := validateModelCapabilities(req.Config); msg != "" {
		httpResponse(w, msg, http.StatusBadRequest)
		return
	}

	if msg := validateProviderCredentialsJSON(req.Config); msg != "" {
		httpResponse(w, msg, http.StatusBadRequest)
		return
	}

	// Check if provider already exists.
	existing, err := s.store.GetProvider(r.Context(), req.Key)
	if err != nil {
		slog.Error("check existing provider failed", "key", req.Key, "error", err)
		httpResponse(w, fmt.Sprintf("failed to check existing provider: %v", err), http.StatusInternalServerError)
		return
	}
	if existing != nil {
		httpResponse(w, fmt.Sprintf("provider %q already exists", req.Key), http.StatusConflict)
		return
	}

	userEmail := s.getUserEmail(r)
	record, err := s.store.CreateProvider(r.Context(), service.ProviderRecord{
		Key:       req.Key,
		Config:    req.Config,
		CreatedBy: userEmail,
		UpdatedBy: userEmail,
	})
	if err != nil {
		slog.Error("create provider failed", "key", req.Key, "error", err)
		if workspaceBusinessError(w, err) {
			return
		}
		httpResponse(w, fmt.Sprintf("failed to create provider: %v", err), http.StatusInternalServerError)
		return
	}

	// Hot reload: register the new provider in the live registry.
	if err := s.reloadWorkspaceProvider(r.Context(), req.Key, req.Config); err != nil {
		slog.Warn("provider created in DB but failed to hot-reload", "key", req.Key, "error", err)
	}

	httpResponseJSON(w, providerResponse{ProviderRecord: *record}, http.StatusCreated)
}

// UpdateProviderAPI handles PUT /api/v1/providers/:key.
func (s *Server) UpdateProviderAPI(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	key := r.PathValue("key")
	if key == "" {
		httpResponse(w, "provider key is required", http.StatusBadRequest)
		return
	}

	var req providerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Config.Type == "" {
		httpResponse(w, "config.type is required", http.StatusBadRequest)
		return
	}
	if !service.IsSupportedProviderType(req.Config.Type) {
		httpResponse(w, fmt.Sprintf("unsupported config.type %q (supported: %s)", req.Config.Type, strings.Join(service.SupportedProviderTypes, ", ")), http.StatusBadRequest)
		return
	}

	if msg := validateRateLimitConfig(req.Config.RateLimit); msg != "" {
		httpResponse(w, msg, http.StatusBadRequest)
		return
	}
	if msg := validateEmbeddingConfig(req.Config); msg != "" {
		httpResponse(w, msg, http.StatusBadRequest)
		return
	}
	if msg := validateModelLimits(req.Config); msg != "" {
		httpResponse(w, msg, http.StatusBadRequest)
		return
	}
	if msg := validateModelCapabilities(req.Config); msg != "" {
		httpResponse(w, msg, http.StatusBadRequest)
		return
	}

	// Preserve managed OAuth fields that redacted UI/API responses omit.
	existing, err := s.store.GetProvider(r.Context(), key)
	if err != nil {
		slog.Error("update provider: failed to read existing config", "key", key, "error", err)
		httpResponse(w, fmt.Sprintf("failed to read existing provider: %v", err), http.StatusInternalServerError)
		return
	}
	if existing != nil {
		preserveProviderManagedAuth(&req.Config, existing.Config)
		preserveProviderCredentialsJSON(&req.Config, existing.Config, req.ClearCredentialsJSON)
		preserveProviderAvailability(&req.Config, existing.Config)
	}

	// Reject an unusable key at the edge rather than at provider construction,
	// where it surfaces as a hot-reload warning in the log and a provider that
	// silently never works.
	if msg := validateProviderCredentialsJSON(req.Config); msg != "" {
		httpResponse(w, msg, http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	record, err := s.store.UpdateProvider(r.Context(), key, service.ProviderRecord{
		Key:       key,
		Config:    req.Config,
		UpdatedBy: userEmail,
	})
	if err != nil {
		slog.Error("update provider failed", "key", key, "error", err)
		if workspaceBusinessError(w, err) {
			return
		}
		httpResponse(w, fmt.Sprintf("failed to update provider: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("provider %q not found", key), http.StatusNotFound)
		return
	}

	// Hot reload: update the provider in the live registry.
	if err := s.reloadWorkspaceProvider(r.Context(), key, req.Config); err != nil {
		slog.Warn("provider updated in DB but failed to hot-reload", "key", key, "error", err)
	}

	httpResponseJSON(w, providerResponse{ProviderRecord: *record}, http.StatusOK)
}

// SetProviderDisabledAPI handles PUT /api/v1/providers/:key/disable.
// Availability is its own endpoint so parking a provider never rewrites its
// credentials, model list or OAuth state, and so an ordinary config save
// cannot resume a provider somebody deliberately stopped.
func (s *Server) SetProviderDisabledAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.ProviderDisableStorer)
	if !ok || s.store == nil {
		httpResponse(w, "store does not support disabling providers", http.StatusServiceUnavailable)
		return
	}

	key := r.PathValue("key")
	if key == "" {
		httpResponse(w, "provider key is required", http.StatusBadRequest)
		return
	}

	// A pointer distinguishes an absent field from an explicit false.
	var req struct {
		Disabled *bool `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Disabled == nil {
		httpResponse(w, "disabled must be a boolean", http.StatusBadRequest)
		return
	}

	if err := store.SetProviderDisabled(r.Context(), key, *req.Disabled, s.getUserEmail(r)); err != nil {
		slog.Error("set provider availability failed", "key", key, "disabled", *req.Disabled, "error", err)
		if workspaceBusinessError(w, err) {
			return
		}
		httpResponse(w, fmt.Sprintf("failed to update provider availability: %v", err), http.StatusInternalServerError)
		return
	}

	// The live registry carries the flag, so it must learn about the change;
	// a stale entry would keep serving a provider the operator just stopped.
	if record, err := s.store.GetProvider(r.Context(), key); err != nil {
		slog.Warn("provider availability changed but reload state could not be read", "key", key, "error", err)
	} else if record != nil {
		if err := s.reloadWorkspaceProvider(r.Context(), key, record.Config); err != nil {
			slog.Warn("provider availability changed but failed to hot-reload", "key", key, "error", err)
		}
	}

	httpResponseJSON(w, map[string]bool{"disabled": *req.Disabled}, http.StatusOK)
}

// DeleteProviderAPI handles DELETE /api/v1/providers/:key.
func (s *Server) DeleteProviderAPI(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	key := r.PathValue("key")
	if key == "" {
		httpResponse(w, "provider key is required", http.StatusBadRequest)
		return
	}

	if err := s.store.DeleteProvider(r.Context(), key); err != nil {
		slog.Error("delete provider failed", "key", key, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete provider: %v", err), http.StatusInternalServerError)
		return
	}

	// Hot reload: remove the provider from the live registry.
	s.removeWorkspaceProvider(r.Context(), key)

	httpResponse(w, "deleted", http.StatusOK)
}

// ─── Helpers ───

// redactProviderRecord replaces secret fields with a sentinel value so the
// UI can tell whether a key is set without exposing the actual secret.
// The sentinel value "***" is recognized by the UI.
func redactProviderRecord(rec *service.ProviderRecord) {
	if rec.Config.APIKey != "" {
		rec.Config.APIKey = "***"
	}
	if rec.Config.RefreshToken != "" {
		rec.Config.RefreshToken = "***"
	}
	if rec.Config.CredentialsJSON != "" {
		rec.Config.CredentialsJSON = redactedSecret
	}
	models := service.ProviderChatModels(rec.Config)
	rec.ReasoningEfforts = service.CatalogReasoningEfforts(rec.Config.Type, models, func(model string) []string {
		return rec.Config.ModelCapabilities[model].ReasoningEfforts
	})
	rec.ModelCapabilities = service.CatalogModelCapabilities(rec.Config, models)
}

// preserveProviderAvailability keeps the disabled flag out of ordinary config
// saves. The UI and the MCP tools submit the full config, so without this an
// edit made while a provider is parked would silently resume it.
func preserveProviderAvailability(next *config.LLMConfig, existing config.LLMConfig) {
	next.Disabled = existing.Disabled
}

// redactedSecret is the placeholder every provider response carries in place
// of a stored secret, and which writers round-trip unchanged.
const redactedSecret = "***"

// validateProviderCredentialsJSON rejects a Google credentials file that
// cannot authenticate anything, at the edge rather than at provider
// construction — where it surfaces only as a hot-reload warning in the log and
// a provider that silently never works. A value on a non-Vertex type is named
// as a mistake instead of being stored as an unused secret.
func validateProviderCredentialsJSON(cfg config.LLMConfig) string {
	if cfg.CredentialsJSON == "" || cfg.CredentialsJSON == redactedSecret {
		return ""
	}

	if cfg.Type != "vertex" && cfg.Type != "vertex-gemini" {
		return fmt.Sprintf("credentials_json applies to the vertex and vertex-gemini provider types, not %q", cfg.Type)
	}

	if _, err := gcp.ParseCredentials(cfg.CredentialsJSON); err != nil {
		return err.Error()
	}

	return ""
}

// preserveProviderCredentialsJSON keeps a stored Google service-account key
// across an edit that did not touch it — the sentinel the writer read back, or
// an omitted field. Unlike api_key this is not auth_type-scoped: the key is
// the credential for both vertex types regardless of auth_type, and clearing
// it is an explicit request (providerRequest.ClearCredentialsJSON).
func preserveProviderCredentialsJSON(next *config.LLMConfig, existing config.LLMConfig, clear bool) {
	if clear {
		next.CredentialsJSON = ""

		return
	}
	if next.CredentialsJSON == "" || next.CredentialsJSON == redactedSecret {
		next.CredentialsJSON = existing.CredentialsJSON
	}
}

func preserveProviderManagedAuth(next *config.LLMConfig, existing config.LLMConfig) {
	if next.AuthType != existing.AuthType {
		return
	}
	if next.APIKey == "" {
		next.APIKey = existing.APIKey
	}
	if next.RefreshToken == "" {
		next.RefreshToken = existing.RefreshToken
	}
	if next.TokenExpiresAt == "" {
		next.TokenExpiresAt = existing.TokenExpiresAt
	}
	if next.AuthType != "chatgpt" || existing.ExtraHeaders["ChatGPT-Account-ID"] == "" {
		return
	}
	if next.ExtraHeaders == nil {
		next.ExtraHeaders = make(map[string]string)
	}
	if next.ExtraHeaders["ChatGPT-Account-ID"] == "" {
		next.ExtraHeaders["ChatGPT-Account-ID"] = existing.ExtraHeaders["ChatGPT-Account-ID"]
	}
}
