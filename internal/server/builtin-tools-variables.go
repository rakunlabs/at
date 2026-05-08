package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Variable Management Tool Executors (Phase 2) ───
//
// Variables are the per-instance key-value store backing skill bash
// handlers (`$VAR_<KEY>`) and JS handlers (`getVar()`). Secrets are
// AES-256-GCM encrypted at rest by the store layer and redacted in
// list responses by the server layer; the redaction policy mirrors
// the HTTP handler in secrets.go (List redacts → "***", Get returns
// full value, Create/Update return the record as stored).
//
// One MCP-specific behaviour: variable_create is an upsert by key,
// just like the HTTP handler. The reasoning is that an LLM driving a
// skill installation flow often re-runs the same "set up variables"
// step; if the second run errored on collision, every install would
// need a "did this already exist?" probe. Mirroring the HTTP behaviour
// keeps the contract consistent.

// execVariableList returns all variables with secrets redacted.
func (s *Server) execVariableList(ctx context.Context, _ map[string]any) (string, error) {
	if s.variableStore == nil {
		return "", fmt.Errorf("variable store not configured")
	}
	records, err := s.variableStore.ListVariables(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("list variables: %w", err)
	}
	if records == nil {
		records = &service.ListResult[service.Variable]{Data: []service.Variable{}}
	}
	for i := range records.Data {
		if records.Data[i].Secret {
			records.Data[i].Value = "***"
		}
	}
	out, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal variables: %w", err)
	}
	return string(out), nil
}

// execVariableGet returns the full unredacted variable. Mirrors
// GetVariableAPI: even secrets come back in plaintext because the
// caller has explicitly asked for this specific record by ID.
func (s *Server) execVariableGet(ctx context.Context, args map[string]any) (string, error) {
	if s.variableStore == nil {
		return "", fmt.Errorf("variable store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	record, err := s.variableStore.GetVariable(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get variable %q: %w", id, err)
	}
	if record == nil {
		return "", fmt.Errorf("variable %q not found", id)
	}
	out, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal variable: %w", err)
	}
	return string(out), nil
}

// execVariableCreate creates or upserts a variable by key. Mirrors
// CreateVariableAPI's upsert behaviour to keep idempotent install
// flows from blowing up on the second run.
func (s *Server) execVariableCreate(ctx context.Context, args map[string]any) (string, error) {
	if s.variableStore == nil {
		return "", fmt.Errorf("variable store not configured")
	}
	key, _ := args["key"].(string)
	if key == "" {
		return "", fmt.Errorf("key is required")
	}
	value, _ := args["value"].(string)
	if value == "" {
		return "", fmt.Errorf("value is required")
	}
	secret, _ := args["secret"].(bool)
	description := stringArg(args, "description")

	v := service.Variable{
		Key:         key,
		Value:       value,
		Description: description,
		Secret:      secret,
		CreatedBy:   "mcp",
		UpdatedBy:   "mcp",
	}

	// Upsert by key. We intentionally don't surface the GetVariableByKey
	// error: if the lookup fails for a transient reason, fall through to
	// CreateVariable which will surface the same DB error path.
	existing, _ := s.variableStore.GetVariableByKey(ctx, key)
	if existing != nil {
		existing.Value = value
		if description != "" {
			existing.Description = description
		}
		existing.Secret = secret
		existing.UpdatedBy = "mcp"
		record, err := s.variableStore.UpdateVariable(ctx, existing.ID, *existing)
		if err != nil {
			return "", fmt.Errorf("update variable %q: %w", key, err)
		}
		out, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal variable: %w", err)
		}
		return string(out), nil
	}

	record, err := s.variableStore.CreateVariable(ctx, v)
	if err != nil {
		return "", fmt.Errorf("create variable %q: %w", key, err)
	}
	out, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal variable: %w", err)
	}
	return string(out), nil
}

// execVariableUpdate replaces a variable by ID.
func (s *Server) execVariableUpdate(ctx context.Context, args map[string]any) (string, error) {
	if s.variableStore == nil {
		return "", fmt.Errorf("variable store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	key, _ := args["key"].(string)
	if key == "" {
		return "", fmt.Errorf("key is required")
	}
	secret, _ := args["secret"].(bool)
	v := service.Variable{
		Key:         key,
		Value:       stringArg(args, "value"),
		Description: stringArg(args, "description"),
		Secret:      secret,
		UpdatedBy:   "mcp",
	}
	record, err := s.variableStore.UpdateVariable(ctx, id, v)
	if err != nil {
		return "", fmt.Errorf("update variable %q: %w", id, err)
	}
	if record == nil {
		return "", fmt.Errorf("variable %q not found", id)
	}
	out, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal variable: %w", err)
	}
	return string(out), nil
}

func (s *Server) execVariableDelete(ctx context.Context, args map[string]any) (string, error) {
	if s.variableStore == nil {
		return "", fmt.Errorf("variable store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := s.variableStore.DeleteVariable(ctx, id); err != nil {
		return "", fmt.Errorf("delete variable %q: %w", id, err)
	}
	return fmt.Sprintf(`{"status":"deleted","id":%q}`, id), nil
}
