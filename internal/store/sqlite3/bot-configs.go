package sqlite3

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
)

// ─── Bot Config CRUD ───

type botConfigRow struct {
	ID              string         `db:"id"`
	Platform        string         `db:"platform"`
	Name            string         `db:"name"`
	Token           string         `db:"token"`
	DefaultAgentID  string         `db:"default_agent_id"`
	ChannelAgents   sql.NullString `db:"channel_agents"`
	AllowedAgentIDs sql.NullString `db:"allowed_agent_ids"`
	CustomCommands  sql.NullString `db:"custom_commands"`
	AccessMode      string         `db:"access_mode"`
	PendingApproval bool           `db:"pending_approval"`
	AllowedUsers    sql.NullString `db:"allowed_users"`
	PendingUsers    sql.NullString `db:"pending_users"`
	Enabled         bool           `db:"enabled"`
	UserContainers  bool           `db:"user_containers"`
	ContainerImage  string         `db:"container_image"`
	ContainerCPU    string         `db:"container_cpu"`
	ContainerMemory string         `db:"container_memory"`
	SpeechToText    string         `db:"speech_to_text"`
	WhisperModel    string         `db:"whisper_model"`
	CreatedAt       string         `db:"created_at"`
	UpdatedAt       string         `db:"updated_at"`
	CreatedBy       sql.NullString `db:"created_by"`
	UpdatedBy       sql.NullString `db:"updated_by"`
}

var sqliteBotConfigColumns = []any{
	"id", "platform", "name", "token", "default_agent_id",
	"channel_agents", "allowed_agent_ids", "custom_commands",
	"access_mode", "pending_approval", "allowed_users", "pending_users",
	"enabled",
	"user_containers", "container_image", "container_cpu", "container_memory",
	"speech_to_text", "whisper_model",
	"created_at", "updated_at", "created_by", "updated_by",
}

func scanSQLiteBotConfigRow(scanner interface {
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
	)
}

