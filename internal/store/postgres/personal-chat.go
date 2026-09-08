package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.PersonalChatStorer = (*Postgres)(nil)

type personalMessageRow struct {
	Sequence int64  `db:"sequence"`
	Data     string `db:"data"`
	Status   string `db:"status"`
	Hash     string `db:"request_hash"`
	Token    string `db:"lease_token"`
}

func (r personalMessageRow) message() (service.PersonalChatMessage, error) {
	var m service.PersonalChatMessage
	err := json.Unmarshal([]byte(r.Data), &m)
	m.Status = r.Status
	m.Sequence = r.Sequence
	return m, err
}

// All mutations lock the parent first. The partial unique index is a second
// defence against concurrent admission. Database time, not worker time, fences leases.
func (p *Postgres) personalLocked(ctx context.Context, owner, id string, fn func(*goqu.TxDatabase, *service.PersonalConversation) error) error {
	if owner == "" {
		return service.ErrPersonalChatNotFound
	}
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin personal chat: %w", err)
	}
	defer tx.Rollback()
	var c service.PersonalConversation
	found, err := tx.From(p.tableConversations).Where(goqu.Ex{"id": id, "owner_user_id": owner}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &c)
	if err != nil {
		return fmt.Errorf("lock personal conversation: %w", err)
	}
	if !found {
		return service.ErrPersonalChatNotFound
	}
	reconciled, err := tx.Update(p.tablePersonalMessages).Set(goqu.Record{"status": "failed", "lease_token": "", "data": goqu.L(`data || '{"error":"generation_interrupted"}'::jsonb`)}).Where(goqu.Ex{"conversation_id": id, "status": []string{"pending", "streaming"}}, goqu.Or(goqu.I("lease_until").Lte(goqu.L("clock_timestamp()")), goqu.I("deadline").Lte(goqu.L("clock_timestamp()")))).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("reconcile personal turn: %w", err)
	}
	if n, err := reconciled.RowsAffected(); err != nil {
		return fmt.Errorf("count reconciled turns: %w", err)
	} else if n != 0 {
		if _, err := tx.Update(p.tableConversations).Set(goqu.Record{"updated_at": goqu.L("clock_timestamp()")}).Where(goqu.Ex{"id": id, "owner_user_id": owner}).Returning(goqu.Star()).Executor().ScanStructContext(ctx, &c); err != nil {
			return fmt.Errorf("touch recovered conversation: %w", err)
		}
	}
	if err = fn(tx, &c); err != nil {
		return fmt.Errorf("operate on personal conversation: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit personal chat: %w", err)
	}
	return nil
}

func personalLimit(n uint) uint {
	if n == 0 {
		return 50
	}
	if n > 100 {
		return 100
	}
	return n
}

func (p *Postgres) CreatePersonalConversation(ctx context.Context, c service.PersonalConversation) (*service.PersonalConversation, error) {
	if c.OwnerUserID == "" {
		return nil, service.ErrPersonalChatNotFound
	}
	c.ID = ulid.Make().String()
	c.CreatedAt = time.Now().UTC()
	c.UpdatedAt = c.CreatedAt
	_, err := p.goqu.Insert(p.tableConversations).Rows(c).Executor().ExecContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("create personal conversation: %w", err)
	}
	return &c, nil
}

func (p *Postgres) ListPersonalConversations(ctx context.Context, owner, before string, limit uint) ([]service.PersonalConversation, error) {
	items := []service.PersonalConversation{}
	if owner == "" {
		return items, service.ErrPersonalChatNotFound
	}
	q := p.goqu.From(p.tableConversations).Where(goqu.Ex{"owner_user_id": owner}).Order(goqu.I("id").Desc()).Limit(personalLimit(limit))
	if before != "" {
		q = q.Where(goqu.I("id").Lt(before))
	}
	if err := q.ScanStructsContext(ctx, &items); err != nil {
		return nil, fmt.Errorf("list personal conversations: %w", err)
	}
	// Bounded owner reads recover abandoned turns without a global janitor.
	for i := range items {
		c, err := p.GetPersonalConversation(ctx, owner, items[i].ID)
		if err != nil {
			return nil, err
		}
		items[i] = *c
	}
	return items, nil
}

func (p *Postgres) GetPersonalConversation(ctx context.Context, owner, id string) (*service.PersonalConversation, error) {
	var result service.PersonalConversation
	err := p.personalLocked(ctx, owner, id, func(_ *goqu.TxDatabase, c *service.PersonalConversation) error { result = *c; return nil })
	return &result, err
}

func (p *Postgres) personalIdle(ctx context.Context, tx *goqu.TxDatabase, id string) error {
	n, err := tx.From(p.tablePersonalMessages).Where(goqu.Ex{"conversation_id": id, "status": []string{"pending", "streaming"}}).CountContext(ctx)
	if err != nil {
		return fmt.Errorf("check personal turn: %w", err)
	}
	if n != 0 {
		return service.ErrPersonalChatConflict
	}
	return nil
}

