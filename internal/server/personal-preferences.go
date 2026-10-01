package server

import (
	"fmt"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// builtinPersonalPreferenceKey keeps model-visible preferences separate from
// application settings, credential records and future preference consumers.
func builtinPersonalPreferenceKey(key string) bool {
	switch key {
	case "timezone", "location", "language":
		return true
	default:
		return false
	}
}

// personalUserPreferences is shared by tools and automatic prompt injection.
func personalUserPreferences(prefs []service.UserPreference) []service.UserPreference {
	var personal []service.UserPreference
	for _, pref := range prefs {
		if !pref.Secret && builtinPersonalPreferenceKey(pref.Key) {
			personal = append(personal, pref)
		}
	}
	return personal
}

func appendPersonalPreferencesPrompt(prompt string, prefs []service.UserPreference) string {
	var lines []string
	for _, pref := range personalUserPreferences(prefs) {
		lines = append(lines, fmt.Sprintf("- %s: %s", pref.Key, string(pref.Value)))
	}
	if len(lines) == 0 {
		return prompt
	}
	if prompt != "" {
		prompt += "\n\n"
	}
	return prompt + "User preferences:\n" + strings.Join(lines, "\n")
}
