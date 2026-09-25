package service

import (
	"fmt"
	"net"
	"strings"
)

const VariableUseToolHTTPRequest = "http_request"

// ValidateVariableUsePolicy validates the non-secret policy that controls
// where a runtime may resolve a variable reference. An empty policy preserves
// the historical behaviour: the variable can be managed but not referenced by
// a model-driven tool.
func ValidateVariableUsePolicy(v Variable) error {
	seenTools := map[string]bool{}
	for _, tool := range v.AllowedTools {
		tool = strings.TrimSpace(tool)
		if tool != VariableUseToolHTTPRequest {
			return fmt.Errorf("unsupported variable-use tool %q", tool)
		}
		if tool == "" || seenTools[tool] {
			return fmt.Errorf("duplicate or empty variable-use tool %q", tool)
		}
		seenTools[tool] = true
	}
	if len(v.AllowedHosts) > 0 && !seenTools[VariableUseToolHTTPRequest] {
		return fmt.Errorf("allowed_hosts requires %q in allowed_tools", VariableUseToolHTTPRequest)
	}
	if seenTools[VariableUseToolHTTPRequest] && len(v.AllowedHosts) == 0 {
		return fmt.Errorf("%q variable use requires at least one allowed host", VariableUseToolHTTPRequest)
	}
	seenHosts := map[string]bool{}
	for _, raw := range v.AllowedHosts {
		host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
		if host == "" || strings.ContainsAny(host, "/:@?#") {
			return fmt.Errorf("invalid variable-use host %q", raw)
		}
		candidate := host
		wildcard := false
		if strings.HasPrefix(candidate, "*.") {
			wildcard = true
			candidate = strings.TrimPrefix(candidate, "*.")
			if candidate == "" {
				return fmt.Errorf("invalid variable-use host %q", raw)
			}
		}
		if wildcard && net.ParseIP(candidate) != nil {
			return fmt.Errorf("invalid variable-use host %q", raw)
		}
		if net.ParseIP(candidate) == nil && !validDNSName(candidate) {
			return fmt.Errorf("invalid variable-use host %q", raw)
		}
		if seenHosts[host] {
			return fmt.Errorf("duplicate variable-use host %q", raw)
		}
		seenHosts[host] = true
	}
	return nil
}

func validDNSName(host string) bool {
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

func VariableAllowsTool(v Variable, tool string) bool {
	for _, allowed := range v.AllowedTools {
		if allowed == tool {
			return true
		}
	}
	return false
}

// VariableAllowsHost matches exact hosts and explicit wildcard subdomains.
// "*.example.com" matches "api.example.com", but not "example.com".
func VariableAllowsHost(v Variable, requestHost string) bool {
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(requestHost), "."))
	for _, raw := range v.AllowedHosts {
		allowed := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
		if host == allowed {
			return true
		}
		if strings.HasPrefix(allowed, "*.") {
			suffix := strings.TrimPrefix(allowed, "*")
			if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
				return true
			}
		}
	}
	return false
}
