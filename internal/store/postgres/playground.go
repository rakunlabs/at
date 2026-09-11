package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.PlaygroundStorer = (*Postgres)(nil)

const (
	playgroundDefaultLimit uint = 50
	playgroundMaxLimit     uint = 200
	// playgroundTitleMaxRunes bounds a derived fork title so forking a fork
	// of a fork cannot grow the title without limit.
	playgroundTitleMaxRunes = 200
	// playgroundForkSuffix marks a derived title.
	playgroundForkSuffix = " (fork)"
	// playgroundUntitled is the base for a derived title when the source has
	// no title of its own.
	playgroundUntitled = "Untitled"
)

var (
	playgroundConversationColumns = []any{"id", "owner_user_id", "title", "system_prompt", "provider_key", "model", "config", "forked_from_id", "forked_from_sequence", "created_at", "updated_at"}
	playgroundMessageColumns      = []any{"id", "conversation_id", "sequence", "role", "provider_key", "model", "data", "created_at"}
)

type playgroundConversationRow struct {
	ID                 string         `db:"id"`
	OwnerUserID        string         `db:"owner_user_id"`
	Title              string         `db:"title"`
	SystemPrompt       string         `db:"system_prompt"`
	ProviderKey        string         `db:"provider_key"`
	Model              string         `db:"model"`
	Config             string         `db:"config"`
	ForkedFromID       sql.NullString `db:"forked_from_id"`
	ForkedFromSequence sql.NullInt64  `db:"forked_from_sequence"`
	CreatedAt          time.Time      `db:"created_at"`
	UpdatedAt          time.Time      `db:"updated_at"`
}

type playgroundMessageRow struct {
	ID             string    `db:"id"`
	ConversationID string    `db:"conversation_id"`
	Sequence       int64     `db:"sequence"`
	Role           string    `db:"role"`
	ProviderKey    string    `db:"provider_key"`
	Model          string    `db:"model"`
	Data           string    `db:"data"`
	CreatedAt      time.Time `db:"created_at"`
}

func playgroundConversationRowToRecord(row playgroundConversationRow) (service.PlaygroundConversation, error) {
	config, err := playgroundDecode(row.Config)
	if err != nil {
		return service.PlaygroundConversation{}, fmt.Errorf("decode playground conversation config: %w", err)
	}
	return service.PlaygroundConversation{
		ID:                 row.ID,
		OwnerUserID:        row.OwnerUserID,
		Title:              row.Title,
		SystemPrompt:       row.SystemPrompt,
		ProviderKey:        row.ProviderKey,
		Model:              row.Model,
		Config:             config,
		ForkedFromID:       row.ForkedFromID.String,
		ForkedFromSequence: row.ForkedFromSequence.Int64,
		CreatedAt:          row.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:          row.UpdatedAt.UTC().Format(time.RFC3339),
	}, nil
}

func playgroundMessageRowToRecord(row playgroundMessageRow) (service.PlaygroundMessage, error) {
	data, err := playgroundDecode(row.Data)
	if err != nil {
		return service.PlaygroundMessage{}, fmt.Errorf("decode playground message data: %w", err)
	}
	return service.PlaygroundMessage{
		ID:             row.ID,
		ConversationID: row.ConversationID,
		Sequence:       row.Sequence,
		Role:           row.Role,
		ProviderKey:    row.ProviderKey,
		Model:          row.Model,
		Data:           data,
		CreatedAt:      row.CreatedAt.UTC().Format(time.RFC3339),
	}, nil
}

