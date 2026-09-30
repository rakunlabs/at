package postgres

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

func TestExternalPostgresUsernameAndEmail(t *testing.T) {
	p, provider := externalFixture(t)
	ctx := t.Context()
	complete := func(subject, username, email string, verified bool) (*service.AuthUser, *service.AuthIdentityLink) {
		t.Helper()
		link := externalLink(provider, subject)
		link.Username, link.Email, link.EmailVerified = username, email, verified
		u, l, err := p.CompleteAuthExternalIdentity(ctx, link, provider.Version, nil, time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		return u, l
	}
	first, link := complete("first", "  ada.lovelace  ", "ada@example.test", true)
	if first.Username != "ada.lovelace" || link.Email != "ada@example.test" || !link.EmailVerified {
		t.Fatalf("provider metadata not adopted: %+v %+v", first, link)
	}
	second, _ := complete("second", "ada.lovelace", "ada@example.test", true)
	if second.ID == first.ID || !strings.HasPrefix(second.Username, "ada.lovelace-") {
		t.Fatalf("username/email collision merged accounts: %+v", second)
	}
	local, err := p.CreateAuthUser(ctx, service.AuthUser{Username: "taken", PasswordHash: "hash"}, false)
	if err != nil {
		t.Fatal(err)
	}
	external, _ := complete("third", "taken", "", false)
	if external.ID == local.ID || !strings.HasPrefix(external.Username, "taken-") {
		t.Fatalf("local username collision: %+v", external)
	}
	unnamed, _ := complete("unnamed", "", "", false)
	if unnamed.Username != "external-"+strings.ToLower(unnamed.ID) {
		t.Fatalf("missing fallback: %+v", unnamed)
	}
	same, refreshed := complete("first", "renamed", "new@example.test", false)
	if same.ID != first.ID || same.Username != first.Username || refreshed.Username != "renamed" {
		t.Fatalf("provider rename changed account: %+v %+v", same, refreshed)
	}
	identities, err := p.ListAuthUserIdentities(ctx, []string{first.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(identities[first.ID]) != 1 || identities[first.ID][0].Email != "new@example.test" || identities[first.ID][0].EmailVerified {
		t.Fatalf("unverified email not persisted: %+v", identities)
	}
}

// The display name is stored on first sign-in, refreshed on the next, and is
// searchable, while the account's own username never changes because of it.
func TestExternalPostgresDisplayName(t *testing.T) {
	p, provider := externalFixture(t)
	ctx := t.Context()
	complete := func(name string) (*service.AuthUser, *service.AuthIdentityLink) {
		t.Helper()
		link := externalLink(provider, "person")
		link.Username, link.DisplayName = "ada", name
		u, l, err := p.CompleteAuthExternalIdentity(ctx, link, provider.Version, nil, time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		return u, l
	}
	first, link := complete("  Ada\tLovelace ")
	if link.DisplayName != "Ada Lovelace" {
		t.Fatalf("display name not normalized: %q", link.DisplayName)
	}
	same, refreshed := complete("Ada King")
	if same.ID != first.ID || same.Username != first.Username || refreshed.DisplayName != "Ada King" {
		t.Fatalf("display name refresh changed account or was dropped: %+v %+v", same, refreshed)
	}
	identities, err := p.ListAuthUserIdentities(ctx, []string{first.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := identities[first.ID]; len(got) != 1 || got[0].DisplayName != "Ada King" {
		t.Fatalf("display name not listed: %+v", got)
	}
	page, err := p.ListAuthUsers(ctx, service.AuthUserQuery{Search: "ada king", Limit: 10})
	if err != nil || len(page) != 1 || page[0].ID != first.ID {
		t.Fatalf("display name not searchable: %+v %v", page, err)
	}
}

func TestExternalPostgresUsernameIndependentCeiling(t *testing.T) {
	p, provider := externalFixture(t)
	// No external- prefix and no links: recovery/unlink must not erase origin.
	rows := make([]goqu.Record, 1000)
	for i := range rows {
		rows[i] = goqu.Record{"id": fmt.Sprintf("jit-%d", i), "username": fmt.Sprintf("person-%d", i), "password_hash": "", "externally_provisioned": true}
	}
	if _, err := p.goqu.Insert(p.tableAuthUsers).Rows(rows).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	link := externalLink(provider, "one-too-many")
	link.Username = "new-person"
	if _, _, err := p.CompleteAuthExternalIdentity(t.Context(), link, provider.Version, nil, time.Now().Add(time.Minute)); !errors.Is(err, service.ErrAuthSessionLimit) {
		t.Fatalf("JIT ceiling bypassed by readable usernames: %v", err)
	}
}
