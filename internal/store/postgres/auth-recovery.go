package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

type authRecoveryActorKey struct{}
type authRecoveryActor struct {
	ID, SessionID, ProofHash string
	Version                  int64
}

// IssueAuthRecovery atomically consumes administrator recent-auth authority and
// creates target-bound recovery authority under both account locks.
func (p *Postgres) IssueAuthRecovery(ctx context.Context, actorID, sessionID string, version int64, proofHash, targetID, ticketHash string) error {
	if len(proofHash) != 64 || len(ticketHash) != 64 {
		return service.ErrAuthConflict
	}
	ctx = context.WithValue(ctx, authRecoveryActorKey{}, authRecoveryActor{actorID, sessionID, proofHash, version})
	return p.UpdateAuthSecurity(ctx, targetID, "", -1, func(u *service.AuthSecurityUpdate) error {
		live := u.State.Transactions[:0]
		for _, c := range u.State.Transactions {
			if c.Purpose != "recovery" {
				live = append(live, c)
			}
		}
		u.State.Transactions = live
		u.State.Transactions = append(u.State.Transactions, service.AuthTransaction{Hash: ticketHash, Purpose: "recovery", Method: "admin:" + actorID, Version: u.User.SessionVersion, Expires: u.Now.Add(15 * time.Minute)})
		u.RecoveryAction = "issue"
		u.RecoverySource = "admin:" + actorID
		return nil
	})
}

func (p *Postgres) consumeRecoveryActor(ctx context.Context, tx *goqu.TxDatabase, actor authRecoveryActor, now time.Time) (time.Time, error) {
	var user authUserRow
	found, err := tx.From(p.tableAuthUsers).Select("id", "username", "password_hash", "admin", "disabled", "session_version").Where(goqu.Ex{"id": actor.ID, "admin": true, "disabled": false, "session_version": actor.Version}).ScanStructContext(ctx, &user)
	if err != nil {
		return time.Time{}, fmt.Errorf("check recovery administrator: %w", err)
	}
	if !found {
		return time.Time{}, service.ErrAuthConflict
	}
	var session string
	found, err = tx.From(p.tableAuthSessions).Select("hash").Where(goqu.Ex{"hash": actor.SessionID, "user_id": actor.ID, "version": actor.Version, "transport": "web"}, goqu.I("expires_at").Gt(now)).ScanValContext(ctx, &session)
	if err != nil {
		return time.Time{}, err
	}
	if !found {
		return time.Time{}, service.ErrAuthConflict
	}
	var blob []byte
	found, err = tx.From(p.authSecurityTable("auth_security")).Select("data").Where(goqu.Ex{"user_id": actor.ID}).ScanValContext(ctx, &blob)
	if err != nil {
		return time.Time{}, err
	}
	if !found {
		return time.Time{}, service.ErrAuthConflict
	}
	var state service.AuthSecurityState
	if err := json.Unmarshal(blob, &state); err != nil {
		return time.Time{}, err
	}
	// Secrets stay encrypted: only the hashed purpose-bound proof is changed.
	for i := range state.Transactions {
		c := &state.Transactions[i]
		if c.Hash == actor.ProofHash && c.Purpose == "proof:recovery.issue" && c.SessionID == actor.SessionID && c.Version == actor.Version && c.Expires.After(now) {
			deadline := c.Expires
			c.Expires = now
			blob, err = json.Marshal(state)
			if err != nil {
				return time.Time{}, err
			}
			_, err = tx.Update(p.authSecurityTable("auth_security")).Set(goqu.Record{"data": goqu.L("?::jsonb", string(blob))}).Where(goqu.Ex{"user_id": actor.ID}).Executor().ExecContext(ctx)
			return deadline, err
		}
	}
	return time.Time{}, service.ErrAuthConflict
}
