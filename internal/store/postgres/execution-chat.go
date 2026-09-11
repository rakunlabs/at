package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

func (p *Postgres) GetExecutionBotSession(ctx context.Context, platform, userID, channelID, botID string) (*service.ChatSession, error) {
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "bots.use", ResourceID: botID}); err != nil {
		return nil, err
	}
	principal, _, ok := service.ExecutionFromContext(ctx)
	if !ok {
		return nil, service.ErrExecutionDenied
	}
	var row chatSessionRow
	found, err := p.goqu.From(p.tableChatSessions).Select("id", "agent_id", "task_id", "organization_id", "name", "config", "created_at", "updated_at", "created_by", "updated_by").Where(
		goqu.Ex{"workspace_id": principal.WorkspaceID},
		goqu.L("config->>'platform'").Eq(platform), goqu.L("config->>'platform_user_id'").Eq(userID),
		goqu.L("config->>'platform_channel_id'").Eq(channelID), goqu.L("config->>'bot_config_id'").Eq(botID),
	).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("get scoped bot session: %w", err)
	}
	if !found {
		return nil, nil
	}
	return chatSessionRowToRecord(row)
}

func (p *Postgres) CreateExecutionBotSession(ctx context.Context, session service.ChatSession) (*service.ChatSession, error) {
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "bots.use", ResourceID: session.Config.BotConfigID}); err != nil {
		return nil, err
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "agents.run", ResourceID: session.AgentID}); err != nil {
		return nil, err
	}
	principal, _, ok := service.ExecutionFromContext(ctx)
	if !ok {
		return nil, service.ErrExecutionDenied
	}
	// This writer creates only independent bot sessions, never arbitrary linked
	// task/org references obtained from transport input.
	if session.TaskID != "" || session.OrganizationID != "" {
		return nil, service.ErrExecutionDenied
	}
	config, err := json.Marshal(session.Config)
	if err != nil {
		return nil, fmt.Errorf("encode bot session config: %w", err)
	}
	session.ID = ulid.Make().String()
	now := time.Now().UTC()
	session.CreatedBy, session.UpdatedBy = principal.UserID, principal.UserID
	session.CreatedAt, session.UpdatedAt = now.Format(time.RFC3339), now.Format(time.RFC3339)
	_, err = p.goqu.Insert(p.tableChatSessions).Rows(goqu.Record{
		"id": session.ID, "workspace_id": principal.WorkspaceID, "agent_id": session.AgentID, "name": session.Name,
		"config": goqu.L("?::jsonb", string(config)), "created_at": now, "updated_at": now, "created_by": session.CreatedBy, "updated_by": session.UpdatedBy,
	}).Executor().ExecContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("create scoped bot session: %w", err)
	}
	return &session, nil
}
