package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func TestCurrentTimeRestrictedDispatch(t *testing.T) {
	ctx := nonHostToolContext(t, "u1", "w1", "r1", "s1", "current_time")
	s := &Server{}
	for _, tc := range []struct {
		name string
		args map[string]any
		zone string
	}{
		{"default", nil, "UTC"},
		{"empty", map[string]any{"timezone": ""}, "UTC"},
		{"utc", map[string]any{"timezone": "UTC"}, "UTC"},
		{"istanbul", map[string]any{"timezone": "Europe/Istanbul"}, "Europe/Istanbul"},
		{"new-york", map[string]any{"timezone": "America/New_York"}, "America/New_York"},
		{"trimmed", map[string]any{"timezone": " Asia/Kolkata "}, "Asia/Kolkata"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := time.Now().UTC()
			raw, err := s.dispatchBuiltinTool(ctx, "current_time", tc.args)
			after := time.Now().UTC()
			if err != nil {
				t.Fatal(err)
			}
			var got currentTimeResult
			if err := json.Unmarshal([]byte(raw), &got); err != nil {
				t.Fatal(err)
			}
			utc, err := time.Parse(time.RFC3339Nano, got.UTC)
			if err != nil {
				t.Fatal(err)
			}
			local, err := time.Parse(time.RFC3339Nano, got.Datetime)
			if err != nil {
				t.Fatal(err)
			}
			loc, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			_, offset := utc.In(loc).Zone()
			if utc.Before(before) || utc.After(after) || !utc.Equal(local) || got.UnixSeconds != utc.Unix() || got.Timezone != tc.zone || got.UTCOffsetSeconds != offset || got.Datetime != utc.In(loc).Format(time.RFC3339Nano) {
				t.Fatalf("inconsistent current time: %+v", got)
			}
		})
	}
}

func TestCurrentTimeRejectsInvalidTimezone(t *testing.T) {
	ctx := nonHostToolContext(t, "u1", "w1", "r1", "s1", "current_time")
	for _, zone := range []any{"Unknown/Zone", "Local", "../etc/passwd", "/etc/localtime", 42, nil, []string{"UTC"}} {
		if _, err := (&Server{}).dispatchBuiltinTool(ctx, "current_time", map[string]any{"timezone": zone}); err == nil {
			t.Fatalf("invalid timezone accepted: %v", zone)
		}
	}
	if _, err := (&Server{}).dispatchBuiltinTool(t.Context(), "current_time", nil); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatalf("unbound current_time admitted: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := (&Server{}).dispatchBuiltinTool(cancelled, "current_time", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled current_time admitted: %v", err)
	}
}
