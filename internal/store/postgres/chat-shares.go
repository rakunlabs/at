package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.ChatShareStorer = (*Postgres)(nil)

type chatShareRow struct {
	ID                   string       `db:"id"`
	WorkspaceID          string       `db:"workspace_id"`
	SourceConversationID string       `db:"source_conversation_id"`
	SourceOwnerUserID    string       `db:"source_owner_user_id"`
	ThroughSequence      int64        `db:"through_sequence"`
	CurrentVersion       int64        `db:"current_version"`
	RevokedAt            sql.NullTime `db:"revoked_at"`
	CreatedAt            time.Time    `db:"created_at"`
	UpdatedAt            time.Time    `db:"updated_at"`
	Payload              string       `db:"payload"`
	Options              string       `db:"options"`
}

func mediaObjectSelect(alias string) []any {
	columns := make([]any, 0, len(mediaObjectColumns))
	for _, column := range mediaObjectColumns {
		name := column.(string)
		columns = append(columns, goqu.I(alias+"."+name).As(name))
	}
	return columns
}

func (p *Postgres) ReadPlaygroundPrefix(ctx context.Context, owner, id string, through int64) (*service.PlaygroundConversation, []service.PlaygroundMessage, error) {
	if through < 1 {
		return nil, nil, service.ErrChatShareConflict
	}
	var conversation *service.PlaygroundConversation
	messages := []service.PlaygroundMessage{}
	err := p.playgroundLocked(ctx, owner, id, func(tx *goqu.TxDatabase, row *playgroundConversationRow) error {
		converted, err := playgroundConversationRowToRecord(*row)
		if err != nil {
			return err
		}
		conversation = &converted
		var rows []playgroundMessageRow
		if err := tx.From(p.tablePlaygroundMessages).Select(playgroundMessageColumns...).
			Where(goqu.Ex{"conversation_id": id}, goqu.I("sequence").Lte(through)).
			Order(goqu.I("sequence").Asc()).Limit(service.ChatShareMaxMessages+1).
			ScanStructsContext(ctx, &rows); err != nil {
			return fmt.Errorf("read chat share prefix: %w", err)
		}
		if len(rows) > service.ChatShareMaxMessages {
			return service.ErrChatShareTooLarge
		}
		if len(rows) == 0 || rows[len(rows)-1].Sequence != through {
			return service.ErrChatShareConflict
		}
		for _, messageRow := range rows {
			message, err := playgroundMessageRowToRecord(messageRow)
			if err != nil {
				return err
			}
			messages = append(messages, message)
		}
		return nil
	})
	return conversation, messages, err
}

func (p *Postgres) CreateChatShare(ctx context.Context, share service.ChatShare, mediaIDs []string) (*service.ChatShare, error) {
	if share.ID == "" {
		share.ID = ulid.Make().String()
	}
	return p.writeChatShare(ctx, share, mediaIDs, false)
}

func (p *Postgres) UpdateChatShare(ctx context.Context, share service.ChatShare, mediaIDs []string) (*service.ChatShare, error) {
	return p.writeChatShare(ctx, share, mediaIDs, true)
}

func (p *Postgres) writeChatShare(ctx context.Context, share service.ChatShare, mediaIDs []string, update bool) (*service.ChatShare, error) {
	payload, err := json.Marshal(share.Payload)
	if err != nil {
		return nil, fmt.Errorf("marshal chat share payload: %w", err)
	}
	options, err := json.Marshal(share.Options)
	if err != nil {
		return nil, fmt.Errorf("marshal chat share options: %w", err)
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin chat share write: %w", err)
	}
	defer tx.Rollback()
	actor, err := p.workspaceActor(ctx, tx, "models.use")
	if err != nil {
		return nil, err
	}
	if actor.WorkspaceID != share.WorkspaceID || actor.UserID != share.SourceOwnerUserID {
		return nil, service.ErrAccessDenied
	}
	if update {
		var current chatShareRow
		found, err := tx.From(p.workspaceTable("chat_shares")).Select("id", "workspace_id", "source_conversation_id", "source_owner_user_id", "current_version").
			Where(goqu.Ex{"id": share.ID, "source_owner_user_id": share.SourceOwnerUserID, "revoked_at": nil}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &current)
		if err != nil {
			return nil, fmt.Errorf("lock chat share: %w", err)
		}
		if !found || current.SourceConversationID != share.SourceConversationID || current.WorkspaceID != share.WorkspaceID {
			return nil, service.ErrChatShareNotFound
		}
		share.CurrentVersion = current.CurrentVersion + 1
		if _, err = tx.Update(p.workspaceTable("chat_shares")).Set(goqu.Record{
			"through_sequence": share.ThroughSequence,
			"current_version":  share.CurrentVersion,
			"updated_at":       goqu.L("clock_timestamp()"),
		}).Where(goqu.Ex{"id": share.ID}).Executor().ExecContext(ctx); err != nil {
			return nil, fmt.Errorf("update chat share: %w", err)
		}
	} else {
		share.CurrentVersion = 1
		if _, err = tx.Insert(p.workspaceTable("chat_shares")).Rows(goqu.Record{
			"id":                     share.ID,
			"workspace_id":           share.WorkspaceID,
			"source_conversation_id": share.SourceConversationID,
			"source_owner_user_id":   share.SourceOwnerUserID,
			"through_sequence":       share.ThroughSequence,
			"current_version":        share.CurrentVersion,
		}).Executor().ExecContext(ctx); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return nil, service.ErrChatShareConflict
			}
			return nil, fmt.Errorf("create chat share: %w", err)
		}
	}
	if _, err = tx.Insert(p.workspaceTable("chat_share_versions")).Rows(goqu.Record{
		"share_id": share.ID,
		"version":  share.CurrentVersion,
		"payload":  string(payload),
		"options":  string(options),
	}).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("create chat share version: %w", err)
	}
	for _, mediaID := range mediaIDs {
		if _, err := tx.Insert(p.workspaceTable("chat_share_media")).Rows(goqu.Record{
			"share_id": share.ID, "version": share.CurrentVersion, "media_object_id": mediaID,
		}).Executor().ExecContext(ctx); err != nil {
			return nil, fmt.Errorf("attach chat share media: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit chat share: %w", err)
	}
	return p.GetChatShare(ctx, share.ID)
}

