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
//
// Gemini's function-calling schema is a subset of OpenAPI 3.0, not full JSON
// Schema. Besides the structural keywords ($ref/$defs/additionalProperties/…)
// it also rejects the numeric/validation keywords below with errors like
// `Unknown name "exclusiveMinimum" ... Cannot find field`. These commonly leak
// in from Pydantic (Field(gt=…) → exclusiveMinimum, multiple_of → multipleOf)
// or Zod-generated schemas. Stripping them only drops a validation hint; the
// property type itself is preserved.
var unsupportedKeys = map[string]struct{}{
	"$schema":              {},
	"additionalProperties": {},
	"$ref":                 {},
	"ref":                  {},
	"$defs":                {},
	"definitions":          {},
	// Numeric / value validation keywords unsupported by Gemini.
	"exclusiveMinimum": {},
	"exclusiveMaximum": {},
	"multipleOf":       {},
	"const":            {},
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

// SanitizeSchemaForGemini returns a deep copy of the given JSON Schema map kept
// strictly to the subset of OpenAPI 3.0 that Google Gemini's function-calling /
// responseSchema API accepts. Unlike SanitizeSchema (a denylist that only drops
// a handful of known-bad keys), this is an ALLOWLIST: any keyword Gemini does
// not understand is dropped, because Gemini validates field names strictly and
// returns `400 Unknown name "<x>" ... Cannot find field` for anything else.
//
// Beyond field filtering it also normalises three shapes Gemini rejects:
//   - `type: ["string","null"]` (array/union type)  → `type:"string"` + `nullable:true`
//   - `format` values outside Gemini's set          → dropped (keeps the property)
//   - `oneOf`                                        → remapped to `anyOf`
//     (`allOf` / `not` are simply dropped — Gemini has no equivalent)
//
// The returned map is always a fresh copy; the original is never mutated.
func SanitizeSchemaForGemini(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	return sanitizeGeminiMap(schema)
}

// geminiAllowedKeys is the set of JSON Schema keywords Gemini's Schema type
// understands. `type`, `format`, `properties`, `items`, `anyOf`/`oneOf` are
// handled explicitly (not via this set) because they need transformation or
// recursion.
var geminiAllowedKeys = map[string]struct{}{
	"title":            {},
	"description":      {},
	"nullable":         {},
	"default":          {},
	"enum":             {},
	"required":         {},
	"minItems":         {},
	"maxItems":         {},
	"minProperties":    {},
	"maxProperties":    {},
	"minLength":        {},
	"maxLength":        {},
	"pattern":          {},
	"minimum":          {},
	"maximum":          {},
	"example":          {},
	"propertyOrdering": {},
}

// geminiAllowedFormats lists the only `format` values Gemini accepts. STRING
// supports enum/date-time; NUMBER/INTEGER support the numeric widths. Anything
// else (email, uri, uuid, date, ipv4, byte, binary, …) is dropped.
var geminiAllowedFormats = map[string]struct{}{
	"enum":      {},
	"date-time": {},
	"int32":     {},
	"int64":     {},
	"float":     {},
	"double":    {},
}

// sanitizeGeminiMap deep-copies a schema node keeping only Gemini-safe keys.
func sanitizeGeminiMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))

	for k, v := range m {
		switch k {
		case "type":
			if t, nullable := normalizeGeminiType(v); t != nil {
				out["type"] = t
				if nullable {
					out["nullable"] = true
				}
			}
		case "format":
			if s, ok := v.(string); ok {
				if _, allowed := geminiAllowedFormats[s]; allowed {
					out["format"] = s
				}
			}
		case "properties":
			if props, ok := v.(map[string]any); ok {
				out["properties"] = sanitizeGeminiProperties(props)
			}
		case "items":
			if items := sanitizeGeminiItems(v); items != nil {
				out["items"] = items
			}
		case "anyOf", "oneOf":
			// Gemini supports only anyOf; fold oneOf into it.
			if arr, ok := v.([]any); ok {
				san := sanitizeGeminiSchemaArray(arr)
				if existing, ok := out["anyOf"].([]any); ok {
					out["anyOf"] = append(existing, san...)
				} else {
					out["anyOf"] = san
				}
			}
		default:
			// Keep only explicitly-allowed leaf keys; drop everything else
			// (allOf, not, $ref, $defs, additionalProperties, exclusiveMinimum,
			// multipleOf, const, uniqueItems, if/then/else, examples, …).
			if _, ok := geminiAllowedKeys[k]; ok {
				out[k] = deepCopyValue(v)
			}
		}
	}

	collapseGeminiAnyOf(out)
	pruneEmptyProperties(out)
	pruneRequired(out)
	return out
}

