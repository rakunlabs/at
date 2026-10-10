package postgres

import (
	"errors"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestSystemSettingsPostgres(t *testing.T) {
	p, ctx, w, _ := workspaceFixture(t)
	if stored, err := p.GetSystemSettings(ctx); err != nil || stored != nil {
		t.Fatalf("initial settings: %+v %v", stored, err)
	}
	s := service.DefaultSystemSettings()
	s.Name = "Installation"
	saved, err := p.SaveSystemSettings(ctx, s)
	if err != nil || saved.Version != 1 {
		t.Fatalf("first save: %+v %v", saved, err)
	}
	if _, err := p.SaveSystemSettings(ctx, s); !errors.Is(err, service.ErrSystemSettingsConflict) {
		t.Fatalf("stale insert: %v", err)
	}
	saved.LogLevel = "debug"
	updated, err := p.SaveSystemSettings(ctx, *saved)
	if err != nil || updated.Version != 2 {
		t.Fatalf("update: %+v %v", updated, err)
	}
	if _, err := p.SaveSystemSettings(ctx, *saved); !errors.Is(err, service.ErrSystemSettingsConflict) {
		t.Fatalf("stale update: %v", err)
	}
	reloaded, err := p.GetSystemSettings(service.WithExecutionMaintenance(t.Context()))
	if err != nil || reloaded.Name != "Installation" || reloaded.LogLevel != "debug" {
		t.Fatalf("startup read: %+v %v", reloaded, err)
	}
	user := workspaceUser(t, p, "system-settings-member")
	member := workspaceMember(t, p, ctx, w.ID, user, "admin")
	if _, err := p.GetSystemSettings(member); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("workspace admin read: %v", err)
	}
	if _, err := p.SaveSystemSettings(member, *updated); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("workspace admin write: %v", err)
	}
}