// Both JSONB columns are opaque client state. They round-trip untouched, and
// an absent object is normalised to an empty one rather than a nil map.
func playgroundDecode(raw string) (map[string]any, error) {
	out := map[string]any{}
	if raw == "" || raw == "null" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func playgroundEncode(v map[string]any) (string, error) {
	if v == nil {
		return "{}", nil
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode playground payload: %w", err)
	}
	return string(encoded), nil
}

func playgroundLimit(n uint) uint {
	if n == 0 {
		return playgroundDefaultLimit
	}
	if n > playgroundMaxLimit {
		return playgroundMaxLimit
	}
	return n
}

// playgroundForkTitle derives the title of a fork. An explicit caller title
// always wins; otherwise the source title (or playgroundUntitled when the
// source has none) gains playgroundForkSuffix. The base is trimmed first so
// the suffix is never the part that gets cut off, which is what keeps the
// title readable when a fork is itself forked repeatedly.
func playgroundForkTitle(explicit, source string) string {
	if explicit != "" {
		return playgroundClampTitle(explicit, playgroundTitleMaxRunes)
	}
	base := source
	if base == "" {
		base = playgroundUntitled
	}
	return playgroundClampTitle(base, playgroundTitleMaxRunes-len([]rune(playgroundForkSuffix))) + playgroundForkSuffix
}

// Titles are counted in runes, not bytes, so a multibyte title is never cut
// mid-character.
func playgroundClampTitle(v string, max int) string {
	runes := []rune(v)
	if len(runes) <= max {
		return v
	}
	return string(runes[:max])
}

// playgroundLocked resolves and row-locks the owner's conversation, then runs
// fn inside the same transaction. Locking the parent is what serialises
// concurrent sequence allocation, so appends cannot collide on the
// (conversation_id, sequence) unique constraint.
func (p *Postgres) playgroundLocked(ctx context.Context, owner, id string, fn func(*goqu.TxDatabase, *playgroundConversationRow) error) error {
	if owner == "" || id == "" {
		return service.ErrPlaygroundNotFound
	}
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin playground transaction: %w", err)
	}
	defer tx.Rollback()

	var row playgroundConversationRow
	found, err := tx.From(p.tablePlaygroundChats).Select(playgroundConversationColumns...).Where(goqu.Ex{"id": id, "owner_user_id": owner}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &row)
	if err != nil {
		return fmt.Errorf("lock playground conversation: %w", err)
	}
	if !found {
		return service.ErrPlaygroundNotFound
	}
	if err := fn(tx, &row); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit playground transaction: %w", err)
	}
	return nil
}

