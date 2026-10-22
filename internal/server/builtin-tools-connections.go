package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Connection Management Tool Executors (Phase 2) ───
//
// Connections are AES-256-GCM-encrypted credential bundles. The MCP
// surface deliberately mirrors the HTTP handlers' redaction policy:
//
//   - List:    secrets always redacted to `*_set: true` booleans
//              (executed via toConnectionResponse(c, false))
//   - Get:     secrets redacted by default; pass reveal=true to
//              receive plaintext (the only place plaintext can leave
//              the process besides the agent's own runtime)
//   - Create:  response redacted (the agent already supplied the
//              secrets, no value in echoing them)
//   - Update:  response redacted; empty secret fields PRESERVE the
//              existing value, matching UpdateConnectionAPI exactly
//              so a fetch-mutate-write loop can't accidentally wipe
//              tokens by leaving them blank.
//   - Delete:  by default fails if any agent references the
//              connection; with force=true, references are stripped
//              from each affected agent before the connection is
//              dropped — same path as DeleteConnectionAPI.

// execConnectionList lists all connections (or filters by provider)
// with secrets always redacted.
func (s *Server) execConnectionList(ctx context.Context, args map[string]any) (string, error) {
	if s.connectionStore == nil {
		return "", fmt.Errorf("connection store not configured")
	}

	var items []service.Connection
	if provider, _ := args["provider"].(string); provider != "" {
		list, err := s.connectionStore.ListConnectionsByProvider(ctx, provider)
		if err != nil {
			return "", fmt.Errorf("list connections by provider: %w", err)
		}
		items = list
	} else {
		res, err := s.connectionStore.ListConnections(ctx, nil)
		if err != nil {
			return "", fmt.Errorf("list connections: %w", err)
		}
		if res != nil {
			items = res.Data
		}
	}

	usage, _ := s.computeConnectionUsage(ctx)
	resp := make([]connectionResponse, 0, len(items))
	for _, c := range items {
		r := toConnectionResponse(c, false)
		r.UsedByAgents = usage[c.ID]
		resp = append(resp, r)
	}
	out, err := json.MarshalIndent(map[string]any{"connections": resp}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal connections: %w", err)
	}
	return string(out), nil
}

// execConnectionGet returns a single connection. By default secrets
// are redacted; reveal=true returns plaintext.
func (s *Server) execConnectionGet(ctx context.Context, args map[string]any) (string, error) {
	if s.connectionStore == nil {
		return "", fmt.Errorf("connection store not configured")
	}
	reveal, _ := args["reveal"].(bool)

	return multiGet(ctx, args, "id", func(ctx context.Context, id string) (string, error) {
		rec, err := s.connectionStore.GetConnection(ctx, id)
		if err != nil {
			return "", fmt.Errorf("get connection %q: %w", id, err)
		}
		if rec == nil {
			return "", fmt.Errorf("connection %q not found", id)
		}

		resp := toConnectionResponse(*rec, reveal)
		usage, _ := s.computeConnectionUsage(ctx)
		resp.UsedByAgents = usage[id]

		out, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal connection: %w", err)
		}
		return string(out), nil
	})
}

// decodeConnectionCredentials coerces an args["credentials"] value
// into a service.ConnectionCredentials. Returns the zero value (with
// no error) for nil input.
func decodeConnectionCredentials(raw any) (service.ConnectionCredentials, error) {
	var creds service.ConnectionCredentials
	if raw == nil {
		return creds, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return creds, fmt.Errorf("marshal: %w", err)
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return creds, fmt.Errorf("invalid credentials object: %w", err)
	}
	return creds, nil
}

// decodeMetadata coerces args["metadata"] into a map[string]any. Nil
// input yields nil map (no error).
func decodeMetadata(raw any) (map[string]any, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		// Fall back to JSON round-trip in case the JSON decoder produced
		// a different shape (e.g. when args came in already unmarshalled
		// as RawMessage).
		data, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("metadata: %w", err)
		}
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("metadata must be an object: %w", err)
		}
	}
	return m, nil
}

func (s *Server) execConnectionCreate(ctx context.Context, args map[string]any) (string, error) {
	if s.connectionStore == nil {
		return "", fmt.Errorf("connection store not configured")
	}
	provider := strings.TrimSpace(stringArg(args, "provider"))
	if provider == "" {
		return "", fmt.Errorf("provider is required")
	}
	name := strings.TrimSpace(stringArg(args, "name"))
	if name == "" {
		return "", fmt.Errorf("name is required")
	}

	creds, err := decodeConnectionCredentials(args["credentials"])
	if err != nil {
		return "", fmt.Errorf("credentials: %w", err)
	}
	metadata, err := decodeMetadata(args["metadata"])
	if err != nil {
		return "", err
	}

	rec, err := s.connectionStore.CreateConnection(ctx, service.Connection{
		Provider:     provider,
		Name:         name,
		AccountLabel: stringArg(args, "account_label"),
		Description:  stringArg(args, "description"),
		Credentials:  creds,
		Metadata:     metadata,
		CreatedBy:    "mcp",
		UpdatedBy:    "mcp",
	})
	if err != nil {
		if isUniqueViolation(err) {
			return "", fmt.Errorf("connection (%s, %s) already exists", provider, name)
		}
		return "", fmt.Errorf("create connection: %w", err)
	}
	resp := toConnectionResponse(*rec, false)
	out, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal connection: %w", err)
	}
	return string(out), nil
}

