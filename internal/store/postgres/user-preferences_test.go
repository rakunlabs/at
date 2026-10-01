package postgres

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
)

func TestSecretUserPreferenceJSONB(t *testing.T) {
	p := newTestStore(t, bytes.Repeat([]byte{1}, 32))
	ctx := t.Context()
	pref := service.UserPreference{
		UserID: "local-mcp-user", Key: "local_mcp_servers", Secret: true,
		Value: json.RawMessage(`{"servers":[{"name":"laptop","headers":{"Authorization":"Bearer local-secret"}}]}`),
	}
	for _, value := range []json.RawMessage{pref.Value, json.RawMessage(`{"servers":[]}`)} {
		pref.Value = value
		if err := p.SetUserPreference(ctx, pref); err != nil {
			t.Fatalf("save encrypted preference: %v", err)
		}
		var raw string
		if _, err := p.goqu.From(p.tableUserPreferences).Select("value").ScanValContext(ctx, &raw); err != nil {
			t.Fatal(err)
		}
		var ciphertext string
		if err := json.Unmarshal([]byte(raw), &ciphertext); err != nil || !atcrypto.IsEncrypted(ciphertext) || strings.Contains(raw, "local-secret") {
			t.Fatalf("expected JSON-wrapped ciphertext, got %q: %v", raw, err)
		}
		got, err := p.GetUserPreference(ctx, pref.UserID, pref.Key)
		if err != nil || got == nil || !got.Secret || !bytes.Equal(got.Value, value) {
			t.Fatalf("get encrypted preference: %+v, %v", got, err)
		}
		items, err := p.ListUserPreferences(ctx, pref.UserID)
		if err != nil || len(items) != 1 || !bytes.Equal(items[0].Value, value) {
			t.Fatalf("list encrypted preferences: %+v, %v", items, err)
		}
	}
}

func TestUserPrefRowToRecordSecretJSON(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	plain := `{"servers":[]}`
	ciphertext, err := atcrypto.Encrypt(plain, key)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := json.Marshal(ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, stored, want string
		key                []byte
		wantError          bool
	}{
		{name: "encrypted JSON string", stored: string(wrapped), want: plain, key: key},
		{name: "legacy raw ciphertext", stored: ciphertext, want: plain, key: key},
		{name: "plaintext object", stored: plain, want: plain, key: key},
		{name: "plaintext string", stored: `"tr"`, want: `"tr"`, key: key},
		{name: "encryption disabled", stored: plain, want: plain},
		{name: "missing key", stored: string(wrapped), wantError: true},
		{name: "wrong key", stored: string(wrapped), key: bytes.Repeat([]byte{2}, 32), wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := userPrefRowToRecord(userPreferenceRow{Value: tt.stored, Secret: true}, tt.key)
			if tt.wantError {
				if err == nil {
					t.Fatal("expected decryption error")
				}
				return
			}
			if err != nil || string(got.Value) != tt.want {
				t.Fatalf("decode preference: %+v, %v", got, err)
			}
		})
	}
}

func TestPublicUserPreferenceWrite(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := t.Context()
	pref := service.UserPreference{UserID: "preference-user", Key: "language", Value: json.RawMessage(`"tr"`)}
	if err := p.SetPublicUserPreference(ctx, pref); err != nil {
		t.Fatal(err)
	}
	pref.Value = json.RawMessage(`"en"`)
	if err := p.SetPublicUserPreference(ctx, pref); err != nil {
		t.Fatal(err)
	}
	got, err := p.GetUserPreference(ctx, pref.UserID, pref.Key)
	if err != nil || got == nil || got.Secret || string(got.Value) != `"en"` {
		t.Fatalf("updated preference: %+v, %v", got, err)
	}
	pref.Secret = true
	pref.Value = json.RawMessage(`"secret"`)
	if err := p.SetUserPreference(ctx, pref); err != nil {
		t.Fatal(err)
	}
	pref.Secret = false
	pref.Value = json.RawMessage(`"overwrite"`)
	if err := p.SetPublicUserPreference(ctx, pref); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("secret overwrite: %v", err)
	}
	got, err = p.GetUserPreference(ctx, pref.UserID, pref.Key)
	if err != nil || got == nil || !got.Secret || string(got.Value) != `"secret"` {
		t.Fatalf("secret changed: %+v, %v", got, err)
	}
	pref.UserID = "another-user"
	if err := p.SetPublicUserPreference(ctx, pref); err != nil {
		t.Fatal(err)
	}
	pref.Secret = true
	if err := p.SetPublicUserPreference(ctx, pref); err == nil {
		t.Fatal("public writer accepted secret payload")
	}
}