func (p *Postgres) CreatePlaygroundConversation(ctx context.Context, c service.PlaygroundConversation) (*service.PlaygroundConversation, error) {
	if c.OwnerUserID == "" {
		return nil, service.ErrPlaygroundNotFound
	}
	config, err := playgroundEncode(c.Config)
	if err != nil {
		return nil, err
	}
	record := goqu.Record{
		"id":            ulid.Make().String(),
		"owner_user_id": c.OwnerUserID,
		"title":         c.Title,
		"system_prompt": c.SystemPrompt,
		"provider_key":  c.ProviderKey,
		"model":         c.Model,
		"config":        config,
		"created_at":    goqu.L("clock_timestamp()"),
		"updated_at":    goqu.L("clock_timestamp()"),
	}
	var row playgroundConversationRow
	if _, err := p.goqu.Insert(p.tablePlaygroundChats).Rows(record).Returning(playgroundConversationColumns...).Executor().ScanStructContext(ctx, &row); err != nil {
		return nil, fmt.Errorf("create playground conversation: %w", err)
	}
	out, err := playgroundConversationRowToRecord(row)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Conversations are ordered by recency so the history sidebar shows the most
// recently used first. The cursor is the full (updated_at, id) keyset of the
// anchor row, because updated_at alone is not unique and IDs alone do not
// follow recency.
func (p *Postgres) ListPlaygroundConversations(ctx context.Context, owner, before string, limit uint) ([]service.PlaygroundConversation, error) {
	items := []service.PlaygroundConversation{}
	if owner == "" {
		return items, service.ErrPlaygroundNotFound
	}
	q := p.goqu.From(p.tablePlaygroundChats).Select(playgroundConversationColumns...).Where(goqu.Ex{"owner_user_id": owner}).Order(goqu.I("updated_at").Desc(), goqu.I("id").Desc()).Limit(playgroundLimit(limit))
	if before != "" {
		var anchor playgroundConversationRow
		found, err := p.goqu.From(p.tablePlaygroundChats).Select(playgroundConversationColumns...).Where(goqu.Ex{"id": before, "owner_user_id": owner}).ScanStructContext(ctx, &anchor)
		if err != nil {
			return nil, fmt.Errorf("resolve playground conversation cursor: %w", err)
		}
		if !found {
			return nil, service.ErrPlaygroundNotFound
		}
		q = q.Where(goqu.Or(
			goqu.I("updated_at").Lt(anchor.UpdatedAt),
			goqu.And(goqu.I("updated_at").Eq(anchor.UpdatedAt), goqu.I("id").Lt(anchor.ID)),
		))
	}
	var rows []playgroundConversationRow
	if err := q.ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list playground conversations: %w", err)
	}
	for _, row := range rows {
		item, err := playgroundConversationRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (p *Postgres) GetPlaygroundConversation(ctx context.Context, owner, id string) (*service.PlaygroundConversation, error) {
	if owner == "" || id == "" {
		return nil, service.ErrPlaygroundNotFound
	}
	var row playgroundConversationRow
	found, err := p.goqu.From(p.tablePlaygroundChats).Select(playgroundConversationColumns...).Where(goqu.Ex{"id": id, "owner_user_id": owner}).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("get playground conversation: %w", err)
	}
	if !found {
		return nil, service.ErrPlaygroundNotFound
	}
	out, err := playgroundConversationRowToRecord(row)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (p *Postgres) PatchPlaygroundConversation(ctx context.Context, owner, id string, patch map[string]any) (*service.PlaygroundConversation, error) {
	values := goqu.Record{"updated_at": goqu.L("clock_timestamp()")}
	for key, value := range patch {
		switch key {
		case "title", "system_prompt", "provider_key", "model":
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("playground %s must be a string: %w", key, service.ErrPlaygroundConflict)
			}
			values[key] = text
		case "config":
			config, ok := value.(map[string]any)
			if !ok && value != nil {
				return nil, fmt.Errorf("playground config must be an object: %w", service.ErrPlaygroundConflict)
			}
			encoded, err := playgroundEncode(config)
			if err != nil {
				return nil, err
			}
			values[key] = encoded
		default:
			return nil, fmt.Errorf("unknown playground field %q: %w", key, service.ErrPlaygroundConflict)
		}
	}
	var out service.PlaygroundConversation
	err := p.playgroundLocked(ctx, owner, id, func(tx *goqu.TxDatabase, _ *playgroundConversationRow) error {
		var row playgroundConversationRow
		if _, err := tx.Update(p.tablePlaygroundChats).Set(values).Where(goqu.Ex{"id": id, "owner_user_id": owner}).Returning(playgroundConversationColumns...).Executor().ScanStructContext(ctx, &row); err != nil {
			return fmt.Errorf("patch playground conversation: %w", err)
		}
		var convErr error
		out, convErr = playgroundConversationRowToRecord(row)
		return convErr
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (p *Postgres) DeletePlaygroundConversation(ctx context.Context, owner, id string) error {
	// Messages disappear with the parent through ON DELETE CASCADE.
	return p.playgroundLocked(ctx, owner, id, func(tx *goqu.TxDatabase, _ *playgroundConversationRow) error {
		if _, err := tx.Delete(p.tablePlaygroundChats).Where(goqu.Ex{"id": id, "owner_user_id": owner}).Executor().ExecContext(ctx); err != nil {
			return fmt.Errorf("delete playground conversation: %w", err)
		}
		return nil
	})
}

// ForkPlaygroundConversation branches a new conversation off an existing one.
// Everything happens inside the source's row lock, so the prefix that is read
// is exactly the prefix that is copied even while the source is being appended
// to. The source itself is never written: a fork must not reorder the caller's
// history sidebar or move the source's updated_at.
func (p *Postgres) ForkPlaygroundConversation(ctx context.Context, owner, id string, fromSequence int64, title string) (*service.PlaygroundConversation, error) {
	if fromSequence < 1 {
		return nil, fmt.Errorf("from_sequence must be positive: %w", service.ErrPlaygroundConflict)
	}
	var out service.PlaygroundConversation
	err := p.playgroundLocked(ctx, owner, id, func(tx *goqu.TxDatabase, source *playgroundConversationRow) error {
		// One row over the cap is enough to detect an oversized prefix
		// without materialising the whole transcript.
		var prefix []playgroundMessageRow
		if err := tx.From(p.tablePlaygroundMessages).Select(playgroundMessageColumns...).
			Where(goqu.Ex{"conversation_id": id}, goqu.I("sequence").Lte(fromSequence)).
			Order(goqu.I("sequence").Asc()).Limit(service.PlaygroundForkMaxMessages+1).
			ScanStructsContext(ctx, &prefix); err != nil {
			return fmt.Errorf("read playground fork prefix: %w", err)
		}
		if len(prefix) > service.PlaygroundForkMaxMessages {
			return fmt.Errorf("fork prefix exceeds %d messages: %w", service.PlaygroundForkMaxMessages, service.ErrPlaygroundTooLarge)
		}
		// Sequences are gapless, so the last row of the prefix carries
		// fromSequence exactly when that message exists. Anything else means
		// the caller pointed past the end of the transcript.
		if len(prefix) == 0 || prefix[len(prefix)-1].Sequence != fromSequence {
			return fmt.Errorf("from_sequence %d does not name a message: %w", fromSequence, service.ErrPlaygroundConflict)
		}

		var row playgroundConversationRow
		if _, err := tx.Insert(p.tablePlaygroundChats).Rows(goqu.Record{
			"id":                   ulid.Make().String(),
			"owner_user_id":        owner,
			"title":                playgroundForkTitle(title, source.Title),
			"system_prompt":        source.SystemPrompt,
			"provider_key":         source.ProviderKey,
			"model":                source.Model,
			"config":               source.Config,
			"forked_from_id":       source.ID,
			"forked_from_sequence": fromSequence,
			"created_at":           goqu.L("clock_timestamp()"),
			"updated_at":           goqu.L("clock_timestamp()"),
		}).Returning(playgroundConversationColumns...).Executor().ScanStructContext(ctx, &row); err != nil {
			return fmt.Errorf("create playground fork: %w", err)
		}

		// Copied messages are stamped with the fork time rather than the
		// original one: they are new rows in a new conversation, and the
		// list cursor orders by the sequence anyway.
		records := make([]any, 0, len(prefix))
		for i, m := range prefix {
			records = append(records, goqu.Record{
				"id":              ulid.Make().String(),
				"conversation_id": row.ID,
				"sequence":        int64(i + 1),
				"role":            m.Role,
				"provider_key":    m.ProviderKey,
				"model":           m.Model,
				"data":            m.Data,
				"created_at":      row.CreatedAt,
			})
		}
		if _, err := tx.Insert(p.tablePlaygroundMessages).Rows(records...).Executor().ExecContext(ctx); err != nil {
			return fmt.Errorf("copy playground fork messages: %w", err)
		}
		var convErr error
		out, convErr = playgroundConversationRowToRecord(row)
		return convErr
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Messages are fetched newest-first so the cursor pages backwards, then
// reversed so callers always receive chronological order.
func (p *Postgres) ListPlaygroundMessages(ctx context.Context, owner, id, before string, limit uint) ([]service.PlaygroundMessage, error) {
	items := []service.PlaygroundMessage{}
	if owner == "" || id == "" {
		return items, service.ErrPlaygroundNotFound
	}
	if _, err := p.GetPlaygroundConversation(ctx, owner, id); err != nil {
		return nil, err
	}
	q := p.goqu.From(p.tablePlaygroundMessages).Select(playgroundMessageColumns...).Where(goqu.Ex{"conversation_id": id}).Order(goqu.I("sequence").Desc()).Limit(playgroundLimit(limit))
	if before != "" {
		var sequence int64
		found, err := p.goqu.From(p.tablePlaygroundMessages).Select("sequence").Where(goqu.Ex{"conversation_id": id, "id": before}).ScanValContext(ctx, &sequence)
		if err != nil {
			return nil, fmt.Errorf("resolve playground message cursor: %w", err)
		}
		if !found {
			return nil, service.ErrPlaygroundNotFound
		}
		q = q.Where(goqu.I("sequence").Lt(sequence))
	}
	var rows []playgroundMessageRow
	if err := q.ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list playground messages: %w", err)
	}
	for i := len(rows) - 1; i >= 0; i-- {
		item, err := playgroundMessageRowToRecord(rows[i])
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (p *Postgres) AppendPlaygroundMessages(ctx context.Context, owner, id string, items []service.PlaygroundMessage) ([]service.PlaygroundMessage, error) {
	for _, item := range items {
		if !service.ValidPlaygroundRole(item.Role) {
			return nil, fmt.Errorf("invalid playground message role %q: %w", item.Role, service.ErrPlaygroundConflict)
		}
	}
	out := []service.PlaygroundMessage{}
	err := p.playgroundLocked(ctx, owner, id, func(tx *goqu.TxDatabase, _ *playgroundConversationRow) error {
		if len(items) == 0 {
			return nil
		}
		// The parent row lock taken by playgroundLocked serialises this
		// MAX+1 allocation across connections and replicas.
		var admission struct {
			Sequence  int64     `db:"sequence"`
			CreatedAt time.Time `db:"created_at"`
		}
		if _, err := tx.From(p.tablePlaygroundMessages).Select(goqu.L("COALESCE(MAX(sequence), 0)").As("sequence"), goqu.L("clock_timestamp()").As("created_at")).Where(goqu.Ex{"conversation_id": id}).ScanStructContext(ctx, &admission); err != nil {
			return fmt.Errorf("allocate playground sequence: %w", err)
		}
		records := make([]any, 0, len(items))
		for _, item := range items {
			admission.Sequence++
			data, err := playgroundEncode(item.Data)
			if err != nil {
				return err
			}
			stored := service.PlaygroundMessage{
				ID:             ulid.Make().String(),
				ConversationID: id,
				Sequence:       admission.Sequence,
				Role:           item.Role,
				ProviderKey:    item.ProviderKey,
				Model:          item.Model,
				Data:           item.Data,
				CreatedAt:      admission.CreatedAt.UTC().Format(time.RFC3339),
			}
			if stored.Data == nil {
				stored.Data = map[string]any{}
			}
			records = append(records, goqu.Record{
				"id":              stored.ID,
				"conversation_id": id,
				"sequence":        stored.Sequence,
				"role":            stored.Role,
				"provider_key":    stored.ProviderKey,
				"model":           stored.Model,
				"data":            data,
				"created_at":      admission.CreatedAt,
			})
			out = append(out, stored)
		}
		if _, err := tx.Insert(p.tablePlaygroundMessages).Rows(records...).Executor().ExecContext(ctx); err != nil {
			return fmt.Errorf("append playground messages: %w", err)
		}
		if _, err := tx.Update(p.tablePlaygroundChats).Set(goqu.Record{"updated_at": goqu.L("clock_timestamp()")}).Where(goqu.Ex{"id": id, "owner_user_id": owner}).Executor().ExecContext(ctx); err != nil {
			return fmt.Errorf("touch playground conversation: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (p *Postgres) TruncatePlaygroundMessages(ctx context.Context, owner, id string, fromSequence int64) error {
	if fromSequence < 1 {
		return fmt.Errorf("from_sequence must be positive: %w", service.ErrPlaygroundConflict)
	}
	return p.playgroundLocked(ctx, owner, id, func(tx *goqu.TxDatabase, _ *playgroundConversationRow) error {
		if _, err := tx.Delete(p.tablePlaygroundMessages).Where(goqu.Ex{"conversation_id": id}, goqu.I("sequence").Gte(fromSequence)).Executor().ExecContext(ctx); err != nil {
			return fmt.Errorf("truncate playground messages: %w", err)
		}
		if _, err := tx.Update(p.tablePlaygroundChats).Set(goqu.Record{"updated_at": goqu.L("clock_timestamp()")}).Where(goqu.Ex{"id": id, "owner_user_id": owner}).Executor().ExecContext(ctx); err != nil {
			return fmt.Errorf("touch playground conversation: %w", err)
		}
		return nil
	})
}
