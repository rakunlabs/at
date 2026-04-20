package sqlite3

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rakunlabs/at/internal/config"
	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
)

func newTestStore(t *testing.T, encKey []byte) *SQLite {
	t.Helper()
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "test.sqlite") + "?cache=shared"

	cfg := &config.StoreSQLite{
		Datasource: dsn,
		Migrate: config.Migrate{
			Datasource: dsn,
		},
	}

	store, err := New(context.Background(), cfg, encKey)
	if err != nil {
		t.Fatalf("sqlite3.New: %v", err)
	}
	t.Cleanup(store.Close)
	return store
}

func TestConnection_EncryptionRoundTrip(t *testing.T) {
	ctx := context.Background()
	key, err := atcrypto.DeriveKey("test-passphrase")
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	store := newTestStore(t, key)

	c := service.Connection{
		Provider:     "youtube",
		Name:         "Main Channel",
		AccountLabel: "@rakunlabs",
		Description:  "Primary channel",
		Credentials: service.ConnectionCredentials{
			ClientID:     "abc.apps.googleusercontent.com",
			ClientSecret: "GOCSPX-secret",
			RefreshToken: "1//0g-refresh",
		},
		Metadata:  map[string]any{"scopes": []any{"youtube.upload"}},
		CreatedBy: "tester",
		UpdatedBy: "tester",
	}

	created, err := store.CreateConnection(ctx, c)
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}

	// Read back: credentials must decrypt identically.
	fetched, err := store.GetConnection(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetConnection: %v", err)
	}
	if fetched == nil {
		t.Fatal("GetConnection: nil")
	}
	if fetched.Credentials.RefreshToken != "1//0g-refresh" {
		t.Errorf("RefreshToken: got %q, want %q", fetched.Credentials.RefreshToken, "1//0g-refresh")
	}
	if fetched.Credentials.ClientSecret != "GOCSPX-secret" {
		t.Errorf("ClientSecret: got %q, want %q", fetched.Credentials.ClientSecret, "GOCSPX-secret")
	}

	// Raw read of the credentials column must be encrypted (enc: prefix).
	var raw string
	row := store.db.QueryRowContext(ctx, "SELECT credentials FROM at_connections WHERE id = ?", created.ID)
	if err := row.Scan(&raw); err != nil {
		t.Fatalf("scan raw: %v", err)
	}
	if !atcrypto.IsEncrypted(raw) {
		t.Errorf("stored credentials are not encrypted: %q", raw)
	}
}

func TestConnection_UniqueProviderName(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, nil)

	_, err := store.CreateConnection(ctx, service.Connection{
		Provider: "youtube",
		Name:     "Main",
		Credentials: service.ConnectionCredentials{
			RefreshToken: "r1",
		},
	})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Duplicate name for same provider should fail (UNIQUE constraint).
	_, err = store.CreateConnection(ctx, service.Connection{
		Provider: "youtube",
		Name:     "Main",
		Credentials: service.ConnectionCredentials{
			RefreshToken: "r2",
		},
	})
	if err == nil {
		t.Error("expected unique constraint violation on duplicate (provider, name)")
	}

	// Same name under a different provider should succeed.
	_, err = store.CreateConnection(ctx, service.Connection{
		Provider: "google",
		Name:     "Main",
		Credentials: service.ConnectionCredentials{
			RefreshToken: "r3",
		},
	})
	if err != nil {
		t.Errorf("expected success for different provider, got: %v", err)
	}
}

func TestConnection_ListByProvider(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, nil)

	for _, n := range []string{"Main", "Backup", "Client B"} {
		if _, err := store.CreateConnection(ctx, service.Connection{
			Provider: "youtube",
			Name:     n,
			Credentials: service.ConnectionCredentials{
				RefreshToken: "r-" + n,
			},
		}); err != nil {
			t.Fatalf("create %q: %v", n, err)
		}
	}
	if _, err := store.CreateConnection(ctx, service.Connection{
		Provider: "google",
		Name:     "Main",
		Credentials: service.ConnectionCredentials{
			RefreshToken: "rg",
		},
	}); err != nil {
		t.Fatalf("create google: %v", err)
	}

	list, err := store.ListConnectionsByProvider(ctx, "youtube")
	if err != nil {
		t.Fatalf("ListConnectionsByProvider: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 youtube connections, got %d", len(list))
	}
	// Sorted by name ascending: Backup, Client B, Main.
	if list[0].Name != "Backup" || list[1].Name != "Client B" || list[2].Name != "Main" {
		t.Errorf("unexpected order: %v, %v, %v", list[0].Name, list[1].Name, list[2].Name)
	}
}
