package postgres

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.AuthMobileStorer = (*Postgres)(nil)

func (p *Postgres) CreateAuthMobileRequest(ctx context.Context, r service.AuthMobileRequest) error {
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin mobile request: %w", err)
	}
	defer tx.Rollback()
	var claimed bool
	if _, err := tx.From(p.tableAuthBootstrap).Select("claimed").ForUpdate(goqu.Wait).ScanValContext(ctx, &claimed); err != nil {
		return fmt.Errorf("lock mobile admission: %w", err)
	}
	// Count all rows, including expired rows awaiting bounded janitor cleanup.
	n, err := tx.From(p.tableAuthMobileRequests).CountContext(ctx)
	if err != nil {
		return fmt.Errorf("count mobile requests: %w", err)
	}
	if n >= 1000 {
		return service.ErrAuthSessionLimit
	}
	if err := checkAuthAdmissionDeadline(ctx, tx, r.ExpiresAt); err != nil {
		return err
	}
	if _, err := tx.Insert(p.tableAuthMobileRequests).Rows(goqu.Record{"id": r.ID, "challenge": r.Challenge, "state": r.State, "remember": r.Remember, "device_name": r.DeviceName, "created_at": r.CreatedAt, "expires_at": r.ExpiresAt}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("insert mobile request: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit mobile request: %w", err)
	}
	return nil
}

func (p *Postgres) GetAuthMobileRequest(ctx context.Context, id string) (*service.AuthMobileRequest, error) {
	var r service.AuthMobileRequest
	found, err := p.goqu.From(p.tableAuthMobileRequests).Where(goqu.Ex{"id": id, "user_id": ""}, goqu.I("expires_at").Gt(goqu.L("clock_timestamp()"))).ScanStructContext(ctx, &r)
	if err != nil {
		return nil, fmt.Errorf("get mobile request: %w", err)
	}
	if !found {
		return nil, nil
	}
	return &r, nil
}

// Lock order is user -> browser family -> request, matching revocation and redemption.
func (p *Postgres) DecideAuthMobileRequest(ctx context.Context, id, userID, webSession, codeHash string) (*service.AuthMobileRequest, error) {
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin mobile decision: %w", err)
	}
	defer tx.Rollback()
	var u authUserRow
	found, err := tx.From(p.tableAuthUsers).Select("id", "session_version").Where(goqu.Ex{"id": userID, "disabled": false}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &u)
	if err != nil {
		return nil, fmt.Errorf("lock approving user: %w", err)
	}
	if !found {
		return nil, service.ErrAuthConflict
	}
	var webExpires time.Time
	found, err = tx.From(p.tableAuthSessions).Select("expires_at").Where(goqu.Ex{"hash": webSession, "user_id": userID, "version": u.SessionVersion, "transport": "web"}).ForUpdate(goqu.Wait).ScanValContext(ctx, &webExpires)
	if err != nil {
		return nil, fmt.Errorf("lock approving session: %w", err)
	}
	if !found {
		return nil, service.ErrAuthConflict
	}
	var r service.AuthMobileRequest
	found, err = tx.From(p.tableAuthMobileRequests).Where(goqu.Ex{"id": id, "user_id": ""}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &r)
	if err != nil {
		return nil, fmt.Errorf("lock mobile request: %w", err)
	}
	if !found {
		return nil, service.ErrAuthConflict
	}
	if err := checkAuthAdmissionDeadline(ctx, tx, webExpires); err != nil {
		return nil, err
	}
	if err := checkAuthAdmissionDeadline(ctx, tx, r.ExpiresAt); err != nil {
		return nil, err
	}
	if codeHash == "" {
		_, err = tx.Delete(p.tableAuthMobileRequests).Where(goqu.Ex{"id": id}).Executor().ExecContext(ctx)
	} else {
		var now time.Time
		if _, err := tx.Select(goqu.L("clock_timestamp()")).ScanValContext(ctx, &now); err != nil {
			return nil, fmt.Errorf("read approval time: %w", err)
		}
		r.CodeExpiresAt = now.Add(time.Minute)
		if r.CodeExpiresAt.After(r.ExpiresAt) {
			r.CodeExpiresAt = r.ExpiresAt
		}
		if r.CodeExpiresAt.After(webExpires) {
			r.CodeExpiresAt = webExpires
		}
		_, err = tx.Update(p.tableAuthMobileRequests).Set(goqu.Record{"user_id": userID, "version": u.SessionVersion, "web_session": webSession, "code_hash": codeHash, "code_expires_at": r.CodeExpiresAt}).Where(goqu.Ex{"id": id}).Executor().ExecContext(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("persist mobile decision: %w", err)
	}
	if err := checkAuthAdmissionDeadline(ctx, tx, webExpires); err != nil {
		return nil, err
	}
	if err := checkAuthAdmissionDeadline(ctx, tx, r.ExpiresAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit mobile decision: %w", err)
	}
	return &r, nil
}

