package postgres

import (
	"context"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestConnector_CRUD(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, nil)

	in := service.Connector{
		Slug:        "spotify",
		Name:        "Spotify",
		Description: "Music",
		Icon:        "Music",
		AuthKind:    service.ConnectorAuthOAuth2,
		OAuth: &service.ConnectorOAuth{
			AuthURL:          "https://accounts.spotify.com/authorize",
			TokenURL:         "https://accounts.spotify.com/api/token",
			Scopes:           []string{"user-read-email"},
			UsePKCE:          true,
			UserinfoURL:      "https://api.spotify.com/v1/me",
			AccountLabelPath: "email",
		},
		Fields: []service.ConnectorField{
			{Key: "spotify_client_id", Label: "Client ID", Type: "text", Required: true},
			{Key: "spotify_client_secret", Label: "Client Secret", Type: "secret"},
		},
		CreatedBy: "tester",
		UpdatedBy: "tester",
	}

	created, err := store.CreateConnector(ctx, in)
	if err != nil {
		t.Fatalf("CreateConnector: %v", err)
	}
	if created.Slug != "spotify" {
		t.Fatalf("slug: got %q", created.Slug)
	}

	got, err := store.GetConnector(ctx, "spotify")
	if err != nil {
		t.Fatalf("GetConnector: %v", err)
	}
	if got == nil {
		t.Fatal("GetConnector: nil")
	}
	if got.OAuth == nil || !got.OAuth.UsePKCE {
		t.Errorf("oauth/pkce not round-tripped: %+v", got.OAuth)
	}
	if got.OAuth.AccountLabelPath != "email" {
		t.Errorf("account_label_path: got %q", got.OAuth.AccountLabelPath)
	}
	if len(got.Fields) != 2 || got.Fields[0].Key != "spotify_client_id" {
		t.Errorf("fields not round-tripped: %+v", got.Fields)
	}

	// Update auth kind / fields.
	got.Name = "Spotify Music"
	got.Fields = append(got.Fields, service.ConnectorField{Key: "spotify_extra", Type: "secret"})
	if _, err := store.UpdateConnector(ctx, "spotify", *got); err != nil {
		t.Fatalf("UpdateConnector: %v", err)
	}
	updated, _ := store.GetConnector(ctx, "spotify")
	if updated.Name != "Spotify Music" || len(updated.Fields) != 3 {
		t.Errorf("update not applied: name=%q fields=%d", updated.Name, len(updated.Fields))
	}

	// List.
	list, err := store.ListConnectors(ctx, nil)
	if err != nil {
		t.Fatalf("ListConnectors: %v", err)
	}
	if list == nil || len(list.Data) != 1 {
		t.Fatalf("expected 1 connector, got %d", len(list.Data))
	}

	// Delete.
	if err := store.DeleteConnector(ctx, "spotify"); err != nil {
		t.Fatalf("DeleteConnector: %v", err)
	}
	gone, _ := store.GetConnector(ctx, "spotify")
	if gone != nil {
		t.Error("expected connector to be deleted")
	}
}

func TestConnector_TokenKindNoOAuth(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, nil)

	_, err := store.CreateConnector(ctx, service.Connector{
		Slug:     "pexels",
		Name:     "Pexels",
		AuthKind: service.ConnectorAuthToken,
		Fields: []service.ConnectorField{
			{Key: "pexels_api_key", Type: "secret", Required: true},
		},
	})
	if err != nil {
		t.Fatalf("CreateConnector: %v", err)
	}

	got, err := store.GetConnector(ctx, "pexels")
	if err != nil {
		t.Fatalf("GetConnector: %v", err)
	}
	if got.OAuth != nil {
		t.Errorf("expected nil OAuth for token connector, got %+v", got.OAuth)
	}
	if got.AuthKind != service.ConnectorAuthToken {
		t.Errorf("auth_kind: got %q", got.AuthKind)
	}
}
