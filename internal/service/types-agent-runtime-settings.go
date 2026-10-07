package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

const (
	DefaultMaxBackgroundSubagentsPerOwner = 16
	MaxBackgroundSubagentsPerOwner        = 128
	MaxTrustedLocalMCP                    = 32
)

var ErrAgentRuntimeSettingsConflict = errors.New("agent runtime settings changed; reload before saving")

// AgentRuntimeSettings controls installation-wide limits for ephemeral agent runs.
type AgentRuntimeSettings struct {
	Version                        int64 `json:"version"`
	MaxBackgroundSubagentsPerOwner int   `json:"max_background_subagents_per_owner"`
	// TrustedLocalMCP lists exact loopback MCP endpoints (sidecar MCP servers
	// on this host, e.g. http://127.0.0.1:8890/mcp) that agent runs may dial.
	// Every other loopback URL stays refused. Installation administration:
	// it decides which of the host's private services workspace
	// configuration can reach.
	TrustedLocalMCP []string `json:"trusted_local_mcp"`
}

func DefaultAgentRuntimeSettings() AgentRuntimeSettings {
	return AgentRuntimeSettings{
		Version:                        1,
		MaxBackgroundSubagentsPerOwner: DefaultMaxBackgroundSubagentsPerOwner,
		TrustedLocalMCP:                []string{},
	}
}

func (s AgentRuntimeSettings) Validate() error {
	if s.Version < 1 {
		return fmt.Errorf("invalid settings version")
	}
	if s.MaxBackgroundSubagentsPerOwner < 1 || s.MaxBackgroundSubagentsPerOwner > MaxBackgroundSubagentsPerOwner {
		return fmt.Errorf("max background subagents per owner must be between 1 and %d", MaxBackgroundSubagentsPerOwner)
	}
	if len(s.TrustedLocalMCP) > MaxTrustedLocalMCP {
		return fmt.Errorf("at most %d trusted local MCP endpoints", MaxTrustedLocalMCP)
	}
	for _, raw := range s.TrustedLocalMCP {
		if err := validateTrustedLocalMCP(raw); err != nil {
			return err
		}
	}
	return nil
}

// Normalize trims entries and drops blanks and duplicates.
func (s *AgentRuntimeSettings) Normalize() {
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range s.TrustedLocalMCP {
		key := localMCPKey(raw)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	s.TrustedLocalMCP = out
}

// Only loopback endpoints belong here: anything reachable over the network is
// already allowed and needs no exception.
func validateTrustedLocalMCP(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return fmt.Errorf("trusted local MCP %q must be an http(s) URL", raw)
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("trusted local MCP %q must point to this host (127.0.0.1, ::1 or localhost); network MCP URLs need no exception", raw)
	}
	return nil
}

type AgentRuntimeSettingsStorer interface {
	GetAgentRuntimeSettings(ctx context.Context) (*AgentRuntimeSettings, error)
	SaveAgentRuntimeSettings(ctx context.Context, settings AgentRuntimeSettings) (*AgentRuntimeSettings, error)
}
