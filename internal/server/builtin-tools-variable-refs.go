package server

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

const variableRefPrefix = "variable://"

type variableUseResolver interface {
	ResolveVariableForUse(context.Context, string) (*service.Variable, error)
}

type resolvedHTTPHeader struct {
	Value  string
	Secret string
}

// resolveHTTPHeaderValue resolves the explicit reference object accepted by
// http_request header values. Ordinary scalar headers retain their historical
// behaviour. References are never expanded in URLs, commands, logs, or generic
// tool arguments.
func (s *Server) resolveHTTPHeaderValue(ctx context.Context, target *url.URL, raw any) (resolvedHTTPHeader, error) {
	ref, ok := raw.(map[string]any)
	if !ok {
		return resolvedHTTPHeader{Value: fmt.Sprintf("%v", raw)}, nil
	}
	refValue, _ := ref["$ref"].(string)
	if !strings.HasPrefix(refValue, variableRefPrefix) {
		return resolvedHTTPHeader{}, fmt.Errorf("HTTP header object must contain a variable:// $ref")
	}
	for field := range ref {
		if field != "$ref" && field != "prefix" && field != "suffix" {
			return resolvedHTTPHeader{}, fmt.Errorf("variable reference supports only $ref, prefix, and suffix")
		}
	}
	key := strings.TrimSpace(strings.TrimPrefix(refValue, variableRefPrefix))
	if key == "" || len(key) > 256 || strings.ContainsAny(key, "/?#") {
		return resolvedHTTPHeader{}, fmt.Errorf("invalid variable reference")
	}
	prefix, prefixOK := ref["prefix"].(string)
	if _, exists := ref["prefix"]; exists && !prefixOK {
		return resolvedHTTPHeader{}, fmt.Errorf("variable reference prefix must be a string")
	}
	suffix, suffixOK := ref["suffix"].(string)
	if _, exists := ref["suffix"]; exists && !suffixOK {
		return resolvedHTTPHeader{}, fmt.Errorf("variable reference suffix must be a string")
	}
	if len(prefix)+len(suffix) > 1024 || strings.ContainsAny(prefix+suffix, "\r\n") {
		return resolvedHTTPHeader{}, fmt.Errorf("invalid variable reference prefix or suffix")
	}
	if target == nil || target.Scheme != "https" || target.Hostname() == "" {
		return resolvedHTTPHeader{}, fmt.Errorf("secret variable references require an HTTPS target")
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "variables.use", ResourceID: key}); err != nil {
		return resolvedHTTPHeader{}, err
	}
	resolver, ok := s.variableStore.(variableUseResolver)
	if !ok {
		return resolvedHTTPHeader{}, fmt.Errorf("variable-use resolver not configured")
	}
	variable, err := resolver.ResolveVariableForUse(ctx, key)
	if err != nil {
		return resolvedHTTPHeader{}, fmt.Errorf("resolve variable reference %q: %w", key, err)
	}
	if variable == nil {
		return resolvedHTTPHeader{}, fmt.Errorf("variable reference %q not found", key)
	}
	if !variable.Secret {
		return resolvedHTTPHeader{}, fmt.Errorf("variable reference %q is not secret; pass its value directly", key)
	}
	if !service.VariableAllowsTool(*variable, service.VariableUseToolHTTPRequest) || !service.VariableAllowsHost(*variable, target.Hostname()) {
		return resolvedHTTPHeader{}, fmt.Errorf("variable reference %q is not allowed for http_request to %q", key, target.Hostname())
	}
	if variable.Value == "" || variable.Value == "***" {
		return resolvedHTTPHeader{}, fmt.Errorf("variable reference %q is unavailable", key)
	}
	return resolvedHTTPHeader{Value: prefix + variable.Value + suffix, Secret: variable.Value}, nil
}

func redactResolvedSecrets(value string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "***")
		}
	}
	return value
}
