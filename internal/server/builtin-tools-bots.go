package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Bot Config Management Tool Executors ───
//
// These executors expose CRUD on telegram/discord bot configurations through
// the management MCP. The most common use case is letting an LLM (or an
// MCP client like opencode) add/edit a bot's `custom_commands` array
// without round-tripping through curl + the full BotConfig PUT body.
//
// Pattern intentionally mirrors the existing agent_* executors:
//   - bot_list   → fetches via the existing list store call (with redacted token)
//   - bot_get    → returns a single bot config (token redacted)
//   - bot_update → partial update; fetches existing record and only overwrites
//                  the fields the caller provided, then re-puts via the store.
//
// We deliberately do NOT expose bot_create or bot_delete here. Creating a
// new bot requires a Telegram/Discord token from outside the system (and
// has lifecycle implications around polling), and deleting a bot is rare
// + dangerous; both stay UI-only.

// execBotList lists bot configurations. Tokens are redacted in the
// response — never emit raw bot tokens to an LLM/MCP client.
func (s *Server) execBotList(ctx context.Context, args map[string]any) (string, error) {
	if s.botConfigStore == nil {
		return "", fmt.Errorf("bot config store not configured")
	}

	records, err := s.botConfigStore.ListBotConfigs(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("list bot configs: %w", err)
	}
	if records == nil {
		records = &service.ListResult[service.BotConfig]{
			Data: []service.BotConfig{},
		}
	}
	for i := range records.Data {
		redactBotToken(&records.Data[i])
	}
	out, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal bot configs: %w", err)
	}
	return string(out), nil
}

// execBotGet returns a single bot config by id. Token is redacted.
func (s *Server) execBotGet(ctx context.Context, args map[string]any) (string, error) {
	if s.botConfigStore == nil {
		return "", fmt.Errorf("bot config store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	record, err := s.botConfigStore.GetBotConfig(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get bot config %q: %w", id, err)
	}
	if record == nil {
		return "", fmt.Errorf("bot config %q not found", id)
	}
	redactBotToken(record)
	out, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal bot config: %w", err)
	}
	return string(out), nil
}

