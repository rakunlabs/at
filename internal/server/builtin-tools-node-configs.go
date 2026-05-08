package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Node Config Tool Executors (Phase 2) ───
//
// Node configs hold reusable per-node-type configuration (currently
// only `email` for SMTP). The `data` field is a JSON-encoded string
// (not a JSON object — the column is `text` in the DB) whose internal
// schema depends on `type`. We mirror the HTTP layer's redaction:
// list responses redact sensitive fields (e.g. email→password) inside
// the data blob; get returns the full payload.

func (s *Server) execNodeConfigList(ctx context.Context, args map[string]any) (string, error) {
	if s.nodeConfigStore == nil {
		return "", fmt.Errorf("node config store not configured")
	}

	var listResult *service.ListResult[service.NodeConfig]
	configType, _ := args["type"].(string)
	if configType != "" {
		records, err := s.nodeConfigStore.ListNodeConfigsByType(ctx, configType)
		if err != nil {
			return "", fmt.Errorf("list node configs by type %q: %w", configType, err)
		}
		listResult = &service.ListResult[service.NodeConfig]{
			Data: records,
			Meta: service.ListMeta{Total: uint64(len(records))},
		}
	} else {
		res, err := s.nodeConfigStore.ListNodeConfigs(ctx, nil)
		if err != nil {
			return "", fmt.Errorf("list node configs: %w", err)
		}
		listResult = res
	}
	if listResult == nil {
		listResult = &service.ListResult[service.NodeConfig]{Data: []service.NodeConfig{}}
	} else if listResult.Data == nil {
		listResult.Data = []service.NodeConfig{}
	}
	for i := range listResult.Data {
		listResult.Data[i].Data = redactNodeConfigData(listResult.Data[i].Type, listResult.Data[i].Data)
	}
	out, err := json.MarshalIndent(listResult, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal node configs: %w", err)
	}
	return string(out), nil
}

func (s *Server) execNodeConfigGet(ctx context.Context, args map[string]any) (string, error) {
	if s.nodeConfigStore == nil {
		return "", fmt.Errorf("node config store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	rec, err := s.nodeConfigStore.GetNodeConfig(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get node config %q: %w", id, err)
	}
	if rec == nil {
		return "", fmt.Errorf("node config %q not found", id)
	}
	out, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal node config: %w", err)
	}
	return string(out), nil
}

func (s *Server) execNodeConfigCreate(ctx context.Context, args map[string]any) (string, error) {
	if s.nodeConfigStore == nil {
		return "", fmt.Errorf("node config store not configured")
	}
	name := stringArg(args, "name")
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	t := stringArg(args, "type")
	if t == "" {
		return "", fmt.Errorf("type is required")
	}
	rec, err := s.nodeConfigStore.CreateNodeConfig(ctx, service.NodeConfig{
		Name:      name,
		Type:      t,
		Data:      stringArg(args, "data"),
		CreatedBy: "mcp",
		UpdatedBy: "mcp",
	})
	if err != nil {
		return "", fmt.Errorf("create node config: %w", err)
	}
	out, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal node config: %w", err)
	}
	return string(out), nil
}

func (s *Server) execNodeConfigUpdate(ctx context.Context, args map[string]any) (string, error) {
	if s.nodeConfigStore == nil {
		return "", fmt.Errorf("node config store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	name := stringArg(args, "name")
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	t := stringArg(args, "type")
	if t == "" {
		return "", fmt.Errorf("type is required")
	}
	rec, err := s.nodeConfigStore.UpdateNodeConfig(ctx, id, service.NodeConfig{
		Name:      name,
		Type:      t,
		Data:      stringArg(args, "data"),
		UpdatedBy: "mcp",
	})
	if err != nil {
		return "", fmt.Errorf("update node config %q: %w", id, err)
	}
	if rec == nil {
		return "", fmt.Errorf("node config %q not found", id)
	}
	out, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal node config: %w", err)
	}
	return string(out), nil
}

func (s *Server) execNodeConfigDelete(ctx context.Context, args map[string]any) (string, error) {
	if s.nodeConfigStore == nil {
		return "", fmt.Errorf("node config store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := s.nodeConfigStore.DeleteNodeConfig(ctx, id); err != nil {
		return "", fmt.Errorf("delete node config %q: %w", id, err)
	}
	return fmt.Sprintf(`{"status":"deleted","id":%q}`, id), nil
}
