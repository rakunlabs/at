package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// scheduleTestStore serves cron triggers and their execution bindings.
type scheduleTestStore struct {
	service.TriggerStorer
	triggers map[string]service.Trigger
	bound    []string
}

func (s *scheduleTestStore) GetTrigger(_ context.Context, id string) (*service.Trigger, error) {
	t, ok := s.triggers[id]
	if !ok {
		return nil, nil
	}
	return &t, nil
}

func (s *scheduleTestStore) ListExecutionServiceBindings(context.Context, string) ([]service.ExecutionServiceBinding, error) {
	out := make([]service.ExecutionServiceBinding, 0, len(s.bound))
	for _, id := range s.bound {
		out = append(out, service.ExecutionServiceBinding{Kind: "trigger", SubjectID: id})
	}
	return out, nil
}

func TestSchedulerScheduleOfReportsLoadedTriggers(t *testing.T) {
	cron := func(id, spec string) service.Trigger {
		return service.Trigger{ID: id, Type: "cron", Enabled: true, Config: map[string]any{"schedule": spec, "timezone": "Europe/Amsterdam"}}
	}
	store := &scheduleTestStore{triggers: map[string]service.Trigger{
		"bound":   cron("bound", "0 10 * * 1,3,6"),
		"unbound": cron("unbound", "0 7 * * *"),
		"invalid": cron("invalid", "not a cron"),
	}, bound: []string{"bound", "invalid"}}

	s := &Scheduler{triggerStore: store}
	if got := s.ScheduleOf("bound"); got.Active || got.Scheduled {
		t.Fatalf("before start: %+v", got)
	}

	s.SetExecutionContext(func(ctx context.Context, id string) (context.Context, error) {
		for _, b := range store.bound {
			if b == id {
				return ctx, nil
			}
		}
		return nil, errors.New("no binding")
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Stop()

	bound := s.ScheduleOf("bound")
	if !bound.Active || !bound.Scheduled || bound.NextRun.IsZero() {
		t.Fatalf("bound trigger not reported as scheduled: %+v", bound)
	}
	if h := bound.NextRun.In(mustLocation(t, "Europe/Amsterdam")).Hour(); h != 10 {
		t.Fatalf("next run hour = %d, want 10 Amsterdam", h)
	}
	// An enabled trigger without a binding is the case the UI showed as
	// Active while it never ran.
	if got := s.ScheduleOf("unbound"); !got.Active || got.Scheduled {
		t.Fatalf("unbound trigger: %+v", got)
	}
	// An invalid spec is skipped without stopping the other schedules.
	if got := s.ScheduleOf("invalid"); got.Scheduled {
		t.Fatalf("invalid trigger: %+v", got)
	}

	// Binding the trigger and reloading schedules it.
	store.bound = append(store.bound, "unbound")
	if err := s.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := s.ScheduleOf("unbound"); !got.Scheduled {
		t.Fatalf("after binding: %+v", got)
	}

	s.Stop()
	if got := s.ScheduleOf("bound"); got.Active || got.Scheduled {
		t.Fatalf("after stop: %+v", got)
	}
}

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load location %s: %v", name, err)
	}
	return loc
}