// execBotUpdate applies a partial update to a bot config. The caller's
// args may include any combination of supported fields; anything not
// provided is left as-is (we fetch the current record first and merge).
//
// This is the tool the user originally hit a wall on: `/asmr` and
// `/silent` had to be set via curl because there was no MCP equivalent
// of "PUT just the custom_commands field". Now an MCP client can do:
//
//	{
//	  "name": "bot_update",
//	  "arguments": {
//	    "id": "01KQ3AGX7TQY275NBFH8A23751",
//	    "custom_commands": [
//	      {"command": "asmr", "organization_id": "01KQ364AKS70NF4KRZF8KPRBST", ...}
//	    ]
//	  }
//	}
func (s *Server) execBotUpdate(ctx context.Context, args map[string]any) (string, error) {
	if s.botConfigStore == nil {
		return "", fmt.Errorf("bot config store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}

	// Fetch the existing record to merge against. UpdateBotConfig at the
	// store layer is a full replacement, so we must build the complete
	// updated record ourselves.
	existing, err := s.botConfigStore.GetBotConfig(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get bot config %q: %w", id, err)
	}
	if existing == nil {
		return "", fmt.Errorf("bot config %q not found", id)
	}

	updated := *existing // value copy; we'll overwrite fields below

	// Top-level scalars.
	if v, ok := args["name"].(string); ok && v != "" {
		updated.Name = strings.TrimSpace(v)
	}
	if v, ok := args["platform"].(string); ok && v != "" {
		updated.Platform = strings.TrimSpace(v)
	}
	if v, ok := args["token"].(string); ok && v != "" {
		// Refuse the redacted placeholder so a round-tripped record from
		// bot_get can't accidentally clobber the real token.
		if isRedactedBotToken(v) {
			return "", fmt.Errorf("token is redacted; pass a real token or omit the field to keep the current one")
		}
		updated.Token = v
	}
	if v, ok := args["default_agent_id"].(string); ok {
		updated.DefaultAgentID = strings.TrimSpace(v)
	}
	if v, ok := args["access_mode"].(string); ok && v != "" {
		updated.AccessMode = strings.TrimSpace(v)
	}
	if v, ok := args["enabled"].(bool); ok {
		updated.Enabled = v
	}
	if v, ok := args["pending_approval"].(bool); ok {
		updated.PendingApproval = v
	}
	if v, ok := args["user_containers"].(bool); ok {
		updated.UserContainers = v
	}
	if v, ok := args["container_image"].(string); ok {
		updated.ContainerImage = v
	}
	if v, ok := args["container_cpu"].(string); ok {
		updated.ContainerCPU = v
	}
	if v, ok := args["container_memory"].(string); ok {
		updated.ContainerMemory = v
	}
	if v, ok := args["speech_to_text"].(string); ok {
		updated.SpeechToText = v
	}
	if v, ok := args["whisper_model"].(string); ok {
		updated.WhisperModel = v
	}

	// Repeated string fields. These are full replacements (not merges)
	// because the caller passing an empty array is a meaningful "clear
	// the list" signal. We only touch the field when the key is
	// present in args.
	if raw, ok := args["allowed_agent_ids"]; ok {
		ids, err := decodeStringArray(raw)
		if err != nil {
			return "", fmt.Errorf("allowed_agent_ids: %w", err)
		}
		updated.AllowedAgentIDs = ids
	}
	if raw, ok := args["allowed_users"]; ok {
		users, err := decodeStringArray(raw)
		if err != nil {
			return "", fmt.Errorf("allowed_users: %w", err)
		}
		updated.AllowedUsers = users
	}
	if raw, ok := args["pending_users"]; ok {
		users, err := decodeStringArray(raw)
		if err != nil {
			return "", fmt.Errorf("pending_users: %w", err)
		}
		updated.PendingUsers = users
	}

	// channel_agents map (chat_id → agent_id). Same convention: present
	// = full replacement.
	if raw, ok := args["channel_agents"]; ok {
		data, _ := json.Marshal(raw)
		var m map[string]string
		if err := json.Unmarshal(data, &m); err != nil {
			return "", fmt.Errorf("channel_agents: must be an object of string→string")
		}
		updated.ChannelAgents = m
	}

	// custom_commands — the headline reason this tool exists. Full
	// replacement; caller passes the complete list. Each entry is
	// validated lightly (command must be non-empty after stripping
	// the leading slash) and the slash is stripped so the bot's
	// case-insensitive match works regardless of whether the LLM
	// included the `/` prefix.
	if raw, ok := args["custom_commands"]; ok {
		data, _ := json.Marshal(raw)
		var cmds []service.BotCustomCommand
		if err := json.Unmarshal(data, &cmds); err != nil {
			return "", fmt.Errorf("custom_commands: must be an array of {command, description, organization_id?, agent_id?, brief?, title_prefix?, max_iterations?}: %w", err)
		}
		clean := make([]service.BotCustomCommand, 0, len(cmds))
		for i := range cmds {
			c := cmds[i]
			c.Command = strings.TrimSpace(strings.TrimPrefix(c.Command, "/"))
			if c.Command == "" {
				continue
			}
			clean = append(clean, c)
		}
		updated.CustomCommands = clean
	}

	// Persist the merged record. The store-level UpdateBotConfig signature
	// matches what the HTTP handler expects, so we go through the same
	// path and pick up its lifecycle handling on the next bot start/stop.
	updated.UpdatedBy = "mcp"
	record, err := s.botConfigStore.UpdateBotConfig(ctx, id, updated)
	if err != nil {
		return "", fmt.Errorf("update bot config %q: %w", id, err)
	}
	if record == nil {
		return "", fmt.Errorf("bot config %q not found after update", id)
	}
	redactBotToken(record)

	out, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal updated bot config: %w", err)
	}
	return string(out), nil
}

// botTokenRedacted is the placeholder we substitute for the real token in
// any bot_list / bot_get / bot_update response. Long enough to be useful
// (you can tell at a glance the field exists) but obviously not a real
// Telegram/Discord token.
const botTokenRedacted = "***redacted***"

// redactBotToken zeroes the secret on a BotConfig before it leaves the
// process. Mutates the receiver.
func redactBotToken(b *service.BotConfig) {
	if b == nil || b.Token == "" {
		return
	}
	b.Token = botTokenRedacted
}

// isRedactedBotToken returns true when v looks like our redaction
// placeholder. Used to refuse round-tripped tokens that would silently
// clobber the real one.
func isRedactedBotToken(v string) bool {
	return v == botTokenRedacted
}

// decodeStringArray converts a generic args value (typically []any from
// JSON-decoded MCP arguments) into []string.
func decodeStringArray(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	var out []string
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("must be an array of strings: %w", err)
	}
	return out, nil
}