func (p *Postgres) PatchPersonalConversation(ctx context.Context, owner, id string, patch map[string]string) (*service.PersonalConversation, error) {
	var result service.PersonalConversation
	err := p.personalLocked(ctx, owner, id, func(tx *goqu.TxDatabase, c *service.PersonalConversation) error {
		if err := p.personalIdle(ctx, tx, id); err != nil {
			return err
		}
		values := goqu.Record{"updated_at": goqu.L("clock_timestamp()")}
		for k, v := range patch {
			switch k {
			case "title", "provider_key", "model", "system_prompt":
				values[k] = v
			default:
				return service.ErrPersonalChatConflict
			}
		}
		_, err := tx.Update(p.tableConversations).Set(values).Where(goqu.Ex{"id": id, "owner_user_id": owner}).Returning(goqu.Star()).Executor().ScanStructContext(ctx, &result)
		return err
	})
	return &result, err
}

func (p *Postgres) DeletePersonalConversation(ctx context.Context, owner, id string) error {
	return p.personalLocked(ctx, owner, id, func(tx *goqu.TxDatabase, _ *service.PersonalConversation) error {
		if err := p.personalIdle(ctx, tx, id); err != nil {
			return err
		}
		_, err := tx.Delete(p.tableConversations).Where(goqu.Ex{"id": id, "owner_user_id": owner}).Executor().ExecContext(ctx)
		return err
	})
}

func (p *Postgres) ListPersonalMessages(ctx context.Context, owner, id, before string, limit uint) ([]service.PersonalChatMessage, error) {
	items := []service.PersonalChatMessage{}
	err := p.personalLocked(ctx, owner, id, func(tx *goqu.TxDatabase, _ *service.PersonalConversation) error {
		q := tx.From(p.tablePersonalMessages).Select("data", "status", "request_hash", "lease_token", "sequence").Where(goqu.Ex{"conversation_id": id}).Order(goqu.I("sequence").Desc()).Limit(personalLimit(limit))
		if before != "" {
			var sequence int64
			found, err := tx.From(p.tablePersonalMessages).Select("sequence").Where(goqu.Ex{"conversation_id": id, "id": before}).ScanValContext(ctx, &sequence)
			if err != nil {
				return err
			}
			if !found {
				return service.ErrPersonalChatNotFound
			}
			q = q.Where(goqu.I("sequence").Lt(sequence))
		}
		var rows []personalMessageRow
		if err := q.ScanStructsContext(ctx, &rows); err != nil {
			return err
		}
		for _, row := range rows {
			m, err := row.message()
			if err != nil {
				return err
			}
			items = append(items, m)
		}
		return nil
	})
	return items, err
}

func (p *Postgres) GetPersonalMessage(ctx context.Context, owner, id, messageID string) (*service.PersonalChatMessage, error) {
	var result service.PersonalChatMessage
	err := p.personalLocked(ctx, owner, id, func(tx *goqu.TxDatabase, _ *service.PersonalConversation) error {
		var row personalMessageRow
		found, err := tx.From(p.tablePersonalMessages).Select("data", "status", "request_hash", "lease_token", "sequence").Where(goqu.Ex{"conversation_id": id, "id": messageID}).ScanStructContext(ctx, &row)
		if err != nil {
			return err
		}
		if !found {
			return service.ErrPersonalChatNotFound
		}
		result, err = row.message()
		return err
	})
	return &result, err
}

func (p *Postgres) BeginPersonalTurn(ctx context.Context, owner, id, requestID, content string) (*service.PersonalChatTurn, error) {
	hash := sha256.Sum256([]byte(content))
	digest := hex.EncodeToString(hash[:])
	turn := &service.PersonalChatTurn{}
	err := p.personalLocked(ctx, owner, id, func(tx *goqu.TxDatabase, c *service.PersonalConversation) error {
		turn.Conversation = *c
		var rows []personalMessageRow
		if err := tx.From(p.tablePersonalMessages).Select("data", "status", "request_hash", "lease_token", "sequence").Where(goqu.Ex{"conversation_id": id, "request_id": requestID}).ScanStructsContext(ctx, &rows); err != nil {
			return err
		}
		if len(rows) != 0 {
			for _, row := range rows {
				if row.Hash != digest {
					return service.ErrPersonalChatConflict
				}
				m, err := row.message()
				if err != nil {
					return err
				}
				if m.Role == "user" {
					turn.User = m
				} else {
					turn.Assistant = m
				}
			}
			turn.Replay = true
			return nil
		}
		if err := p.personalIdle(ctx, tx, id); err != nil {
			return err
		}
		turn.LeaseToken = ulid.Make().String()
		var admission struct {
			Sequence  int64     `db:"sequence"`
			CreatedAt time.Time `db:"created_at"`
		}
		// The locked parent serializes MAX+1 allocation across replicas. Database
		// time is display metadata only; sequence is the authoritative chronology.
		if _, err := tx.From(p.tablePersonalMessages).Select(goqu.L("COALESCE(MAX(sequence), 0)").As("sequence"), goqu.L("clock_timestamp()").As("created_at")).Where(goqu.Ex{"conversation_id": id}).ScanStructContext(ctx, &admission); err != nil {
			return err
		}
		for _, role := range []string{"user", "assistant"} {
			admission.Sequence++
			m := service.PersonalChatMessage{ID: ulid.Make().String(), Sequence: admission.Sequence, ConversationID: id, RequestID: requestID, Role: role, Status: "completed", ProviderKey: c.ProviderKey, Model: c.Model, CreatedAt: admission.CreatedAt.UTC()}
			if role == "user" {
				m.Content = content
				turn.User = m
			} else {
				m.Status = "pending"
				turn.Assistant = m
			}
			data, err := json.Marshal(m)
			if err != nil {
				return err
			}
			_, err = tx.Insert(p.tablePersonalMessages).Rows(goqu.Record{"id": m.ID, "sequence": m.Sequence, "conversation_id": id, "request_id": requestID, "request_hash": digest, "role": role, "status": m.Status, "data": string(data), "lease_token": turn.LeaseToken, "lease_until": goqu.L("clock_timestamp() + interval '15 seconds'"), "deadline": goqu.L("clock_timestamp() + interval '5 minutes'")}).Executor().ExecContext(ctx)
			if err != nil {
				return err
			}
		}
		_, err := tx.Update(p.tableConversations).Set(goqu.Record{"updated_at": goqu.L("clock_timestamp()")}).Where(goqu.Ex{"id": id, "owner_user_id": owner}).Executor().ExecContext(ctx)
		return err
	})
	return turn, err
}

