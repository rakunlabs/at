package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
	"github.com/worldline-go/types"
)

// ─── Bot Config CRUD ───

type botConfigRow struct {
	WorkspaceID     string         `db:"workspace_id"`
	ID              string         `db:"id"`
	Platform        string         `db:"platform"`
	Name            string         `db:"name"`
	Token           string         `db:"token"`
	DefaultAgentID  string         `db:"default_agent_id"`
	ChannelAgents   types.RawJSON  `db:"channel_agents"`
	AllowedAgentIDs types.RawJSON  `db:"allowed_agent_ids"`
	CustomCommands  types.RawJSON  `db:"custom_commands"`
	AccessMode      string         `db:"access_mode"`
	PendingApproval bool           `db:"pending_approval"`
	AllowedUsers    types.RawJSON  `db:"allowed_users"`
	PendingUsers    types.RawJSON  `db:"pending_users"`
	Enabled         bool           `db:"enabled"`
	UserContainers  bool           `db:"user_containers"`
	ContainerImage  string         `db:"container_image"`
	ContainerCPU    string         `db:"container_cpu"`
	ContainerMemory string         `db:"container_memory"`
	SpeechToText    string         `db:"speech_to_text"`
	WhisperModel    string         `db:"whisper_model"`
	CreatedAt       time.Time      `db:"created_at"`
	UpdatedAt       time.Time      `db:"updated_at"`
	CreatedBy       sql.NullString `db:"created_by"`
	UpdatedBy       sql.NullString `db:"updated_by"`
}

var botConfigColumns = []any{
	"id", "platform", "name", "token", "default_agent_id",
	"channel_agents", "allowed_agent_ids", "custom_commands",
	"access_mode", "pending_approval", "allowed_users", "pending_users",
	"enabled",
	"user_containers", "container_image", "container_cpu", "container_memory",
	"speech_to_text", "whisper_model",
	"created_at", "updated_at", "created_by", "updated_by", "workspace_id",
}

func scanBotConfigRow(scanner interface {
	Scan(...any) error
}, row *botConfigRow) error {
	return scanner.Scan(
		&row.ID, &row.Platform, &row.Name, &row.Token, &row.DefaultAgentID,
		&row.ChannelAgents, &row.AllowedAgentIDs, &row.CustomCommands,
		&row.AccessMode, &row.PendingApproval, &row.AllowedUsers, &row.PendingUsers,
		&row.Enabled,
		&row.UserContainers, &row.ContainerImage, &row.ContainerCPU, &row.ContainerMemory,
		&row.SpeechToText, &row.WhisperModel,
		&row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy,
		&row.WorkspaceID,
	)
}

func (p *Postgres) ListBotConfigs(ctx context.Context, q *query.Query) (*service.ListResult[service.BotConfig], error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	sql, total, err := p.buildListQuery(ctx, p.tableBotConfigs, q, botConfigColumns...)
	if err != nil {
		return nil, fmt.Errorf("build list bot configs query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list bot configs: %w", err)
	}
	defer rows.Close()

	var items []service.BotConfig
	for rows.Next() {
		var row botConfigRow
		if err := scanBotConfigRow(rows, &row); err != nil {
			return nil, fmt.Errorf("scan bot config row: %w", err)
		}

		if !a.Allows("credentials.manage", service.AccessResource{WorkspaceID: row.WorkspaceID, ID: row.ID}) {
			row.Token = "***"
		}
		rec, err := botConfigRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *rec)
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.BotConfig]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (p *Postgres) GetBotConfig(ctx context.Context, id string) (*service.BotConfig, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	scope, err := p.businessReadScope(ctx, p.tableBotConfigs)
	if err != nil {
		return nil, err
	}
	query, _, err := p.goqu.From(p.tableBotConfigs).
		Select(botConfigColumns...).
		Where(scope, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get bot config query: %w", err)
	}

	var row botConfigRow
	err = scanBotConfigRow(p.db.QueryRowContext(ctx, query), &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get bot config %q: %w", id, err)
	}

	if !a.Allows("credentials.manage", service.AccessResource{WorkspaceID: row.WorkspaceID, ID: row.ID}) {
		row.Token = "***"
	}
	return botConfigRowToRecord(row)
}

