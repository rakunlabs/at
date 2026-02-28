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

	// Post-processing: clean up properties and required fields.
	// Gemini validates strictly and returns 400 if a required field references a
	// property that is missing or whose definition is empty/invalid (e.g. a
	// property originally defined only via $ref that became {} after stripping).
	pruneEmptyProperties(out)
	pruneRequired(out)

	return out
}

// pruneEmptyProperties removes entries from the "properties" map whose schema
// definition became empty or has no "type" after sanitization. This happens
// when a property was defined solely via $ref or other unsupported keywords
// that got stripped, leaving an empty or incomplete schema object like {}.
// Gemini considers such properties "not defined" and rejects required fields
// that reference them.
func pruneEmptyProperties(out map[string]any) {
	props, ok := out["properties"].(map[string]any)
	if !ok {
		return
	}
	for name, v := range props {
		propSchema, ok := v.(map[string]any)
		if !ok {
			// Non-map property definition — invalid, remove it.
			delete(props, name)
			continue
		}
		if len(propSchema) == 0 {
			// Empty object after sanitization — remove it.
			delete(props, name)
			continue
		}
		// A property with no "type" and no structural keywords (anyOf, oneOf,
		// allOf, enum, items, properties) is considered incomplete by Gemini.
		if !hasValidSchemaType(propSchema) {
			delete(props, name)
		}
	}
}

// hasValidSchemaType reports whether a schema map contains enough type
// information for Gemini to consider it a valid property definition.
func hasValidSchemaType(schema map[string]any) bool {
	if _, ok := schema["type"]; ok {
		return true
	}
	// Structural composition keywords are valid without an explicit type.
	for _, key := range []string{"anyOf", "oneOf", "allOf", "enum", "items", "properties"} {
		if _, ok := schema[key]; ok {
			return true
		}
	}
	return false
}

// pruneRequired removes entries from the "required" array that have no
// corresponding key in "properties". Handles both []any (from json.Unmarshal)
// and []string (from programmatic construction).
func pruneRequired(out map[string]any) {
	raw, exists := out["required"]
	if !exists {
		return
	}

	props, _ := out["properties"].(map[string]any) // might be nil if no properties

	// Collect required field names from either []any or []string.
	var names []string
	switch reqs := raw.(type) {
	case []any:
		for _, r := range reqs {
			if s, ok := r.(string); ok {
				names = append(names, s)
			}
		}
	case []string:
		names = reqs
	default:
		// Unrecognised type — drop entirely to be safe.
		delete(out, "required")
		return
	}

	var valid []any
	for _, name := range names {
		if _, exists := props[name]; exists {
			valid = append(valid, name)
		}
	}
	if len(valid) > 0 {
		out["required"] = valid
	} else {
		delete(out, "required")
	}
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