func (p *Postgres) CheckpointPersonalTurn(ctx context.Context, owner, id, token string, m service.PersonalChatMessage) (*service.PersonalChatMessage, error) {
	var result service.PersonalChatMessage
	err := p.personalLocked(ctx, owner, id, func(tx *goqu.TxDatabase, _ *service.PersonalConversation) error {
		var row personalMessageRow
		found, err := tx.From(p.tablePersonalMessages).Select("data", "status", "request_hash", "lease_token", "sequence").Where(goqu.Ex{"id": m.ID, "conversation_id": id, "role": "assistant"}).ScanStructContext(ctx, &row)
		if err != nil {
			return err
		}
		if !found {
			return service.ErrPersonalChatNotFound
		}
		result, err = row.message()
		if err != nil {
			return err
		}
		if !result.Active() || row.Token != token || token == "" {
			return nil
		}
		// Only mutable output fields are copied; worker input cannot change identity or snapshots.
		result.Content = m.Content
		result.Status = m.Status
		result.Error = m.Error
		result.FinishReason = m.FinishReason
		result.Usage = m.Usage
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		values := goqu.Record{"data": string(data), "status": result.Status, "lease_until": goqu.L("LEAST(deadline, clock_timestamp() + interval '15 seconds')")}
		if !result.Active() {
			values["lease_token"] = ""
		}
		written, err := tx.Update(p.tablePersonalMessages).Set(values).Where(goqu.Ex{"id": m.ID, "conversation_id": id, "lease_token": token, "status": []string{"pending", "streaming"}}, goqu.I("lease_until").Gt(goqu.L("clock_timestamp()")), goqu.I("deadline").Gt(goqu.L("clock_timestamp()"))).Executor().ExecContext(ctx)
		if err != nil {
			return err
		}
		n, err := written.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return service.ErrPersonalChatConflict
		}
		if !result.Active() {
			_, err = tx.Update(p.tableConversations).Set(goqu.Record{"updated_at": goqu.L("clock_timestamp()")}).Where(goqu.Ex{"id": id, "owner_user_id": owner}).Executor().ExecContext(ctx)
			return err
		}
		return nil
	})
	return &result, err
}

// Cancellation is itself a durable terminal transition. It immediately fences
// the worker; its next checkpoint observes cancellation on any replica.
func (p *Postgres) CancelPersonalTurn(ctx context.Context, owner, id, messageID string) (*service.PersonalChatMessage, error) {
	var result service.PersonalChatMessage
	err := p.personalLocked(ctx, owner, id, func(tx *goqu.TxDatabase, _ *service.PersonalConversation) error {
		var row personalMessageRow
		found, err := tx.From(p.tablePersonalMessages).Select("data", "status", "request_hash", "lease_token", "sequence").Where(goqu.Ex{"id": messageID, "conversation_id": id, "role": "assistant"}).ScanStructContext(ctx, &row)
		if err != nil {
			return err
		}
		if !found {
			return service.ErrPersonalChatNotFound
		}
		result, err = row.message()
		if err != nil {
			return err
		}
		if !result.Active() {
			return nil
		}
		result.Status = "cancelled"
		result.Error = "generation_cancelled"
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, err = tx.Update(p.tablePersonalMessages).Set(goqu.Record{"status": result.Status, "data": string(data), "lease_token": ""}).Where(goqu.Ex{"id": messageID, "conversation_id": id}).Executor().ExecContext(ctx)
		if err != nil {
			return err
		}
		_, err = tx.Update(p.tableConversations).Set(goqu.Record{"updated_at": goqu.L("clock_timestamp()")}).Where(goqu.Ex{"id": id, "owner_user_id": owner}).Executor().ExecContext(ctx)
		return err
	})
	return &result, err
}
