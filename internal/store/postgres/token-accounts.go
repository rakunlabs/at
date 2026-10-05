package postgres

import (
	"context"
	"fmt"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.APITokenAccountStorer = (*Postgres)(nil)

// DescribeAPITokenAccounts resolves the owner, creator and last updater of a
// token the caller may read. Admission is the token's own management check, so
// a foreign personal token answers not-found exactly like the other routes.
// Membership is reported for the token's workspace only.
func (p *Postgres) DescribeAPITokenAccounts(ctx context.Context, id string) ([]service.APITokenAccount, error) {
	if err := p.AuthorizeAPITokenManagement(ctx, id, "tokens.read"); err != nil {
		return nil, err
	}
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}

	var row struct {
		OwnerUserID string `db:"owner_user_id"`
		CreatedBy   string `db:"created_by"`
		UpdatedBy   string `db:"updated_by"`
	}
	found, err := p.goqu.From(p.tableAPITokens).Select("owner_user_id", "created_by", "updated_by").
		Where(goqu.Ex{"id": id, "workspace_id": a.WorkspaceID}).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("get api token accounts: %w", err)
	}
	if !found {
		return nil, service.ErrAccessResourceNotFound
	}

	out := []service.APITokenAccount{}
	index := map[string]int{}
	for _, ref := range []struct{ id, role string }{
		{row.OwnerUserID, "owner"},
		{row.CreatedBy, "created_by"},
		{row.UpdatedBy, "updated_by"},
	} {
		if ref.id == "" {
			continue
		}
		if i, ok := index[ref.id]; ok {
			out[i].Roles = append(out[i].Roles, ref.role)
			continue
		}
		index[ref.id] = len(out)
		out = append(out, service.APITokenAccount{ID: ref.id, Roles: []string{ref.role}, Identities: []service.APITokenAccountIdentity{}})
	}
	if len(out) == 0 {
		return out, nil
	}

	ids := make([]string, 0, len(out))
	for _, account := range out {
		ids = append(ids, account.ID)
	}

	var users []authUserRow
	if err := p.goqu.From(p.tableAuthUsers).Select("id", "username", "password_hash", "admin", "disabled", "session_version").
		Where(goqu.I("id").In(ids)).ScanStructsContext(ctx, &users); err != nil {
		return nil, fmt.Errorf("get api token account users: %w", err)
	}
	for _, u := range users {
		account := &out[index[u.ID]]
		account.Found = true
		account.Username = u.Username
		account.Disabled = u.Disabled
		account.PlatformAdmin = u.Admin
	}

	var members []service.WorkspaceMembership
	if err := p.goqu.From(p.workspaceTable("workspace_memberships")).
		Where(goqu.Ex{"workspace_id": a.WorkspaceID}, goqu.I("user_id").In(ids)).ScanStructsContext(ctx, &members); err != nil {
		return nil, fmt.Errorf("get api token account memberships: %w", err)
	}
	for _, m := range members {
		account := &out[index[m.UserID]]
		account.WorkspaceRole = m.Role
		account.WorkspaceStatus = m.Status
	}

	identities, err := p.ListAuthUserIdentities(ctx, ids)
	if err != nil {
		return nil, err
	}
	for userID, links := range identities {
		account := &out[index[userID]]
		for _, link := range links {
			account.Identities = append(account.Identities, service.APITokenAccountIdentity{
				ProviderID:    link.ProviderID,
				Username:      link.Username,
				DisplayName:   link.DisplayName,
				Email:         link.Email,
				EmailVerified: link.EmailVerified,
			})
		}
	}
	return out, nil
}