func (p *Postgres) GetChatShare(ctx context.Context, id string) (*service.ChatShare, error) {
	return p.getChatShareWhere(ctx, goqu.Ex{"s.id": id})
}

func (p *Postgres) GetChatShareBySource(ctx context.Context, owner, conversationID, workspaceID string) (*service.ChatShare, error) {
	return p.getChatShareWhere(ctx, goqu.Ex{"s.source_owner_user_id": owner, "s.source_conversation_id": conversationID, "s.workspace_id": workspaceID})
}

func (p *Postgres) getChatShareWhere(ctx context.Context, where goqu.Ex) (*service.ChatShare, error) {
	var row chatShareRow
	found, err := p.goqu.From(p.workspaceTable("chat_shares").As("s")).
		Join(p.workspaceTable("chat_share_versions").As("v"), goqu.On(
			goqu.I("v.share_id").Eq(goqu.I("s.id")),
			goqu.I("v.version").Eq(goqu.I("s.current_version")),
		)).Select(
		"s.id", "s.workspace_id", goqu.L("COALESCE(s.source_conversation_id, '')").As("source_conversation_id"),
		"s.source_owner_user_id", "s.through_sequence", "s.current_version", "s.revoked_at", "s.created_at", "s.updated_at",
		goqu.I("v.payload").As("payload"), goqu.I("v.options").As("options"),
	).Where(where).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("get chat share: %w", err)
	}
	if !found {
		return nil, service.ErrChatShareNotFound
	}
	return chatShareRowToService(row)
}

func (p *Postgres) RevokeChatShare(ctx context.Context, id, owner string) ([]service.MediaObject, error) {
	return p.revokeChatShares(ctx, goqu.Ex{"s.id": id, "s.source_owner_user_id": owner, "s.revoked_at": nil}, true)
}

func (p *Postgres) RevokeChatSharesForSource(ctx context.Context, owner, sourceID string) ([]service.MediaObject, error) {
	return p.revokeChatShares(ctx, goqu.Ex{"s.source_conversation_id": sourceID, "s.source_owner_user_id": owner, "s.revoked_at": nil}, false)
}

func (p *Postgres) revokeChatShares(ctx context.Context, where goqu.Ex, requireMatch bool) ([]service.MediaObject, error) {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin chat share revoke: %w", err)
	}
	defer tx.Rollback()
	var shareIDs []string
	err = tx.From(p.workspaceTable("chat_shares").As("s")).Select("s.id").Where(where).ForUpdate(goqu.Wait).ScanValsContext(ctx, &shareIDs)
	if err != nil {
		return nil, fmt.Errorf("lock chat share for revoke: %w", err)
	}
	if requireMatch && len(shareIDs) == 0 {
		return nil, service.ErrChatShareNotFound
	}
	if len(shareIDs) == 0 {
		return []service.MediaObject{}, nil
	}
	var mediaRows []mediaObjectRow
	if err := tx.From(p.tableMediaObjects.As("m")).Join(p.workspaceTable("chat_share_media").As("sm"), goqu.On(goqu.I("sm.media_object_id").Eq(goqu.I("m.id")))).
		Select(mediaObjectSelect("m")...).Where(goqu.I("sm.share_id").In(shareIDs)).ScanStructsContext(ctx, &mediaRows); err != nil {
		return nil, fmt.Errorf("list chat share media: %w", err)
	}
	if _, err := tx.Update(p.workspaceTable("chat_shares")).Set(goqu.Record{
		"source_conversation_id": nil,
		"revoked_at":             goqu.L("clock_timestamp()"),
		"updated_at":             goqu.L("clock_timestamp()"),
	}).Where(goqu.I("id").In(shareIDs)).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("revoke chat share: %w", err)
	}
	if _, err := tx.Delete(p.workspaceTable("chat_share_media")).Where(goqu.I("share_id").In(shareIDs)).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("delete chat share media: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit chat share revoke: %w", err)
	}
	objects := make([]service.MediaObject, 0, len(mediaRows))
	for _, row := range mediaRows {
		objects = append(objects, mediaObjectRowToRecord(row))
	}
	return objects, nil
}

