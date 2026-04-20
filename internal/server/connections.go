package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ─── Connection CRUD API ───
//
// Connections are named, reusable credential sets for external service
// providers. One row per "account" — multiple YouTube channels, multiple
// Twitter accounts, etc. Agents reference connections by ID.

// connectionResponse is the public shape returned by the API. Secret fields
// are redacted unless the caller explicitly asks for them (?reveal=true).
type connectionResponse struct {
	ID           string                   `json:"id"`
	Provider     string                   `json:"provider"`
	Name         string                   `json:"name"`
	AccountLabel string                   `json:"account_label,omitempty"`
	Description  string                   `json:"description,omitempty"`
	Credentials  connectionCredentialsOut `json:"credentials"`
	Metadata     map[string]any           `json:"metadata,omitempty"`
	CreatedAt    string                   `json:"created_at"`
	UpdatedAt    string                   `json:"updated_at"`
	CreatedBy    string                   `json:"created_by,omitempty"`
	UpdatedBy    string                   `json:"updated_by,omitempty"`
	UsedByAgents []connectionAgentRef     `json:"used_by_agents,omitempty"`
}

// connectionCredentialsOut redacts secrets to booleans by default; when
// ?reveal=true is passed it contains the actual values.
type connectionCredentialsOut struct {
	ClientID        string            `json:"client_id,omitempty"`
	ClientSecretSet bool              `json:"client_secret_set,omitempty"`
	ClientSecret    string            `json:"client_secret,omitempty"`
	RefreshTokenSet bool              `json:"refresh_token_set,omitempty"`
	RefreshToken    string            `json:"refresh_token,omitempty"`
	APIKeySet       bool              `json:"api_key_set,omitempty"`
	APIKey          string            `json:"api_key,omitempty"`
	ExtraKeysSet    []string          `json:"extra_keys_set,omitempty"`
	Extra           map[string]string `json:"extra,omitempty"`
}

// connectionAgentRef is a compact reference to an agent that binds this connection.
type connectionAgentRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Level string `json:"level"` // "agent" or "skill"
}

func toConnectionResponse(c service.Connection, reveal bool) connectionResponse {
	creds := connectionCredentialsOut{
		ClientID: c.Credentials.ClientID,
	}
	if c.Credentials.ClientSecret != "" {
		creds.ClientSecretSet = true
		if reveal {
			creds.ClientSecret = c.Credentials.ClientSecret
		}
	}
	if c.Credentials.RefreshToken != "" {
		creds.RefreshTokenSet = true
		if reveal {
			creds.RefreshToken = c.Credentials.RefreshToken
		}
	}
	if c.Credentials.APIKey != "" {
		creds.APIKeySet = true
		if reveal {
			creds.APIKey = c.Credentials.APIKey
		}
	}
	if len(c.Credentials.Extra) > 0 {
		keys := make([]string, 0, len(c.Credentials.Extra))
		for k := range c.Credentials.Extra {
			keys = append(keys, k)
		}
		creds.ExtraKeysSet = keys
		if reveal {
			extra := make(map[string]string, len(c.Credentials.Extra))
			for k, v := range c.Credentials.Extra {
				extra[k] = v
			}
			creds.Extra = extra
		}
	}
	return connectionResponse{
		ID:           c.ID,
		Provider:     c.Provider,
		Name:         c.Name,
		AccountLabel: c.AccountLabel,
		Description:  c.Description,
		Credentials:  creds,
		Metadata:     c.Metadata,
		CreatedAt:    c.CreatedAt,
		UpdatedAt:    c.UpdatedAt,
		CreatedBy:    c.CreatedBy,
		UpdatedBy:    c.UpdatedBy,
	}
}

