package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rakunlabs/at/internal/service"
)

// ─── MCP Server / MCP Set Management Tool Executors ───
//
// These executors close the gap between the UI (which has full CRUD on
// general MCP servers and internal MCP Sets via /api/v1/mcp/servers and
// /api/v1/mcp/sets) and the management MCP, which previously could not
// register new MCP backends or compose new internal MCP sets at all.
//
// Both objects share the same MCPServerConfig shape (see types-mcp.go);
// the difference is intent:
//   - MCPServer  → exposed publicly at the gateway as a named MCP endpoint
//   - MCPSet     → consumed internally by agents via agent.mcp_sets
//
// The Update operations are full replacements rather than partial merges.
// We expose the existing record's name as `required` in the tool schema
// so callers always supply it (the store rejects empty names anyway), and
// we recommend a fetch-mutate-write pattern to LLM clients in the tool
// description. Trying to do a partial merge for nested config is awkward
// because slice fields like enabled_builtin_tools have meaningful "empty"
// states (an empty list disables all builtins, distinct from "not provided").

// decodeMCPServerConfig coerces an args["config"] value into a
// service.MCPServerConfig. Returns the zero value (with no error) when
// raw is nil so callers can distinguish "config omitted" from invalid
// input.
func decodeMCPServerConfig(raw any) (service.MCPServerConfig, error) {
	var cfg service.MCPServerConfig
	if raw == nil {
		return cfg, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return cfg, fmt.Errorf("marshal: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid config object: %w", err)
	}
	return cfg, nil
}

// ─── General MCP Servers ───

func (s *Server) execMCPServerList(ctx context.Context, _ map[string]any) (string, error) {
	if s.mcpServerStore == nil {
		return "", fmt.Errorf("mcp server store not configured")
	}
	records, err := s.mcpServerStore.ListMCPServers(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("list mcp servers: %w", err)
	}
	if records == nil {
		records = &service.ListResult[service.MCPServer]{Data: []service.MCPServer{}}
	}
	out, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal mcp servers: %w", err)
	}
	return string(out), nil
}

func (s *Server) execMCPServerGet(ctx context.Context, args map[string]any) (string, error) {
	if s.mcpServerStore == nil {
		return "", fmt.Errorf("mcp server store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	record, err := s.mcpServerStore.GetMCPServer(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get mcp server %q: %w", id, err)
	}
	if record == nil {
		return "", fmt.Errorf("mcp server %q not found", id)
	}
	out, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal mcp server: %w", err)
	}
	return string(out), nil
}

func (s *Server) execMCPServerCreate(ctx context.Context, args map[string]any) (string, error) {
	if s.mcpServerStore == nil {
		return "", fmt.Errorf("mcp server store not configured")
	}
	name, _ := args["name"].(string)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}

	cfg, err := decodeMCPServerConfig(args["config"])
	if err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	servers, err := decodeStringSlice(args["servers"])
	if err != nil {
		return "", fmt.Errorf("servers: %w", err)
	}
	urls, err := decodeStringSlice(args["urls"])
	if err != nil {
		return "", fmt.Errorf("urls: %w", err)
	}

	server := service.MCPServer{
		Name:        name,
		Description: stringArg(args, "description"),
		Config:      cfg,
		Servers:     servers,
		URLs:        urls,
		CreatedBy:   "mcp",
		UpdatedBy:   "mcp",
	}
	record, err := s.mcpServerStore.CreateMCPServer(ctx, server)
	if err != nil {
		return "", fmt.Errorf("create mcp server: %w", err)
	}
	out, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal mcp server: %w", err)
	}
	return string(out), nil
}