// execConnectionUpdate mirrors UpdateConnectionAPI's preservation
// rules: empty secret fields keep the existing stored values so the
// caller can rename or relabel without re-supplying tokens.
func (s *Server) execConnectionUpdate(ctx context.Context, args map[string]any) (string, error) {
	if s.connectionStore == nil {
		return "", fmt.Errorf("connection store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}

	existing, err := s.connectionStore.GetConnection(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get connection %q: %w", id, err)
	}
	if existing == nil {
		return "", fmt.Errorf("connection %q not found", id)
	}

	reqCreds, err := decodeConnectionCredentials(args["credentials"])
	if err != nil {
		return "", fmt.Errorf("credentials: %w", err)
	}
	reqMetadata, err := decodeMetadata(args["metadata"])
	if err != nil {
		return "", err
	}

	// Preserve existing secrets when the caller sends empty strings.
	newCreds := existing.Credentials
	if reqCreds.ClientID != "" {
		newCreds.ClientID = reqCreds.ClientID
	}
	if reqCreds.ClientSecret != "" {
		newCreds.ClientSecret = reqCreds.ClientSecret
	}
	if reqCreds.RefreshToken != "" {
		newCreds.RefreshToken = reqCreds.RefreshToken
	}
	if reqCreds.APIKey != "" {
		newCreds.APIKey = reqCreds.APIKey
	}
	if len(reqCreds.Extra) > 0 {
		if newCreds.Extra == nil {
			newCreds.Extra = map[string]string{}
		}
		for k, v := range reqCreds.Extra {
			if v != "" {
				newCreds.Extra[k] = v
			}
		}
	}

	provider := stringArg(args, "provider")
	if provider == "" {
		provider = existing.Provider
	}
	name := stringArg(args, "name")
	if name == "" {
		name = existing.Name
	}
	accountLabel := existing.AccountLabel
	if v := stringArg(args, "account_label"); v != "" {
		accountLabel = v
	}
	description := existing.Description
	if v := stringArg(args, "description"); v != "" {
		description = v
	}
	metadata := existing.Metadata
	if reqMetadata != nil {
		metadata = reqMetadata
	}

	rec, err := s.connectionStore.UpdateConnection(ctx, id, service.Connection{
		Provider:     provider,
		Name:         name,
		AccountLabel: accountLabel,
		Description:  description,
		Credentials:  newCreds,
		Metadata:     metadata,
		UpdatedBy:    "mcp",
	})
	if err != nil {
		if isUniqueViolation(err) {
			return "", fmt.Errorf("connection (%s, %s) already exists", provider, name)
		}
		return "", fmt.Errorf("update connection %q: %w", id, err)
	}
	if rec == nil {
		return "", fmt.Errorf("connection %q not found", id)
	}
	resp := toConnectionResponse(*rec, false)
	out, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal connection: %w", err)
	}
	return string(out), nil
}

// execConnectionDelete refuses to delete an in-use connection unless
// force=true, in which case agent references are stripped first.
func (s *Server) execConnectionDelete(ctx context.Context, args map[string]any) (string, error) {
	if s.connectionStore == nil {
		return "", fmt.Errorf("connection store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	force, _ := args["force"].(bool)

	usage, err := s.computeConnectionUsage(ctx)
	if err != nil {
		return "", fmt.Errorf("compute connection usage: %w", err)
	}
	refs := usage[id]

	if len(refs) > 0 && !force {
		// Surface the same hint the HTTP handler returns so an LLM can
		// programmatically retry with force=true if appropriate.
		blob, _ := json.MarshalIndent(map[string]any{
			"used_by_agents": refs,
			"hint":           "pass force=true to delete and strip references from affected agents",
		}, "", "  ")
		return "", fmt.Errorf("connection is in use: %s", string(blob))
	}

	if len(refs) > 0 {
		if err := s.stripConnectionFromAgents(ctx, id, refs); err != nil {
			return "", fmt.Errorf("strip connection references: %w", err)
		}
	}

	if err := s.connectionStore.DeleteConnection(ctx, id); err != nil {
		return "", fmt.Errorf("delete connection %q: %w", id, err)
	}
	out, _ := json.MarshalIndent(map[string]any{
		"status":               "deleted",
		"id":                   id,
		"detached_from_agents": len(refs),
	}, "", "  ")
	return string(out), nil
}

// execConnectionImportFromVariables reuses the HTTP handler's logic
// for promoting OAuth variable triples to first-class connections.
func (s *Server) execConnectionImportFromVariables(ctx context.Context, _ map[string]any) (string, error) {
	if s.connectionStore == nil || s.variableStore == nil {
		return "", fmt.Errorf("connection store or variable store not configured")
	}

	created := []connectionResponse{}
	skipped := []map[string]string{}

	connectors, err := s.listConnectors(ctx)
	if err != nil {
		return "", fmt.Errorf("list connectors: %w", err)
	}
	for i := range connectors {
		c := &connectors[i]
		if !isOAuth2Connector(c) {
			continue
		}
		providerKey := c.Slug
		clientID, _ := s.variableStore.GetVariableByKey(ctx, connectorVarKey(c, "_client_id"))
		clientSecret, _ := s.variableStore.GetVariableByKey(ctx, connectorVarKey(c, "_client_secret"))
		refreshToken, _ := s.variableStore.GetVariableByKey(ctx, c.Slug+"_refresh_token")

		if clientID == nil || clientSecret == nil {
			continue
		}

		const name = "Imported"
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
			CreatedBy:   "mcp",
			UpdatedBy:   "mcp",
		})
		if err != nil {
			skipped = append(skipped, map[string]string{
				"provider": providerKey,
				"reason":   err.Error(),
			})
			continue
		}
		created = append(created, toConnectionResponse(*rec, false))
	}

	out, err := json.MarshalIndent(map[string]any{
		"created": created,
		"skipped": skipped,
	}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return string(out), nil
}

func (s *Server) execConnectorList(ctx context.Context, _ map[string]any) (string, error) {
	connectors, err := s.listConnectors(ctx)
	if err != nil {
		return "", fmt.Errorf("list connectors: %w", err)
	}
	out, err := json.MarshalIndent(map[string]any{"connectors": connectors}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal connectors: %w", err)
	}
	return string(out), nil
}

// execConnectorSave creates a connector or updates the stored row for its
// slug; saving a built-in slug for the first time creates an override, the
// same as the HTTP editor.
func (s *Server) execConnectorSave(ctx context.Context, args map[string]any) (string, error) {
	if s.connectorStore == nil {
		return "", fmt.Errorf("connector store not configured")
	}
	data, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("encode connector: %w", err)
	}
	var req connectorRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return "", fmt.Errorf("invalid connector: %w", err)
	}
	c, problem := normalizeConnectorRequest(req)
	if problem != "" {
		return "", fmt.Errorf("%s", problem)
	}
	if problem := connectorFieldPrefixProblem(c); problem != "" {
		return "", fmt.Errorf("%s", problem)
	}
	c.CreatedBy, c.UpdatedBy = "mcp", "mcp"
	existing, err := s.connectorStore.GetConnector(ctx, c.Slug)
	if err != nil {
		return "", fmt.Errorf("load connector: %w", err)
	}
	var rec *service.Connector
	action := "created"
	if existing == nil {
		if s.isBuiltinConnector(c.Slug) {
			action = "overridden"
		}
		rec, err = s.connectorStore.CreateConnector(ctx, c)
	} else {
		action = "updated"
		rec, err = s.connectorStore.UpdateConnector(ctx, c.Slug, c)
	}
	if err != nil {
		return "", fmt.Errorf("save connector %q: %w", c.Slug, err)
	}
	out, err := json.MarshalIndent(map[string]any{"status": action, "connector": rec, "oauth_redirect_uri_path": "/api/v1/oauth/callback"}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal connector: %w", err)
	}
	return string(out), nil
}

