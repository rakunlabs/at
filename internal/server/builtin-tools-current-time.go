package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata" // Keep IANA zones available in minimal deployment images.
)

type currentTimeResult struct {
	Datetime         string `json:"datetime"`
	UTC              string `json:"utc"`
	Timezone         string `json:"timezone"`
	UnixSeconds      int64  `json:"unix_seconds"`
	UTCOffsetSeconds int    `json:"utc_offset_seconds"`
}

func (s *Server) execCurrentTime(ctx context.Context, args map[string]any) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	zone := "UTC"
	if value, ok := args["timezone"]; ok {
		name, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("timezone must be an IANA timezone string")
		}
		if name = strings.TrimSpace(name); name != "" {
			zone = name
		}
	}
	// Local depends on host configuration, not an explicit user timezone.
	if zone == "Local" {
		return "", fmt.Errorf("timezone must be an explicit IANA name or UTC, not Local")
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return "", fmt.Errorf("invalid timezone %q: %w", zone, err)
	}
	now := time.Now().UTC()
	local := now.In(loc)
	_, offset := local.Zone()
	data, err := json.Marshal(currentTimeResult{
		Datetime: local.Format(time.RFC3339Nano), UTC: now.Format(time.RFC3339Nano),
		Timezone: loc.String(), UnixSeconds: now.Unix(), UTCOffsetSeconds: offset,
	})
	if err != nil {
		return "", fmt.Errorf("marshal current time: %w", err)
	}
	return string(data), nil
}