func (p *Postgres) CreateBotConfig(ctx context.Context, bot service.BotConfig) (*service.BotConfig, error) {
	w, err := p.beginBusinessWrite(ctx, p.tableBotConfigs, "bots.write", "")
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()
	if bot.WorkspaceID != "" && bot.WorkspaceID != w.actor.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	if !w.actor.Allows("credentials.manage", service.AccessResource{WorkspaceID: w.actor.WorkspaceID}) {
		return nil, service.ErrAccessDenied
	}
	if err = p.botReferences(ctx, w, bot); err != nil {
		return nil, err
	}
	channelAgentsJSON, err := json.Marshal(bot.ChannelAgents)
	if err != nil {
		return nil, fmt.Errorf("marshal channel_agents: %w", err)
	}
	allowedAgentIDsJSON, err := json.Marshal(bot.AllowedAgentIDs)
	if err != nil {
		return nil, fmt.Errorf("marshal allowed_agent_ids: %w", err)
	}
	allowedUsersJSON, err := json.Marshal(bot.AllowedUsers)
	if err != nil {
		return nil, fmt.Errorf("marshal allowed_users: %w", err)
	}
	pendingUsersJSON, err := json.Marshal(bot.PendingUsers)
	if err != nil {
		return nil, fmt.Errorf("marshal pending_users: %w", err)
	}
	customCommandsJSON, err := json.Marshal(bot.CustomCommands)
	if err != nil {
		return nil, fmt.Errorf("marshal custom_commands: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	if bot.AccessMode == "" {
		bot.AccessMode = "open"
	}

	query, _, err := p.goqu.Insert(p.tableBotConfigs).Rows(
		goqu.Record{
			"workspace_id":      w.actor.WorkspaceID,
			"id":                id,
			"platform":          bot.Platform,
			"name":              bot.Name,
			"token":             bot.Token,
			"default_agent_id":  bot.DefaultAgentID,
			"channel_agents":    types.RawJSON(channelAgentsJSON),
			"allowed_agent_ids": types.RawJSON(allowedAgentIDsJSON),
			"custom_commands":   types.RawJSON(customCommandsJSON),
			"access_mode":       bot.AccessMode,
			"pending_approval":  bot.PendingApproval,
			"allowed_users":     types.RawJSON(allowedUsersJSON),
			"pending_users":     types.RawJSON(pendingUsersJSON),
			"enabled":           bot.Enabled,
			"user_containers":   bot.UserContainers,
			"container_image":   bot.ContainerImage,
			"container_cpu":     bot.ContainerCPU,
			"container_memory":  bot.ContainerMemory,
			"speech_to_text":    bot.SpeechToText,
			"whisper_model":     bot.WhisperModel,
			"created_at":        now,
			"updated_at":        now,
			"created_by":        bot.CreatedBy,
			"updated_by":        bot.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert bot config query: %w", err)
	}

	if _, err := w.tx.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create bot config: %w", err)
	}
	if err = w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit bot config: %w", err)
	}

	return p.GetBotConfig(ctx, id)
}

func (p *Postgres) UpdateBotConfig(ctx context.Context, id string, bot service.BotConfig) (*service.BotConfig, error) {
	w, err := p.beginBusinessWrite(ctx, p.tableBotConfigs, "bots.write", id)
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()
	if bot.WorkspaceID != "" && bot.WorkspaceID != w.actor.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	if !w.actor.Allows("credentials.manage", service.AccessResource{WorkspaceID: w.actor.WorkspaceID, ID: id}) {
		return nil, service.ErrAccessDenied
	}
	if err = p.botReferences(ctx, w, bot); err != nil {
		return nil, err
	}
	if bot.Token == "***" {
		var token string
		found, e := w.tx.From(p.tableBotConfigs).Select("token").Where(w.predicate, goqu.C("id").Eq(id)).ScanValContext(ctx, &token)
		if e != nil {
			return nil, fmt.Errorf("preserve bot token: %w", e)
		}
		if !found {
			return nil, nil
		}
		bot.Token = token
	}
	channelAgentsJSON, err := json.Marshal(bot.ChannelAgents)
	if err != nil {
		return nil, fmt.Errorf("marshal channel_agents: %w", err)
	}
	allowedAgentIDsJSON, err := json.Marshal(bot.AllowedAgentIDs)
	if err != nil {
		return nil, fmt.Errorf("marshal allowed_agent_ids: %w", err)
	}
	allowedUsersJSON, err := json.Marshal(bot.AllowedUsers)
	if err != nil {
		return nil, fmt.Errorf("marshal allowed_users: %w", err)
	}
	pendingUsersJSON, err := json.Marshal(bot.PendingUsers)
	if err != nil {
		return nil, fmt.Errorf("marshal pending_users: %w", err)
	}
	customCommandsJSON, err := json.Marshal(bot.CustomCommands)
	if err != nil {
		return nil, fmt.Errorf("marshal custom_commands: %w", err)
	}

	if bot.AccessMode == "" {
		bot.AccessMode = "open"
	}

	now := time.Now().UTC()

	record := goqu.Record{
		"platform":          bot.Platform,
		"name":              bot.Name,
		"token":             bot.Token,
		"default_agent_id":  bot.DefaultAgentID,
		"channel_agents":    types.RawJSON(channelAgentsJSON),
		"allowed_agent_ids": types.RawJSON(allowedAgentIDsJSON),
		"custom_commands":   types.RawJSON(customCommandsJSON),
		"access_mode":       bot.AccessMode,
		"pending_approval":  bot.PendingApproval,
		"allowed_users":     types.RawJSON(allowedUsersJSON),
		"pending_users":     types.RawJSON(pendingUsersJSON),
		"enabled":           bot.Enabled,
		"user_containers":   bot.UserContainers,
		"container_image":   bot.ContainerImage,
		"container_cpu":     bot.ContainerCPU,
		"container_memory":  bot.ContainerMemory,
		"speech_to_text":    bot.SpeechToText,
		"whisper_model":     bot.WhisperModel,
		"updated_at":        now,
		"updated_by":        bot.UpdatedBy,
	}

	query, _, err := p.goqu.Update(p.tableBotConfigs).Set(record).Where(w.predicate, goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update bot config query: %w", err)
	}

	res, err := w.tx.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update bot config %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}
	if err = w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit bot config update: %w", err)
	}

	return p.GetBotConfig(ctx, id)
}

