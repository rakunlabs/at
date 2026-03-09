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
	ID              string         `db:"id"`
	Platform        string         `db:"platform"`
	Name            string         `db:"name"`
	Token           string         `db:"token"`
	DefaultAgentID  string         `db:"default_agent_id"`
	ChannelAgents   types.RawJSON  `db:"channel_agents"`
	AccessMode      string         `db:"access_mode"`
	PendingApproval bool           `db:"pending_approval"`
	AllowedUsers    types.RawJSON  `db:"allowed_users"`
	PendingUsers    types.RawJSON  `db:"pending_users"`
	Enabled         bool           `db:"enabled"`
	CreatedAt       time.Time      `db:"created_at"`
	UpdatedAt       time.Time      `db:"updated_at"`
	CreatedBy       sql.NullString `db:"created_by"`
	UpdatedBy       sql.NullString `db:"updated_by"`
}

func (p *Postgres) ListBotConfigs(ctx context.Context, q *query.Query) (*service.ListResult[service.BotConfig], error) {
	sql, total, err := p.buildListQuery(ctx, p.tableBotConfigs, q, "id", "platform", "name", "token", "default_agent_id", "channel_agents", "access_mode", "pending_approval", "allowed_users", "pending_users", "enabled", "created_at", "updated_at", "created_by", "updated_by")
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
		if err := rows.Scan(&row.ID, &row.Platform, &row.Name, &row.Token, &row.DefaultAgentID, &row.ChannelAgents, &row.AccessMode, &row.PendingApproval, &row.AllowedUsers, &row.PendingUsers, &row.Enabled, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy); err != nil {
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

func (p *Postgres) GetBotConfig(ctx context.Context, id string) (*service.BotConfig, error) {
	query, _, err := p.goqu.From(p.tableBotConfigs).
		Select("id", "platform", "name", "token", "default_agent_id", "channel_agents", "access_mode", "pending_approval", "allowed_users", "pending_users", "enabled", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get bot config query: %w", err)
	}

	var row botConfigRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Platform, &row.Name, &row.Token, &row.DefaultAgentID, &row.ChannelAgents, &row.AccessMode, &row.PendingApproval, &row.AllowedUsers, &row.PendingUsers, &row.Enabled, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get bot config %q: %w", id, err)
	}

	return botConfigRowToRecord(row)
}

func (p *Postgres) CreateBotConfig(ctx context.Context, bot service.BotConfig) (*service.BotConfig, error) {
	channelAgentsJSON, err := json.Marshal(bot.ChannelAgents)
	if err != nil {
		return nil, fmt.Errorf("marshal channel_agents: %w", err)
	}
	allowedUsersJSON, err := json.Marshal(bot.AllowedUsers)
	if err != nil {
		return nil, fmt.Errorf("marshal allowed_users: %w", err)
	}
	pendingUsersJSON, err := json.Marshal(bot.PendingUsers)
	if err != nil {
		return nil, fmt.Errorf("marshal pending_users: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	if bot.AccessMode == "" {
		bot.AccessMode = "open"
	}

	query, _, err := p.goqu.Insert(p.tableBotConfigs).Rows(
		goqu.Record{
			"id":               id,
			"platform":         bot.Platform,
			"name":             bot.Name,
			"token":            bot.Token,
			"default_agent_id": bot.DefaultAgentID,
			"channel_agents":   types.RawJSON(channelAgentsJSON),
			"access_mode":      bot.AccessMode,
			"pending_approval": bot.PendingApproval,
			"allowed_users":    types.RawJSON(allowedUsersJSON),
			"pending_users":    types.RawJSON(pendingUsersJSON),
			"enabled":          bot.Enabled,
			"created_at":       now,
			"updated_at":       now,
			"created_by":       bot.CreatedBy,
			"updated_by":       bot.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert bot config query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create bot config: %w", err)
	}

	if bot.ChannelAgents == nil {
		bot.ChannelAgents = map[string]string{}
	}

	return &service.BotConfig{
		ID:              id,
		Platform:        bot.Platform,
		Name:            bot.Name,
		Token:           bot.Token,
		DefaultAgentID:  bot.DefaultAgentID,
		ChannelAgents:   bot.ChannelAgents,
		AccessMode:      bot.AccessMode,
		PendingApproval: bot.PendingApproval,
		AllowedUsers:    bot.AllowedUsers,
		PendingUsers:    bot.PendingUsers,
		Enabled:         bot.Enabled,
		CreatedAt:       now.Format(time.RFC3339),
		UpdatedAt:       now.Format(time.RFC3339),
		CreatedBy:       bot.CreatedBy,
		UpdatedBy:       bot.UpdatedBy,
	}, nil
}

func (p *Postgres) UpdateBotConfig(ctx context.Context, id string, bot service.BotConfig) (*service.BotConfig, error) {
	channelAgentsJSON, err := json.Marshal(bot.ChannelAgents)
	if err != nil {
		return nil, fmt.Errorf("marshal channel_agents: %w", err)
	}
	allowedUsersJSON, err := json.Marshal(bot.AllowedUsers)
	if err != nil {
		return nil, fmt.Errorf("marshal allowed_users: %w", err)
	}
	pendingUsersJSON, err := json.Marshal(bot.PendingUsers)
	if err != nil {
		return nil, fmt.Errorf("marshal pending_users: %w", err)
	}

	if bot.AccessMode == "" {
		bot.AccessMode = "open"
	}

	now := time.Now().UTC()

	record := goqu.Record{
		"platform":         bot.Platform,
		"name":             bot.Name,
		"token":            bot.Token,
		"default_agent_id": bot.DefaultAgentID,
		"channel_agents":   types.RawJSON(channelAgentsJSON),
		"access_mode":      bot.AccessMode,
		"pending_approval": bot.PendingApproval,
		"allowed_users":    types.RawJSON(allowedUsersJSON),
		"pending_users":    types.RawJSON(pendingUsersJSON),
		"enabled":          bot.Enabled,
		"updated_at":       now,
		"updated_by":       bot.UpdatedBy,
	}

	query, _, err := p.goqu.Update(p.tableBotConfigs).Set(record).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update bot config query: %w", err)
	}

	res, err := p.db.ExecContext(ctx, query)
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

	return p.GetBotConfig(ctx, id)
}

func (p *Postgres) DeleteBotConfig(ctx context.Context, id string) error {
	query, _, err := p.goqu.Delete(p.tableBotConfigs).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete bot config query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete bot config %q: %w", id, err)
	}

	return nil
}

func botConfigRowToRecord(row botConfigRow) (*service.BotConfig, error) {
	channelAgents := make(map[string]string)
	if len(row.ChannelAgents) > 0 {
		if err := json.Unmarshal(row.ChannelAgents, &channelAgents); err != nil {
			return nil, fmt.Errorf("unmarshal channel_agents for %q: %w", row.ID, err)
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
		AccessMode:      accessMode,
		PendingApproval: row.PendingApproval,
		AllowedUsers:    allowedUsers,
		PendingUsers:    pendingUsers,
		Enabled:         row.Enabled,
		CreatedAt:       row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       row.UpdatedAt.Format(time.RFC3339),
		CreatedBy:       row.CreatedBy.String,
		UpdatedBy:       row.UpdatedBy.String,
	}, nil
}