func (p *Postgres) ImportChatShare(ctx context.Context, id string, version int64, owner, title, providerKey, model string, media map[string]string) (*service.PlaygroundConversation, error) {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin chat share import: %w", err)
	}
	defer tx.Rollback()
	actor, err := p.workspaceActor(ctx, tx, "models.use")
	if err != nil {
		return nil, err
	}
	if actor.UserID != owner {
		return nil, service.ErrAccessDenied
	}
	var row chatShareRow
	found, err := tx.From(p.workspaceTable("chat_shares").As("s")).Join(p.workspaceTable("chat_share_versions").As("v"), goqu.On(
		goqu.I("v.share_id").Eq(goqu.I("s.id")), goqu.I("v.version").Eq(goqu.I("s.current_version")),
	)).Select("s.id", "s.workspace_id", "s.current_version", "s.revoked_at", goqu.I("v.payload").As("payload"), goqu.I("v.options").As("options")).
		Where(goqu.Ex{"s.id": id, "s.current_version": version, "s.revoked_at": nil}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("lock chat share import: %w", err)
	}
	if !found {
		return nil, service.ErrChatShareConflict
	}
	if row.WorkspaceID != actor.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	share, err := chatShareRowToService(row)
	if err != nil {
		return nil, err
	}
	if title == "" {
		title = playgroundForkTitle("", share.Payload.Title)
	}
	var conversationRow playgroundConversationRow
	if _, err := tx.Insert(p.tablePlaygroundChats).Rows(goqu.Record{
		"id":                          ulid.Make().String(),
		"owner_user_id":               owner,
		"title":                       title,
		"system_prompt":               share.Payload.SystemPrompt,
		"provider_key":                providerKey,
		"model":                       model,
		"config":                      "{}",
		"imported_from_share_id":      id,
		"imported_from_share_version": version,
		"created_at":                  goqu.L("clock_timestamp()"),
		"updated_at":                  goqu.L("clock_timestamp()"),
	}).Returning(playgroundConversationColumns...).Executor().ScanStructContext(ctx, &conversationRow); err != nil {
		return nil, fmt.Errorf("create imported chat: %w", err)
	}
	for i, message := range share.Payload.Messages {
		data, ok := replaceChatShareMediaIDs(message.Data, media).(map[string]any)
		if !ok {
			return nil, service.ErrChatShareConflict
		}
		encoded, err := playgroundEncode(data)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Insert(p.tablePlaygroundMessages).Rows(goqu.Record{
			"id": ulid.Make().String(), "conversation_id": conversationRow.ID, "sequence": i + 1,
			"role": message.Role, "provider_key": "", "model": "", "data": encoded, "created_at": conversationRow.CreatedAt,
		}).Executor().ExecContext(ctx); err != nil {
			return nil, fmt.Errorf("copy imported chat messages: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit chat share import: %w", err)
	}
	conversation, err := playgroundConversationRowToRecord(conversationRow)
	return &conversation, err
}

func replaceChatShareMediaIDs(value any, replacements map[string]string) any {
	switch current := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(current))
		for key, child := range current {
			if key == "media_id" {
				if id, ok := child.(string); ok {
					if replacement := replacements[id]; replacement != "" {
						out[key] = replacement
						continue
					}
				}
			}
			out[key] = replaceChatShareMediaIDs(child, replacements)
		}
		return out
	case []any:
		out := make([]any, len(current))
		for i, child := range current {
			out[i] = replaceChatShareMediaIDs(child, replacements)
		}
		return out
	default:
		return current
	}
}

func chatShareRowToService(row chatShareRow) (*service.ChatShare, error) {
	share := &service.ChatShare{
		ID: row.ID, WorkspaceID: row.WorkspaceID, SourceConversationID: row.SourceConversationID,
		SourceOwnerUserID: row.SourceOwnerUserID, ThroughSequence: row.ThroughSequence, CurrentVersion: row.CurrentVersion,
		CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if row.RevokedAt.Valid {
		share.RevokedAt = row.RevokedAt.Time.UTC().Format(time.RFC3339)
	}
	if err := json.Unmarshal([]byte(row.Payload), &share.Payload); err != nil {
		return nil, fmt.Errorf("decode chat share payload: %w", err)
	}
	if err := json.Unmarshal([]byte(row.Options), &share.Options); err != nil {
		return nil, fmt.Errorf("decode chat share options: %w", err)
	}
	return share, nil
}
