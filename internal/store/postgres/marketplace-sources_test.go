package postgres

import (
	"context"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestMarketplaceSourceRemoteFieldsPostgres(t *testing.T) {
	store := newTestStore(t, nil)
	ctx := context.Background()

	created, err := store.CreateMarketplaceSource(ctx, service.MarketplaceSource{
		Name: "team", Type: service.MarketplaceSourceGit, URL: "https://git.example/team.git",
		Ref: "main", Path: "catalog", CredentialID: "cred-1", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.GetMarketplaceSource(ctx, created.ID)
	if err != nil || got == nil {
		t.Fatalf("get: %+v %v", got, err)
	}
	if got.URL != "https://git.example/team.git" || got.Ref != "main" || got.Path != "catalog" || got.CredentialID != "cred-1" || !got.Enabled {
		t.Fatalf("round trip = %+v", got)
	}

	got.Type, got.URL, got.Ref, got.Path, got.CredentialID = service.MarketplaceSourceURL, "https://x.example/marketplace.json", "", "", ""
	updated, err := store.UpdateMarketplaceSource(ctx, created.ID, *got)
	if err != nil || updated == nil || updated.Type != "url" || updated.URL != "https://x.example/marketplace.json" || updated.CredentialID != "" {
		t.Fatalf("update = %+v %v", updated, err)
	}

	list, err := store.ListMarketplaceSources(ctx)
	if err != nil || len(list) != 1 || list[0].URL != updated.URL {
		t.Fatalf("list = %+v %v", list, err)
	}
}
