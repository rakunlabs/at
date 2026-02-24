package service

// SanitizeSchema returns a deep copy of the given JSON Schema map with fields
// removed that are not supported by restrictive provider APIs (e.g. Google Gemini).
//
// Gemini's function-calling API only accepts a subset of JSON Schema and rejects
// fields like $schema, additionalProperties, $ref, $defs, etc.  This function
// recursively walks the schema tree and strips those fields so the schema can be
// forwarded without triggering 400 errors.
//
// The returned map is always a fresh copy; the original is never mutated.
func SanitizeSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	return sanitizeMap(schema)
}

// unsupportedKeys lists JSON Schema keywords that Gemini does not accept.
var unsupportedKeys = map[string]struct{}{
	"$schema":              {},
	"additionalProperties": {},
	"$ref":                 {},
	"ref":                  {},
	"$defs":                {},
	"definitions":          {},
}

// sanitizeMap deep-copies a map[string]any while stripping unsupported keys.
func sanitizeMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if _, drop := unsupportedKeys[k]; drop {
			continue
		}
		out[k] = sanitizeValue(v)
	}
	return out
}

// sanitizeValue deep-copies a single value, recursing into maps and slices.
func sanitizeValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return sanitizeMap(val)
	case []any:
		cp := make([]any, len(val))
		for i, item := range val {
			cp[i] = sanitizeValue(item)
		}
		return cp
	default:
		// Primitive types (string, float64, bool, nil) are immutable; return as-is.
		return v
	}
}