// ListConnectionsAPI handles GET /api/v1/connections[?provider=youtube].
// Secrets are always redacted in list responses.
func (s *Server) ListConnectionsAPI(w http.ResponseWriter, r *http.Request) {
	if s.connectionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()

	var items []service.Connection
	if provider := r.URL.Query().Get("provider"); provider != "" {
		list, err := s.connectionStore.ListConnectionsByProvider(ctx, provider)
		if err != nil {
			slog.Error("list connections by provider failed", "provider", provider, "error", err)
			httpResponse(w, fmt.Sprintf("failed to list connections: %v", err), http.StatusInternalServerError)
			return
		}
		items = list
	} else {
		q, err := query.Parse(r.URL.RawQuery)
		if err != nil {
			httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
			return
		}
		res, err := s.connectionStore.ListConnections(ctx, q)
		if err != nil {
			slog.Error("list connections failed", "error", err)
			httpResponse(w, fmt.Sprintf("failed to list connections: %v", err), http.StatusInternalServerError)
			return
		}
		if res != nil {
			items = res.Data
		}
	}

	// Compute used-by-agents counts in one pass across all agents.
	usage, _ := s.computeConnectionUsage(ctx)

	out := make([]connectionResponse, 0, len(items))
	for _, c := range items {
		resp := toConnectionResponse(c, false)
		resp.UsedByAgents = usage[c.ID]
		out = append(out, resp)
	}

	httpResponseJSON(w, out, http.StatusOK)
}

// GetConnectionAPI handles GET /api/v1/connections/{id}[?reveal=true].
func (s *Server) GetConnectionAPI(w http.ResponseWriter, r *http.Request) {
	if s.connectionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "connection id is required", http.StatusBadRequest)
		return
	}

	rec, err := s.connectionStore.GetConnection(r.Context(), id)
	if err != nil {
		slog.Error("get connection failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get connection: %v", err), http.StatusInternalServerError)
		return
	}
	if rec == nil {
		httpResponse(w, fmt.Sprintf("connection %q not found", id), http.StatusNotFound)
		return
	}

	reveal := r.URL.Query().Get("reveal") == "true"
	resp := toConnectionResponse(*rec, reveal)

	usage, _ := s.computeConnectionUsage(r.Context())
	resp.UsedByAgents = usage[rec.ID]

	httpResponseJSON(w, resp, http.StatusOK)
}

// connectionRequest is the body shape for create/update.
type connectionRequest struct {
	Provider     string                        `json:"provider"`
	Name         string                        `json:"name"`
	AccountLabel string                        `json:"account_label"`
	Description  string                        `json:"description"`
	Credentials  service.ConnectionCredentials `json:"credentials"`
	Metadata     map[string]any                `json:"metadata"`
}

