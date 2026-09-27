package server

import (
	"context"
	"fmt"
	"regexp"
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
