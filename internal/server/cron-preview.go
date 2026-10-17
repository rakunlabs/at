package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/worldline-go/hardloop"
)

// PreviewCronAPI calculates occurrences without saving or executing a trigger.
// Use the scheduler's parser and server-local default timezone, including TZ
// prefixes in the expression, rather than a second browser cron implementation.
func (s *Server) PreviewCronAPI(w http.ResponseWriter, r *http.Request) {
	spec := strings.TrimSpace(r.URL.Query().Get("schedule"))
	zone := strings.TrimSpace(r.URL.Query().Get("timezone"))
	if spec == "" || len(spec) > 512 || len(zone) > 128 {
		httpResponse(w, "A cron schedule is required (maximum 512 characters)", http.StatusBadRequest)
		return
	}
	if zone != "" {
		spec = "CRON_TZ=" + zone + " " + spec
	}
	runs, err := cronOccurrences(spec, time.Now(), 5)
	if err != nil {
		httpResponse(w, fmt.Sprintf("Invalid schedule or timezone: %v", err), http.StatusBadRequest)
		return
	}
	httpResponseJSON(w, map[string]any{"runs": runs}, http.StatusOK)
}

func cronOccurrences(spec string, after time.Time, count int) ([]string, error) {
	parsed, err := hardloop.ParseStandard(spec)
	if err != nil {
		return nil, fmt.Errorf("parse cron: %w", err)
	}
	runs := make([]string, 0, count)
	loc := parsed.Location
	if loc == nil || loc == time.Local {
		loc = after.Location()
	}
	for range count {
		next := parsed.Next(after)
		if next.IsZero() {
			break
		}
		runs = append(runs, next.In(loc).Format(time.RFC3339))
		after = next
	}
	return runs, nil
}