// CreateConnectionAPI handles POST /api/v1/connections.
// Use this to pre-register credentials (client_id / client_secret) before
// starting an OAuth flow, or to store a static API key for token-based skills.
func (s *Server) CreateConnectionAPI(w http.ResponseWriter, r *http.Request) {
	if s.connectionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req connectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	req.Provider = strings.TrimSpace(req.Provider)
	req.Name = strings.TrimSpace(req.Name)
	if req.Provider == "" {
		httpResponse(w, "provider is required", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	rec, err := s.connectionStore.CreateConnection(r.Context(), service.Connection{
		Provider:     req.Provider,
		Name:         req.Name,
		AccountLabel: req.AccountLabel,
		Description:  req.Description,
		Credentials:  req.Credentials,
		Metadata:     req.Metadata,
		CreatedBy:    userEmail,
		UpdatedBy:    userEmail,
	})
	if err != nil {
		if isUniqueViolation(err) {
			httpResponse(w, fmt.Sprintf("connection (%s, %s) already exists", req.Provider, req.Name), http.StatusConflict)
			return
		}
		slog.Error("create connection failed", "provider", req.Provider, "name", req.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create connection: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, toConnectionResponse(*rec, false), http.StatusCreated)
}

// UpdateConnectionAPI handles PUT /api/v1/connections/{id}.
// Only fields explicitly set in the request are written; secret fields with
// empty values are preserved (so callers can rename/re-label without having
// to re-supply the refresh token).
func (s *Server) UpdateConnectionAPI(w http.ResponseWriter, r *http.Request) {
	if s.connectionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "connection id is required", http.StatusBadRequest)
		return
	}

	existing, err := s.connectionStore.GetConnection(r.Context(), id)
	if err != nil {
		slog.Error("get connection for update failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to load connection: %v", err), http.StatusInternalServerError)
		return
	}
	if existing == nil {
		httpResponse(w, fmt.Sprintf("connection %q not found", id), http.StatusNotFound)
		return
	}

	var req connectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Preserve existing values when the caller sends empty strings for secrets.
	newCreds := existing.Credentials
	if req.Credentials.ClientID != "" {
		newCreds.ClientID = req.Credentials.ClientID
	}
	if req.Credentials.ClientSecret != "" {
		newCreds.ClientSecret = req.Credentials.ClientSecret
	}
	if req.Credentials.RefreshToken != "" {
		newCreds.RefreshToken = req.Credentials.RefreshToken
	}
	if req.Credentials.APIKey != "" {
		newCreds.APIKey = req.Credentials.APIKey
	}
	if len(req.Credentials.Extra) > 0 {
		if newCreds.Extra == nil {
			newCreds.Extra = map[string]string{}
		}
		for k, v := range req.Credentials.Extra {
			if v != "" {
				newCreds.Extra[k] = v
			}
		}
	}

	provider := req.Provider
	if provider == "" {
		provider = existing.Provider
	}
	name := req.Name
	if name == "" {
		name = existing.Name
	}

	accountLabel := existing.AccountLabel
	if req.AccountLabel != "" {
		accountLabel = req.AccountLabel
	}
	description := existing.Description
	if req.Description != "" {
		description = req.Description
	}
	metadata := existing.Metadata
	if req.Metadata != nil {
		metadata = req.Metadata
	}

	rec, err := s.connectionStore.UpdateConnection(r.Context(), id, service.Connection{
		Provider:     provider,
		Name:         name,
		AccountLabel: accountLabel,
		Description:  description,
		Credentials:  newCreds,
		Metadata:     metadata,
		UpdatedBy:    s.getUserEmail(r),
	})
	if err != nil {
		if isUniqueViolation(err) {
			httpResponse(w, fmt.Sprintf("connection (%s, %s) already exists", provider, name), http.StatusConflict)
			return
		}
		slog.Error("update connection failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update connection: %v", err), http.StatusInternalServerError)
		return
	}
	if rec == nil {
		httpResponse(w, fmt.Sprintf("connection %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, toConnectionResponse(*rec, false), http.StatusOK)
}

// DeleteConnectionAPI handles DELETE /api/v1/connections/{id}[?force=true].
// By default, returns 409 Conflict with the list of agents that reference the
// connection. With ?force=true, the connection is deleted and all references
// are stripped from affected agent configs.
func (s *Server) DeleteConnectionAPI(w http.ResponseWriter, r *http.Request) {
	if s.connectionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "connection id is required", http.StatusBadRequest)
		return
	}

	force := r.URL.Query().Get("force") == "true"

	// Find agents referencing this connection.
	usage, err := s.computeConnectionUsage(r.Context())
	if err != nil {
		slog.Error("compute connection usage failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to check connection usage: %v", err), http.StatusInternalServerError)
		return
	}
	refs := usage[id]

	if len(refs) > 0 && !force {
		httpResponseJSON(w, map[string]any{
			"error":          "connection is in use",
			"used_by_agents": refs,
			"hint":           "pass ?force=true to delete and strip references from affected agents",
		}, http.StatusConflict)
		return
	}

	if len(refs) > 0 {
		if err := s.stripConnectionFromAgents(r.Context(), id, refs); err != nil {
			slog.Error("strip connection references failed", "id", id, "error", err)
			httpResponse(w, fmt.Sprintf("failed to detach connection references: %v", err), http.StatusInternalServerError)
			return
		}
	}

	if err := s.connectionStore.DeleteConnection(r.Context(), id); err != nil {
		slog.Error("delete connection failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete connection: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, map[string]any{
		"status":               "deleted",
		"detached_from_agents": len(refs),
	}, http.StatusOK)
}

