package server

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

var (
	inlineGetVarPattern = regexp.MustCompile(`getVar\(\s*["']([^"']+)["']\s*\)`)
	inlineEnvVarPattern = regexp.MustCompile(`\bVAR_([A-Z][A-Z0-9_]*)\b`)
)

// inlineToolVariables exposes only variables explicitly referenced by the
// migrated handler. This preserves legacy handlers without restoring the old
// behaviour that copied every workspace secret into every shell process.
func (s *Server) inlineToolVariables(ctx context.Context, tool service.MCPInlineTool) (workflow.VarLookup, workflow.VarLister, error) {
	keys := inlineToolVariableKeys(tool.Handler)
	declared := make(map[string]bool, len(keys))
	for _, key := range keys {
		declared[key] = true
	}

	baseLookup := func(key string) (string, error) {
		if s.variableStore == nil {
			return "", nil
		}
		variable, err := s.variableStore.GetVariableByKey(ctx, key)
		if err != nil {
			return "", fmt.Errorf("resolve variable %q: %w", key, err)
		}
		if variable == nil && strings.ToUpper(key) != key {
			variable, err = s.variableStore.GetVariableByKey(ctx, strings.ToUpper(key))
			if err != nil {
				return "", fmt.Errorf("resolve variable %q: %w", key, err)
			}
		}
		if variable == nil {
			return "", nil
		}
		if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "variables.use", ResourceID: variable.ID}); err != nil {
			return "", fmt.Errorf("use variable %q: %w", key, err)
		}
		return variable.Value, nil
	}

	agentConnections, skillConnections := workflow.AgentConnectionsFromContext(ctx, tool.SourceSkillID)
	bindings := workflow.ResolveAgentConnectionBindings(ctx, s.connectionLookupFunc(), agentConnections, skillConnections)
	bindings, err := s.withPersonalConnectionBindings(ctx, keys, bindings)
	if err != nil {
		return nil, nil, err
	}
	resolvedLookup := workflow.WrapVarLookupWithConnectionsContext(ctx, baseLookup, bindings)
	lookup := func(key string) (string, error) {
		if !declared[key] {
			return "", fmt.Errorf("variable %q is not declared by this MCP tool", key)
		}
		return resolvedLookup(key)
	}
	lister := func() (map[string]string, error) {
		values := make(map[string]string, len(keys))
		for _, key := range keys {
			value, err := lookup(key)
			if err != nil {
				return nil, err
			}
			if value != "" {
				values[key] = value
			}
		}
		return values, nil
	}
	return lookup, lister, nil
}

// withPersonalConnectionBindings fills providers the handler references but
// no agent or skill binding names with the run account's own connection, so a
// person who connected their own account (for example YouTube) uses it from
// Chats instead of the installation's global variables. An explicit binding
// always wins; without a personal connection the global variables remain the
// fallback, exactly as before.
func (s *Server) withPersonalConnectionBindings(ctx context.Context, keys []string, bindings workflow.ConnectionBindings) (workflow.ConnectionBindings, error) {
	if s.connectionStore == nil {
		return bindings, nil
	}
	var providers []string
	for _, key := range keys {
		provider, _, ok := workflow.ResolveConnectionKey(key)
		if !ok || slices.Contains(providers, provider) {
			continue
		}
		if _, bound := bindings[provider]; bound {
			continue
		}
		providers = append(providers, provider)
	}
	if len(providers) == 0 {
		return bindings, nil
	}
	identity, err := s.mcpUserIdentity(ctx)
	if err != nil || identity == nil {
		return bindings, nil
	}
	userCtx, err := identity(ctx)
	if err != nil {
		return bindings, nil
	}
	for _, provider := range providers {
		conn, err := s.personalConnectionForUse(userCtx, provider)
		if err != nil {
			return nil, fmt.Errorf("resolve personal %s connection: %w", provider, err)
		}
		if conn == nil {
			continue
		}
		if bindings == nil {
			bindings = workflow.ConnectionBindings{}
		}
		bindings[provider] = conn
	}
	return bindings, nil
}

// personalConnectionForUse returns the caller's most recently updated
// personal connection for provider that actually holds a credential. A
// half-configured row (client ID only, never authorized) is skipped so it
// cannot shadow the global fallback with empty values.
func (s *Server) personalConnectionForUse(ctx context.Context, provider string) (*service.Connection, error) {
	userID := ""
	if p, _, ok := service.ExecutionFromContext(ctx); ok {
		userID = p.UserID
	}
	if userID == "" {
		if p, ok := service.AccessPrincipalFromContext(ctx); ok {
			userID = p.UserID
		}
	}
	if userID == "" {
		return nil, nil
	}
	list, err := s.connectionStore.ListConnectionsByProvider(ctx, provider)
	if err != nil {
		if errors.Is(err, service.ErrAccessDenied) {
			return nil, nil
		}
		return nil, err
	}
	var chosen *service.Connection
	for i := range list {
		c := &list[i]
		if c.OwnerUserID != userID || c.Credentials.MCPOAuth != nil || !connectionHasCredential(c.Credentials) {
			continue
		}
		if chosen == nil || c.UpdatedAt > chosen.UpdatedAt {
			chosen = c
		}
	}
	if chosen == nil {
		return nil, nil
	}
	if _, _, bound := service.ExecutionFromContext(ctx); bound {
		if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "connections.use", ResourceID: chosen.ID}); err != nil {
			return nil, nil
		}
	}
	if resolver, ok := s.store.(service.WorkspaceCredentialStorer); ok {
		return resolver.ResolveConnectionForUse(ctx, chosen.ID)
	}
	return chosen, nil
}

func connectionHasCredential(c service.ConnectionCredentials) bool {
	if c.RefreshToken != "" || c.APIKey != "" {
		return true
	}
	for key, value := range c.Extra {
		if value != "" && strings.HasSuffix(key, "_access_token") {
			return true
		}
	}
	return false
}

func inlineToolVariableKeys(handler string) []string {
	seen := map[string]bool{}
	var keys []string
	add := func(key string) {
		key = strings.TrimSpace(key)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		keys = append(keys, key)
	}
	for _, match := range inlineGetVarPattern.FindAllStringSubmatch(handler, -1) {
		add(match[1])
	}
	for _, match := range inlineEnvVarPattern.FindAllStringSubmatch(handler, -1) {
		add(strings.ToLower(match[1]))
	}
	sort.Strings(keys)
	return keys
}