func (s *Server) execMCPServerUpdate(ctx context.Context, args map[string]any) (string, error) {
	if s.mcpServerStore == nil {
		return "", fmt.Errorf("mcp server store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	name, _ := args["name"].(string)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}

	cfg, err := decodeMCPServerConfig(args["config"])
	if err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	servers, err := decodeStringSlice(args["servers"])
	if err != nil {
		return "", fmt.Errorf("servers: %w", err)
	}
	urls, err := decodeStringSlice(args["urls"])
	if err != nil {
		return "", fmt.Errorf("urls: %w", err)
	}

	server := service.MCPServer{
		Name:        name,
		Description: stringArg(args, "description"),
		Config:      cfg,
		Servers:     servers,
		URLs:        urls,
		UpdatedBy:   "mcp",
	}
	record, err := s.mcpServerStore.UpdateMCPServer(ctx, id, server)
	if err != nil {
		return "", fmt.Errorf("update mcp server: %w", err)
	}
	if record == nil {
		return "", fmt.Errorf("mcp server %q not found", id)
	}
	out, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal mcp server: %w", err)
	}
	return string(out), nil
}

func (s *Server) execMCPServerDelete(ctx context.Context, args map[string]any) (string, error) {
	if s.mcpServerStore == nil {
		return "", fmt.Errorf("mcp server store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := s.mcpServerStore.DeleteMCPServer(ctx, id); err != nil {
		return "", fmt.Errorf("delete mcp server %q: %w", id, err)
	}
	return fmt.Sprintf(`{"status":"deleted","id":%q}`, id), nil
}

// ─── MCP Sets ───

func (s *Server) execMCPSetList(ctx context.Context, _ map[string]any) (string, error) {
	if s.mcpSetStore == nil {
		return "", fmt.Errorf("mcp set store not configured")
	}
	records, err := s.mcpSetStore.ListMCPSets(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("list mcp sets: %w", err)
	}
	if records == nil {
		records = &service.ListResult[service.MCPSet]{Data: []service.MCPSet{}}
	}
	out, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal mcp sets: %w", err)
	}
	return string(out), nil
}

func (s *Server) execMCPSetGet(ctx context.Context, args map[string]any) (string, error) {
	if s.mcpSetStore == nil {
		return "", fmt.Errorf("mcp set store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	record, err := s.mcpSetStore.GetMCPSet(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get mcp set %q: %w", id, err)
	}
	if record == nil {
		return "", fmt.Errorf("mcp set %q not found", id)
	}
	out, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal mcp set: %w", err)
	}
	return string(out), nil
}

func (s *Server) execMCPSetCreate(ctx context.Context, args map[string]any) (string, error) {
	if s.mcpSetStore == nil {
		return "", fmt.Errorf("mcp set store not configured")
	}
	name, _ := args["name"].(string)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}

	cfg, err := decodeMCPServerConfig(args["config"])
	if err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	servers, err := decodeStringSlice(args["servers"])
	if err != nil {
		return "", fmt.Errorf("servers: %w", err)
	}
	urls, err := decodeStringSlice(args["urls"])
	if err != nil {
		return "", fmt.Errorf("urls: %w", err)
	}
	tags, err := decodeStringSlice(args["tags"])
	if err != nil {
		return "", fmt.Errorf("tags: %w", err)
	}

	// Mirror the HTTP handler: persist empty arrays rather than nil so the
	// stored record has a stable shape regardless of how callers express
	// "no entries".
	if servers == nil {
		servers = []string{}
	}
	if urls == nil {
		urls = []string{}
	}

	set := service.MCPSet{
		Name:        name,
		Description: stringArg(args, "description"),
		Category:    stringArg(args, "category"),
		Tags:        tags,
		Config:      cfg,
		Servers:     servers,
		URLs:        urls,
		CreatedBy:   "mcp",
		UpdatedBy:   "mcp",
	}
	record, err := s.mcpSetStore.CreateMCPSet(ctx, set)
	if err != nil {
		return "", fmt.Errorf("create mcp set: %w", err)
	}
	out, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal mcp set: %w", err)
	}
	return string(out), nil
}

func (s *Server) execMCPSetUpdate(ctx context.Context, args map[string]any) (string, error) {
	if s.mcpSetStore == nil {
		return "", fmt.Errorf("mcp set store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	name, _ := args["name"].(string)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}

	cfg, err := decodeMCPServerConfig(args["config"])
	if err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	servers, err := decodeStringSlice(args["servers"])
	if err != nil {
		return "", fmt.Errorf("servers: %w", err)
	}
	urls, err := decodeStringSlice(args["urls"])
	if err != nil {
		return "", fmt.Errorf("urls: %w", err)
	}
	tags, err := decodeStringSlice(args["tags"])
	if err != nil {
		return "", fmt.Errorf("tags: %w", err)
	}

	if servers == nil {
		servers = []string{}
	}
	if urls == nil {
		urls = []string{}
	}

	set := service.MCPSet{
		Name:        name,
		Description: stringArg(args, "description"),
		Category:    stringArg(args, "category"),
		Tags:        tags,
		Config:      cfg,
		Servers:     servers,
		URLs:        urls,
		UpdatedBy:   "mcp",
	}
	record, err := s.mcpSetStore.UpdateMCPSet(ctx, id, set)
	if err != nil {
		return "", fmt.Errorf("update mcp set: %w", err)
	}
	if record == nil {
		return "", fmt.Errorf("mcp set %q not found", id)
	}
	out, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal mcp set: %w", err)
	}
	return string(out), nil
}

func (s *Server) execMCPSetDelete(ctx context.Context, args map[string]any) (string, error) {
	if s.mcpSetStore == nil {
		return "", fmt.Errorf("mcp set store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := s.mcpSetStore.DeleteMCPSet(ctx, id); err != nil {
		return "", fmt.Errorf("delete mcp set %q: %w", id, err)
	}
	return fmt.Sprintf(`{"status":"deleted","id":%q}`, id), nil
}