// ImportConnectionsFromVariablesAPI handles POST /api/v1/connections/import-from-variables.
// Scans the global variables table for known OAuth provider key triples
// (e.g. youtube_client_id + youtube_client_secret + youtube_refresh_token) and
// creates a Connection row named "Imported (<provider>)" for each complete set.
// The original variables are left in place as a fallback; the new connection
// simply takes priority when an agent is bound to it.
func (s *Server) ImportConnectionsFromVariablesAPI(w http.ResponseWriter, r *http.Request) {
	if s.connectionStore == nil || s.variableStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()
	userEmail := s.getUserEmail(r)
	created := []connectionResponse{}
	skipped := []map[string]string{}

	// Iterate all OAuth provider configs.
	for providerKey, cfg := range oauthProviders {
		clientID, _ := s.variableStore.GetVariableByKey(ctx, cfg.ClientIDVar)
		clientSecret, _ := s.variableStore.GetVariableByKey(ctx, cfg.ClientSecretVar)
		refreshToken, _ := s.variableStore.GetVariableByKey(ctx, cfg.RefreshTokenVar)

		if clientID == nil || clientSecret == nil {
			continue
		}

		name := "Imported"
		// Avoid duplicate-name collision on re-import.
		if existing, _ := s.connectionStore.GetConnectionByName(ctx, providerKey, name); existing != nil {
			skipped = append(skipped, map[string]string{
				"provider": providerKey,
				"reason":   "connection named \"Imported\" already exists",
			})
			continue
		}

		creds := service.ConnectionCredentials{
			ClientID:     clientID.Value,
			ClientSecret: clientSecret.Value,
		}
		if refreshToken != nil {
			creds.RefreshToken = refreshToken.Value
		}

		rec, err := s.connectionStore.CreateConnection(ctx, service.Connection{
			Provider:    providerKey,
			Name:        name,
			Description: "Imported from global variables",
			Credentials: creds,
			CreatedBy:   userEmail,
			UpdatedBy:   userEmail,
		})
		if err != nil {
			slog.Error("import connection failed", "provider", providerKey, "error", err)
			skipped = append(skipped, map[string]string{
				"provider": providerKey,
				"reason":   err.Error(),
			})
			continue
		}
		created = append(created, toConnectionResponse(*rec, false))
	}

	httpResponseJSON(w, map[string]any{
		"created": created,
		"skipped": skipped,
	}, http.StatusOK)
}

// ─── Helpers ───

// computeConnectionUsage builds a map from connection_id to the agents that
// reference it (either at the agent level or inside a skill override).
func (s *Server) computeConnectionUsage(ctx context.Context) (map[string][]connectionAgentRef, error) {
	usage := map[string][]connectionAgentRef{}
	if s.agentStore == nil {
		return usage, nil
	}
	res, err := s.agentStore.ListAgents(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	if res == nil {
		return usage, nil
	}
	for _, a := range res.Data {
		seen := map[string]string{} // conn_id -> level
		for _, connID := range a.Config.Connections {
			if connID != "" && seen[connID] == "" {
				seen[connID] = "agent"
			}
		}
		for _, sr := range a.Config.Skills {
			for _, connID := range sr.Connections {
				if connID != "" && seen[connID] == "" {
					seen[connID] = "skill"
				}
			}
		}
		for connID, level := range seen {
			usage[connID] = append(usage[connID], connectionAgentRef{
				ID:    a.ID,
				Name:  a.Name,
				Level: level,
			})
		}
	}
	return usage, nil
}

// stripConnectionFromAgents removes all references to connID from the
// Connections map and any SkillRef.Connections map on the given agents.
func (s *Server) stripConnectionFromAgents(ctx context.Context, connID string, refs []connectionAgentRef) error {
	for _, ref := range refs {
		agent, err := s.agentStore.GetAgent(ctx, ref.ID)
		if err != nil {
			return fmt.Errorf("load agent %q: %w", ref.ID, err)
		}
		if agent == nil {
			continue
		}
		changed := false
		for provider, id := range agent.Config.Connections {
			if id == connID {
				delete(agent.Config.Connections, provider)
				changed = true
			}
		}
		for i := range agent.Config.Skills {
			for provider, id := range agent.Config.Skills[i].Connections {
				if id == connID {
					delete(agent.Config.Skills[i].Connections, provider)
					changed = true
				}
			}
		}
		if !changed {
			continue
		}
		if _, err := s.agentStore.UpdateAgent(ctx, ref.ID, *agent); err != nil {
			return fmt.Errorf("update agent %q: %w", ref.ID, err)
		}
	}
	return nil
}

// isUniqueViolation heuristically detects SQL/store uniqueness errors.
// Both sqlite and postgres surface the offending constraint/text in the
// error message.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unique") ||
		strings.Contains(msg, "already exists") ||
		strings.Contains(msg, "duplicate") {
		return true
	}
	var dup interface{ Error() string }
	return errors.As(err, &dup) && strings.Contains(strings.ToLower(dup.Error()), "unique")
}