func (p *Postgres) RedeemAuthMobileCode(ctx context.Context, hash, challenge string, s service.AuthSession, sessionTTL, rememberTTL time.Duration) (*service.AuthUser, *service.AuthSession, error) {
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("begin mobile redemption: %w", err)
	}
	defer tx.Rollback()
	var candidate service.AuthMobileRequest
	found, err := tx.From(p.tableAuthMobileRequests).Where(goqu.Ex{"code_hash": hash}, goqu.I("user_id").Neq("")).ScanStructContext(ctx, &candidate)
	if err != nil {
		return nil, nil, fmt.Errorf("find mobile code: %w", err)
	}
	if !found {
		return nil, nil, service.ErrAuthConflict
	}
	var u authUserRow
	userFound, err := tx.From(p.tableAuthUsers).Select("id", "username", "admin", "disabled", "session_version").Where(goqu.Ex{"id": candidate.UserID}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &u)
	if err != nil {
		return nil, nil, fmt.Errorf("lock mobile user: %w", err)
	}
	var webExpires time.Time
	webFound, err := tx.From(p.tableAuthSessions).Select("expires_at").Where(goqu.Ex{"hash": candidate.WebSession, "user_id": candidate.UserID, "version": candidate.Version, "transport": "web"}).ForUpdate(goqu.Wait).ScanValContext(ctx, &webExpires)
	if err != nil {
		return nil, nil, fmt.Errorf("lock mobile approving family: %w", err)
	}
	var r service.AuthMobileRequest
	found, err = tx.From(p.tableAuthMobileRequests).Where(goqu.Ex{"code_hash": hash}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &r)
	if err != nil {
		return nil, nil, fmt.Errorf("lock mobile code: %w", err)
	}
	if !found {
		return nil, nil, service.ErrAuthConflict
	}
	if _, err := tx.Delete(p.tableAuthMobileRequests).Where(goqu.Ex{"id": r.ID}).Executor().ExecContext(ctx); err != nil {
		return nil, nil, fmt.Errorf("consume mobile code: %w", err)
	}
	var now time.Time
	if _, err := tx.Select(goqu.L("clock_timestamp()")).ScanValContext(ctx, &now); err != nil {
		return nil, nil, fmt.Errorf("read redemption time: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(r.Challenge), []byte(challenge)) != 1 || !userFound || u.Disabled || u.SessionVersion != r.Version || !webFound || !webExpires.After(now) || !r.CodeExpiresAt.After(now) || !r.ExpiresAt.After(now) {
		if err := tx.Commit(); err != nil {
			return nil, nil, fmt.Errorf("commit rejected mobile code: %w", err)
		}
		return nil, nil, service.ErrAuthConflict
	}
	s.UserID, s.Version, s.Remember, s.Transport = u.ID, u.SessionVersion, r.Remember, "mobile"
	ttl := sessionTTL
	if r.Remember {
		ttl = rememberTTL
	}
	s.ExpiresAt, s.AccessExpiresAt = now.Add(ttl), now.Add(10*time.Minute)
	if s.AccessExpiresAt.After(s.ExpiresAt) {
		s.AccessExpiresAt = s.ExpiresAt
	}
	s.AdmissionDeadline = r.CodeExpiresAt
	if err := p.insertAuthSession(ctx, tx, s); err != nil {
		return nil, nil, err
	}
	if err := checkAuthAdmissionDeadline(ctx, tx, webExpires); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit mobile redemption: %w", err)
	}
	return authUserRowToRecord(u), &s, nil
}

func (p *Postgres) CleanupAuthMobileRequests(ctx context.Context, limit uint) error {
	if limit == 0 || limit > 1000 {
		return fmt.Errorf("mobile cleanup limit must be 1-1000")
	}
	selected := p.goqu.From(p.tableAuthMobileRequests).Select("id").Where(goqu.I("expires_at").Lte(goqu.L("clock_timestamp()"))).Order(goqu.I("expires_at").Asc()).Limit(limit).ForUpdate(goqu.SkipLocked)
	if _, err := p.goqu.Delete(p.tableAuthMobileRequests).Where(goqu.I("id").In(selected)).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("clean mobile requests: %w", err)
	}
	return nil
}