func (s *SQLite) ListBotConfigs(ctx context.Context, q *query.Query) (*service.ListResult[service.BotConfig], error) {
	sqlStr, total, err := s.buildListQuery(ctx, s.tableBotConfigs, q, sqliteBotConfigColumns...)
	if err != nil {
		return nil, fmt.Errorf("build list bot configs query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, sqlStr)
	if err != nil {
		return nil, fmt.Errorf("list bot configs: %w", err)
	}
	defer rows.Close()

	var items []service.BotConfig
	for rows.Next() {
		var row botConfigRow
		if err := scanSQLiteBotConfigRow(rows, &row); err != nil {
			return nil, fmt.Errorf("scan bot config row: %w", err)
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

func (s *SQLite) GetBotConfig(ctx context.Context, id string) (*service.BotConfig, error) {
	queryStr, _, err := s.goqu.From(s.tableBotConfigs).
		Select(sqliteBotConfigColumns...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get bot config query: %w", err)
	}

	var row botConfigRow
	err = scanSQLiteBotConfigRow(s.db.QueryRowContext(ctx, queryStr), &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get bot config %q: %w", id, err)
	}

	return botConfigRowToRecord(row)
}

func (s *SQLite) CreateBotConfig(ctx context.Context, bot service.BotConfig) (*service.BotConfig, error) {
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

	queryStr, _, err := s.goqu.Insert(s.tableBotConfigs).Rows(
		goqu.Record{
			"id":                id,
			"platform":          bot.Platform,
			"name":              bot.Name,
			"token":             bot.Token,
			"default_agent_id":  bot.DefaultAgentID,
			"channel_agents":    string(channelAgentsJSON),
			"allowed_agent_ids": string(allowedAgentIDsJSON),
			"custom_commands":   string(customCommandsJSON),
			"access_mode":       bot.AccessMode,
			"pending_approval":  bot.PendingApproval,
			"allowed_users":     string(allowedUsersJSON),
			"pending_users":     string(pendingUsersJSON),
			"enabled":           bot.Enabled,
			"user_containers":   bot.UserContainers,
			"container_image":   bot.ContainerImage,
			"container_cpu":     bot.ContainerCPU,
			"container_memory":  bot.ContainerMemory,
			"speech_to_text":    bot.SpeechToText,
			"whisper_model":     bot.WhisperModel,
			"created_at":        now.Format(time.RFC3339),
			"updated_at":        now.Format(time.RFC3339),
			"created_by":        bot.CreatedBy,
			"updated_by":        bot.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert bot config query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, queryStr); err != nil {
		return nil, fmt.Errorf("create bot config: %w", err)
	}

	return s.GetBotConfig(ctx, id)
}

func (s *SQLite) UpdateBotConfig(ctx context.Context, id string, bot service.BotConfig) (*service.BotConfig, error) {
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
		"channel_agents":    string(channelAgentsJSON),
		"allowed_agent_ids": string(allowedAgentIDsJSON),
		"custom_commands":   string(customCommandsJSON),
		"access_mode":       bot.AccessMode,
		"pending_approval":  bot.PendingApproval,
		"allowed_users":     string(allowedUsersJSON),
		"pending_users":     string(pendingUsersJSON),
		"enabled":           bot.Enabled,
		"user_containers":   bot.UserContainers,
		"container_image":   bot.ContainerImage,
		"container_cpu":     bot.ContainerCPU,
		"container_memory":  bot.ContainerMemory,
		"speech_to_text":    bot.SpeechToText,
		"whisper_model":     bot.WhisperModel,
		"updated_at":        now.Format(time.RFC3339),
		"updated_by":        bot.UpdatedBy,
	}

	queryStr, _, err := s.goqu.Update(s.tableBotConfigs).Set(record).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update bot config query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, queryStr)
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

	return s.GetBotConfig(ctx, id)
}

func (s *SQLite) DeleteBotConfig(ctx context.Context, id string) error {
	queryStr, _, err := s.goqu.Delete(s.tableBotConfigs).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete bot config query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, queryStr)
	if err != nil {
		return fmt.Errorf("delete bot config %q: %w", id, err)
	}

	return nil
}

func botConfigRowToRecord(row botConfigRow) (*service.BotConfig, error) {
	channelAgents := make(map[string]string)
	if row.ChannelAgents.Valid && row.ChannelAgents.String != "" {
		if err := json.Unmarshal([]byte(row.ChannelAgents.String), &channelAgents); err != nil {
			return nil, fmt.Errorf("unmarshal channel_agents for %q: %w", row.ID, err)
		}
	}

	var allowedAgentIDs []string
	if row.AllowedAgentIDs.Valid && row.AllowedAgentIDs.String != "" {
		if err := json.Unmarshal([]byte(row.AllowedAgentIDs.String), &allowedAgentIDs); err != nil {
			return nil, fmt.Errorf("unmarshal allowed_agent_ids for %q: %w", row.ID, err)
		}
	}

	var allowedUsers []string
	if row.AllowedUsers.Valid && row.AllowedUsers.String != "" {
		if err := json.Unmarshal([]byte(row.AllowedUsers.String), &allowedUsers); err != nil {
			return nil, fmt.Errorf("unmarshal allowed_users for %q: %w", row.ID, err)
		}
	}

	var pendingUsers []string
	if row.PendingUsers.Valid && row.PendingUsers.String != "" {
		if err := json.Unmarshal([]byte(row.PendingUsers.String), &pendingUsers); err != nil {
			return nil, fmt.Errorf("unmarshal pending_users for %q: %w", row.ID, err)
		}
	}

	var customCommands []service.BotCustomCommand
	if row.CustomCommands.Valid && row.CustomCommands.String != "" {
		if err := json.Unmarshal([]byte(row.CustomCommands.String), &customCommands); err != nil {
			return nil, fmt.Errorf("unmarshal custom_commands for %q: %w", row.ID, err)
		}
	}

	accessMode := row.AccessMode
	if accessMode == "" {
		accessMode = "open"
	}

	return &service.BotConfig{
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
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
		CreatedBy:       row.CreatedBy.String,
		UpdatedBy:       row.UpdatedBy.String,
	}, nil
}