// normalizeGeminiType collapses a JSON Schema `type` into Gemini's single-value
// form. An array type (union) yields the first non-null member plus a nullable
// flag; a `"null"`-only type yields (nil, true) so the property is dropped.
func normalizeGeminiType(v any) (any, bool) {
	switch t := v.(type) {
	case string:
		return t, false
	case []any:
		var picked string
		nullable := false
		for _, item := range t {
			s, ok := item.(string)
			if !ok {
				continue
			}
			if s == "null" {
				nullable = true
				continue
			}
			if picked == "" {
				picked = s
			}
		}
		if picked == "" {
			return nil, nullable
		}
		return picked, nullable
	default:
		return v, false
	}
}

// collapseGeminiAnyOf normalises the `anyOf` construct into a shape Gemini's
// strict validator accepts. MCP clients (e.g. opencode) commonly express a
// nullable array field coming off the wire as `type: ["null","array"]` by
// splitting it into `anyOf: [{"type":"array"}] + nullable: true` while leaving
// `items` on the PARENT. Gemini then rejects it twice:
//
//   - parent has `items` but its own `$type` isn't ARRAY
//   - the `anyOf[0]` array branch is missing its `items`
//
// This walks the (already-sanitized) `anyOf`:
//   - a pure-null / empty branch is dropped and records `nullable: true`
//   - if exactly one real branch remains, it is MERGED UP into the parent
//     (parent keys win; the branch fills in the missing `type`/`items`), and
//     `anyOf` is removed — turning the split shape back into a valid single
//     array/object schema
//   - if several real branches remain (a genuine union) `anyOf` is kept with
//     the null branch stripped and `nullable` recorded instead
func collapseGeminiAnyOf(out map[string]any) {
	raw, ok := out["anyOf"].([]any)
	if !ok {
		return
	}

	var real []map[string]any
	nullable := false
	for _, b := range raw {
		m, ok := b.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); t == "null" {
			nullable = true
			continue
		}
		if len(m) == 0 {
			continue
		}
		real = append(real, m)
	}

	if nullable {
		out["nullable"] = true
	}

	switch len(real) {
	case 0:
		delete(out, "anyOf")
	case 1:
		delete(out, "anyOf")
		for k, v := range real[0] {
			if _, exists := out[k]; !exists {
				out[k] = v
			}
		}
	default:
		branches := make([]any, len(real))
		for i, m := range real {
			branches[i] = m
		}
		out["anyOf"] = branches
	}
}

// sanitizeGeminiProperties recurses into a properties map.
func sanitizeGeminiProperties(props map[string]any) map[string]any {
	out := make(map[string]any, len(props))
	for name, v := range props {
		if sub, ok := v.(map[string]any); ok {
			out[name] = sanitizeGeminiMap(sub)
		}
		// Non-map property definitions are invalid for Gemini — drop them.
	}
	return out
}

// sanitizeGeminiItems recurses into an `items` schema. Tuple-form items
// ([]schema) are collapsed to their first element, since Gemini arrays take a
// single item schema. Returns nil when no usable schema is present.
func sanitizeGeminiItems(v any) map[string]any {
	switch it := v.(type) {
	case map[string]any:
		return sanitizeGeminiMap(it)
	case []any:
		for _, item := range it {
			if sub, ok := item.(map[string]any); ok {
				return sanitizeGeminiMap(sub)
			}
		}
	}
	return nil
}

// sanitizeGeminiSchemaArray recurses into an array of sub-schemas (anyOf/oneOf).
func sanitizeGeminiSchemaArray(arr []any) []any {
	out := make([]any, 0, len(arr))
	for _, item := range arr {
		if sub, ok := item.(map[string]any); ok {
			out = append(out, sanitizeGeminiMap(sub))
		}
	}
	return out
}

// deepCopyValue deep-copies an arbitrary JSON value verbatim (no schema
// filtering) — used for leaf keywords like `default`/`enum` whose contents are
// data, not schema, and must survive untouched.
func deepCopyValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		cp := make(map[string]any, len(val))
		for k, x := range val {
			cp[k] = deepCopyValue(x)
		}
		return cp
	case []any:
		cp := make([]any, len(val))
		for i, x := range val {
			cp[i] = deepCopyValue(x)
		}
		return cp
	default:
		return v
	}
}