func (p *Postgres) DeleteBotConfig(ctx context.Context, id string) error {
	w, err := p.beginBusinessWrite(ctx, p.tableBotConfigs, "bots.write", id)
	if err != nil {
		return err
	}
	defer w.tx.Rollback()
	query, _, err := p.goqu.Delete(p.tableBotConfigs).
		Where(w.predicate, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete bot config query: %w", err)
	}

	_, err = w.tx.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete bot config %q: %w", id, err)
	}

	return w.tx.Commit()
}

func botConfigRowToRecord(row botConfigRow) (*service.BotConfig, error) {
	channelAgents := make(map[string]string)
	if len(row.ChannelAgents) > 0 {
		if err := json.Unmarshal(row.ChannelAgents, &channelAgents); err != nil {
			return nil, fmt.Errorf("unmarshal channel_agents for %q: %w", row.ID, err)
		}
	}

	var allowedAgentIDs []string
	if len(row.AllowedAgentIDs) > 0 {
		if err := json.Unmarshal(row.AllowedAgentIDs, &allowedAgentIDs); err != nil {
			return nil, fmt.Errorf("unmarshal allowed_agent_ids for %q: %w", row.ID, err)
		}
	}

	var allowedUsers []string
	if len(row.AllowedUsers) > 0 {
		if err := json.Unmarshal(row.AllowedUsers, &allowedUsers); err != nil {
			return nil, fmt.Errorf("unmarshal allowed_users for %q: %w", row.ID, err)
		}
	}

	var pendingUsers []string
	if len(row.PendingUsers) > 0 {
		if err := json.Unmarshal(row.PendingUsers, &pendingUsers); err != nil {
			return nil, fmt.Errorf("unmarshal pending_users for %q: %w", row.ID, err)
		}
	}

	var customCommands []service.BotCustomCommand
	if len(row.CustomCommands) > 0 {
		if err := json.Unmarshal(row.CustomCommands, &customCommands); err != nil {
			return nil, fmt.Errorf("unmarshal custom_commands for %q: %w", row.ID, err)
		}
	}

	accessMode := row.AccessMode
	if accessMode == "" {
		accessMode = "open"
	}

	return &service.BotConfig{
		WorkspaceID:     row.WorkspaceID,
		ID:              row.ID,
		Platform:        row.Platform,
		Name:            row.Name,
		Token:           row.Token,
		DefaultAgentID:  row.DefaultAgentID,
		ChannelAgents:   channelAgents,
		AllowedAgentIDs: allowedAgentIDs,
		CustomCommands:  customCommands,
		AccessMode:      accessMode,
		PendingApproval: row.PendingApproval,
		AllowedUsers:    allowedUsers,
		PendingUsers:    pendingUsers,
		Enabled:         row.Enabled,
		UserContainers:  row.UserContainers,
		ContainerImage:  row.ContainerImage,
		ContainerCPU:    row.ContainerCPU,
		ContainerMemory: row.ContainerMemory,
		SpeechToText:    row.SpeechToText,
		WhisperModel:    row.WhisperModel,
		CreatedAt:       row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       row.UpdatedAt.Format(time.RFC3339),
		CreatedBy:       row.CreatedBy.String,
		UpdatedBy:       row.UpdatedBy.String,
	}, nil
}
