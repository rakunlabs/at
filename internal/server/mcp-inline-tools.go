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
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		if s.variableStore == nil {
			return nil, nil, fmt.Errorf("variable store not configured")
		}
		variable, err := s.variableStore.GetVariableByKey(ctx, key)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve variable %q: %w", key, err)
		}
		if variable == nil && strings.ToUpper(key) != key {
			variable, err = s.variableStore.GetVariableByKey(ctx, strings.ToUpper(key))
			if err != nil {
				return nil, nil, fmt.Errorf("resolve variable %q: %w", key, err)
			}
		}
		if variable == nil {
			continue
		}
		if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "variables.use", ResourceID: variable.ID}); err != nil {
			return nil, nil, fmt.Errorf("use variable %q: %w", key, err)
		}
		values[key] = variable.Value
	}
	lookup := func(key string) (string, error) {
		value, ok := values[key]
		if !ok {
			return "", fmt.Errorf("variable %q is not declared by this MCP tool", key)
		}
		return value, nil
	}
	lister := func() (map[string]string, error) {
		copyValues := make(map[string]string, len(values))
		for key, value := range values {
			copyValues[key] = value
		}
		return copyValues, nil
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
