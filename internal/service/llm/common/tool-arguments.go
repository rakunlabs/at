package common

import (
	"encoding/json"
	"fmt"
)

// ParseToolArguments accepts an object (or omitted arguments for no-arg tools),
// never a partial object, array or null that could become an executable call.
func ParseToolArguments(raw string) (map[string]any, error) {
	args := map[string]any{}
	if raw == "" {
		return args, nil
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, fmt.Errorf("invalid JSON object: %w", err)
	}
	if args == nil {
		return nil, fmt.Errorf("expected a JSON object, got null")
	}
	return args, nil
}
