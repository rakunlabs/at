package postgres

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

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
