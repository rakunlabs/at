package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCronOccurrences(t *testing.T) {
	after := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name, spec, first, second string
	}{
		{"minutes", "*/15 * * * *", "2026-03-07T12:15:00Z", "2026-03-07T12:30:00Z"},
		{"timezone", "CRON_TZ=Europe/Istanbul 0 9 * * *", "2026-03-08T09:00:00+03:00", "2026-03-09T09:00:00+03:00"},
		{"DST", "CRON_TZ=America/New_York 0 9 * * *", "2026-03-07T09:00:00-05:00", "2026-03-08T09:00:00-04:00"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runs, err := cronOccurrences(tt.spec, after, 5)
			if err != nil || len(runs) != 5 {
				t.Fatalf("runs = %v, err = %v", runs, err)
			}
			if runs[0] != tt.first || runs[1] != tt.second {
				t.Fatalf("unexpected occurrences: %v", runs)
			}
		})
	}
	runs, err := cronOccurrences("0 0 31 2 *", after, 5)
	if err != nil || len(runs) != 0 {
		t.Fatalf("impossible date: %v, %v", runs, err)
	}
}

func TestPreviewCronAPI(t *testing.T) {
	for _, tt := range []struct {
		query  string
		status int
	}{
		{"schedule=*/5+*+*+*+*", http.StatusOK},
		{"", http.StatusBadRequest},
		{"schedule=invalid", http.StatusBadRequest},
		{"schedule=*+*+*+*+*&timezone=Not/AZone", http.StatusBadRequest},
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/triggers/cron-preview?"+tt.query, nil)
		w := httptest.NewRecorder()
		(&Server{}).PreviewCronAPI(w, r)
		if w.Code != tt.status {
			t.Errorf("%s: status = %d, body = %s", tt.query, w.Code, w.Body.String())
		}
	}
}
