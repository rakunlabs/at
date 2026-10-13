package postgres

import (
	"errors"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestAgentRuntimeSettingsPostgres(t *testing.T) {
	p := newTestStore(t, nil)

	settings, err := p.GetAgentRuntimeSettings(t.Context())
	if err != nil {
		t.Fatalf("get defaults: %v", err)
	}
	if settings.Version != 1 || settings.MaxBackgroundSubagentsPerOwner != service.DefaultMaxBackgroundSubagentsPerOwner {
		t.Fatalf("unexpected defaults: %+v", settings)
	}

	settings.MaxBackgroundSubagentsPerOwner = 24
	saved, err := p.SaveAgentRuntimeSettings(t.Context(), *settings)
	if err != nil {
		t.Fatalf("save settings: %v", err)
	}
	if saved.Version != 2 || saved.MaxBackgroundSubagentsPerOwner != 24 {
		t.Fatalf("unexpected saved settings: %+v", saved)
	}

	if _, err := p.SaveAgentRuntimeSettings(t.Context(), *settings); !errors.Is(err, service.ErrAgentRuntimeSettingsConflict) {
		t.Fatalf("stale save error = %v", err)
	}
}