// connectorFieldPrefixProblem enforces the "<slug>_..." key convention. A key
// with another prefix would be stored but never answered for the tools that
// read it, which looks like a connected account that does not work.
func connectorFieldPrefixProblem(c service.Connector) string {
	for _, f := range c.Fields {
		if !strings.HasPrefix(f.Key, c.Slug+"_") {
			return fmt.Sprintf("field %q must start with %q so tools reading getVar(%q) receive it", f.Key, c.Slug+"_", f.Key)
		}
	}
	return ""
}

func (s *Server) execConnectionBindAgents(ctx context.Context, args map[string]any) (string, error) {
	if s.connectionStore == nil || s.agentStore == nil {
		return "", fmt.Errorf("store not configured")
	}
	id := strings.TrimSpace(stringArg(args, "connection_id"))
	if id == "" {
		return "", fmt.Errorf("connection_id is required")
	}
	conn, err := s.connectionStore.GetConnection(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get connection %q: %w", id, err)
	}
	if conn == nil {
		return "", fmt.Errorf("connection %q not found", id)
	}
	var ids []string
	if raw, ok := args["agent_ids"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				ids = append(ids, strings.TrimSpace(s))
			}
		}
	}
	if len(ids) == 0 || len(ids) > 100 {
		return "", fmt.Errorf("agent_ids must list 1 to 100 agents")
	}
	unbind, _ := args["unbind"].(bool)
	results := make([]connectionBindResult, 0, len(ids))
	for _, agentID := range ids {
		name, err := s.setAgentConnectionBinding(ctx, agentID, conn, unbind, "mcp")
		r := connectionBindResult{AgentID: agentID, Name: name, OK: err == nil}
		if err != nil {
			r.Error = connectionBindError(err)
		}
		results = append(results, r)
	}
	out, err := json.MarshalIndent(map[string]any{"provider": conn.Provider, "connection_id": conn.ID, "results": results}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return string(out), nil
}
