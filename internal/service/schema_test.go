package service

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSanitizeSchema_Nil(t *testing.T) {
	if got := SanitizeSchema(nil); got != nil {
		t.Errorf("SanitizeSchema(nil) = %v, want nil", got)
	}
}

func TestSanitizeSchema_StripsUnsupportedKeys(t *testing.T) {
	input := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "The name",
			},
		},
		"required": []any{"name"},
	}

	got := SanitizeSchema(input)

	if _, ok := got["$schema"]; ok {
		t.Error("expected $schema to be removed")
	}
	if _, ok := got["additionalProperties"]; ok {
		t.Error("expected additionalProperties to be removed")
	}
	if got["type"] != "object" {
		t.Errorf("expected type=object, got %v", got["type"])
	}
	props := got["properties"].(map[string]any)
	nameSchema := props["name"].(map[string]any)
	if nameSchema["type"] != "string" {
		t.Errorf("expected nested type=string, got %v", nameSchema["type"])
	}
}

func TestSanitizeSchema_RecursiveNested(t *testing.T) {
	input := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"properties": map[string]any{
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"ref":                  "SomeRef",
					"properties": map[string]any{
						"id": map[string]any{
							"type":                 "integer",
							"additionalProperties": true,
						},
					},
				},
			},
		},
	}

	got := SanitizeSchema(input)

	// Check top level
	if _, ok := got["$schema"]; ok {
		t.Error("top-level $schema should be removed")
	}

	// Check nested items
	props := got["properties"].(map[string]any)
	itemsProp := props["items"].(map[string]any)
	itemsSchema := itemsProp["items"].(map[string]any)

	if _, ok := itemsSchema["additionalProperties"]; ok {
		t.Error("nested additionalProperties should be removed")
	}
	if _, ok := itemsSchema["ref"]; ok {
		t.Error("nested ref should be removed")
	}

	// Check deeply nested
	idSchema := itemsSchema["properties"].(map[string]any)["id"].(map[string]any)
	if _, ok := idSchema["additionalProperties"]; ok {
		t.Error("deeply nested additionalProperties should be removed")
	}
	if idSchema["type"] != "integer" {
		t.Errorf("deeply nested type should be integer, got %v", idSchema["type"])
	}
}

func TestSanitizeSchema_AnyOfOneOfAllOf(t *testing.T) {
	input := map[string]any{
		"anyOf": []any{
			map[string]any{
				"type":                 "string",
				"additionalProperties": false,
			},
			map[string]any{
				"type":    "integer",
				"$schema": "foo",
			},
		},
	}

	got := SanitizeSchema(input)
	arr := got["anyOf"].([]any)

	first := arr[0].(map[string]any)
	if _, ok := first["additionalProperties"]; ok {
		t.Error("additionalProperties in anyOf[0] should be removed")
	}
	if first["type"] != "string" {
		t.Errorf("anyOf[0] type should be string, got %v", first["type"])
	}

	second := arr[1].(map[string]any)
	if _, ok := second["$schema"]; ok {
		t.Error("$schema in anyOf[1] should be removed")
	}
}

func TestSanitizeSchema_DoesNotMutateOriginal(t *testing.T) {
	input := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"name": map[string]any{
				"type":                 "string",
				"additionalProperties": true,
			},
		},
	}

	// Deep copy input for comparison
	original := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"name": map[string]any{
				"type":                 "string",
				"additionalProperties": true,
			},
		},
	}

	_ = SanitizeSchema(input)

	// Verify input was not mutated
	if !reflect.DeepEqual(input, original) {
		t.Error("SanitizeSchema mutated the original input")
	}
}

func TestSanitizeSchema_RemovesMissingRequiredFields(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"a": map[string]any{"type": "string"},
		},
		"required": []any{"a", "b"}, // "b" is missing from properties
	}

	got := SanitizeSchema(input)
	req, ok := got["required"].([]any)
	if !ok {
		t.Fatal("expected required to be []any")
	}

	if len(req) != 1 {
		t.Errorf("expected 1 required field, got %d", len(req))
	}
	if len(req) > 0 && req[0] != "a" {
		t.Errorf("expected required field 'a', got %v", req[0])
	}
}

func TestSanitizeSchema_RemovesAllMissingRequiredFields(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"a": map[string]any{"type": "string"},
		},
		"required": []any{"b", "c"},
	}

	got := SanitizeSchema(input)
	req, ok := got["required"].([]any)
	// Expect required key to be REMOVED if empty
	if ok {
		// If it's present, it MUST be empty
		if len(req) != 0 {
			t.Errorf("expected 0 required fields or key removed, got %d: %v", len(req), req)
		}
	}
}

func TestSanitizeSchema_RequiredAsStringSlice(t *testing.T) {
	// Programmatically constructed schemas may use []string instead of []any.
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"a": map[string]any{"type": "string"},
			"b": map[string]any{"type": "integer"},
		},
		"required": []string{"a", "b", "c"}, // "c" does not exist in properties
	}

	got := SanitizeSchema(input)
	req, ok := got["required"].([]any)
	if !ok {
		t.Fatal("expected required to be []any after sanitization")
	}
	if len(req) != 2 {
		t.Fatalf("expected 2 required fields, got %d: %v", len(req), req)
	}
	if req[0] != "a" || req[1] != "b" {
		t.Errorf("expected required [a, b], got %v", req)
	}
}

func TestSanitizeSchema_RequiredAsStringSliceAllMissing(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"a": map[string]any{"type": "string"},
		},
		"required": []string{"x", "y"},
	}

	got := SanitizeSchema(input)
	if _, ok := got["required"]; ok {
		t.Error("expected required key to be removed when all entries are missing from properties")
	}
}

func TestSanitizeSchema_NestedRequiredPruning(t *testing.T) {
	// Nested object properties should also have their required fields pruned.
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"outer": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"inner_a": map[string]any{"type": "string"},
				},
				"required": []any{"inner_a", "inner_b"}, // inner_b missing
			},
		},
		"required": []any{"outer"},
	}

	got := SanitizeSchema(input)

	// Top-level required should be intact.
	topReq := got["required"].([]any)
	if len(topReq) != 1 || topReq[0] != "outer" {
		t.Errorf("expected top-level required [outer], got %v", topReq)
	}

	// Nested required should be pruned to only inner_a.
	outerSchema := got["properties"].(map[string]any)["outer"].(map[string]any)
	nestedReq, ok := outerSchema["required"].([]any)
	if !ok {
		t.Fatal("expected nested required to be []any")
	}
	if len(nestedReq) != 1 || nestedReq[0] != "inner_a" {
		t.Errorf("expected nested required [inner_a], got %v", nestedReq)
	}
}

func TestSanitizeSchema_PrunesEmptyPropertiesFromRef(t *testing.T) {
	// A property defined solely via $ref becomes {} after sanitization.
	// It should be removed from properties and from required.
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
			"address": map[string]any{
				"$ref": "#/$defs/Address",
			},
		},
		"required": []any{"name", "address"},
		"$defs": map[string]any{
			"Address": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"street": map[string]any{"type": "string"},
				},
			},
		},
	}

	got := SanitizeSchema(input)

	// $defs should be stripped.
	if _, ok := got["$defs"]; ok {
		t.Error("expected $defs to be removed")
	}

	// "address" property had only $ref, so after stripping it becomes {}.
	// It should be removed from properties.
	props := got["properties"].(map[string]any)
	if _, ok := props["address"]; ok {
		t.Error("expected 'address' property to be removed (was only $ref)")
	}
	if _, ok := props["name"]; !ok {
		t.Error("expected 'name' property to be kept")
	}

	// required should only contain "name" now.
	req, ok := got["required"].([]any)
	if !ok {
		t.Fatal("expected required to be []any")
	}
	if len(req) != 1 || req[0] != "name" {
		t.Errorf("expected required [name], got %v", req)
	}
}

func TestSanitizeSchema_PrunesPropertyWithOnlyAdditionalProperties(t *testing.T) {
	// A property defined only with additionalProperties (which gets stripped)
	// and no type should be removed.
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"valid": map[string]any{"type": "string"},
			"invalid": map[string]any{
				"additionalProperties": map[string]any{"type": "string"},
			},
		},
		"required": []any{"valid", "invalid"},
	}

	got := SanitizeSchema(input)

	props := got["properties"].(map[string]any)
	if _, ok := props["invalid"]; ok {
		t.Error("expected 'invalid' property to be removed (had only additionalProperties)")
	}

	req := got["required"].([]any)
	if len(req) != 1 || req[0] != "valid" {
		t.Errorf("expected required [valid], got %v", req)
	}
}

func TestSanitizeSchema_KeepsPropertyWithEnumNoType(t *testing.T) {
	// A property with enum but no explicit type should be kept.
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": map[string]any{
				"enum": []any{"active", "inactive"},
			},
		},
		"required": []any{"status"},
	}

	got := SanitizeSchema(input)

	props := got["properties"].(map[string]any)
	if _, ok := props["status"]; !ok {
		t.Error("expected 'status' property to be kept (has enum)")
	}

	req := got["required"].([]any)
	if len(req) != 1 || req[0] != "status" {
		t.Errorf("expected required [status], got %v", req)
	}
}

func TestSanitizeSchema_StripsNumericValidationKeywords(t *testing.T) {
	// Gemini rejects exclusiveMinimum/exclusiveMaximum/multipleOf/const with a
	// 400 "Unknown name" error. They must be stripped while the property (and
	// its supported minimum/maximum) is preserved. Mirrors a Pydantic
	// Field(gt=0, lt=100, multiple_of=5) schema.
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{
				"type":             "integer",
				"exclusiveMinimum": float64(0),
				"exclusiveMaximum": float64(100),
				"multipleOf":       float64(5),
				"minimum":          float64(0),
			},
			"mode": map[string]any{
				"type":  "string",
				"const": "fast",
			},
		},
		"required": []any{"count", "mode"},
	}

	got := SanitizeSchema(input)
	props := got["properties"].(map[string]any)

	count := props["count"].(map[string]any)
	for _, k := range []string{"exclusiveMinimum", "exclusiveMaximum", "multipleOf"} {
		if _, ok := count[k]; ok {
			t.Errorf("expected %q to be removed from count", k)
		}
	}
	if count["type"] != "integer" {
		t.Errorf("expected count type to be preserved, got %v", count["type"])
	}
	if count["minimum"] != float64(0) {
		t.Errorf("expected supported 'minimum' to be preserved, got %v", count["minimum"])
	}

	mode := props["mode"].(map[string]any)
	if _, ok := mode["const"]; ok {
		t.Error("expected 'const' to be removed from mode")
	}
	if mode["type"] != "string" {
		t.Errorf("expected mode type to be preserved, got %v", mode["type"])
	}

	// Both properties keep a valid type, so required stays intact.
	req := got["required"].([]any)
	if len(req) != 2 {
		t.Errorf("expected 2 required fields, got %d: %v", len(req), req)
	}
}

func TestSanitizeSchema_KeepsPropertyWithAnyOf(t *testing.T) {
	// A property with anyOf but no explicit type should be kept.
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"value": map[string]any{
				"anyOf": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "integer"},
				},
			},
		},
		"required": []any{"value"},
	}

	got := SanitizeSchema(input)

	props := got["properties"].(map[string]any)
	if _, ok := props["value"]; !ok {
		t.Error("expected 'value' property to be kept (has anyOf)")
	}
}

// ---------------------------------------------------------------------------
// SanitizeSchemaForGemini (allowlist) tests
// ---------------------------------------------------------------------------

func TestSanitizeSchemaForGemini_Nil(t *testing.T) {
	if got := SanitizeSchemaForGemini(nil); got != nil {
		t.Errorf("SanitizeSchemaForGemini(nil) = %v, want nil", got)
	}
}

func TestSanitizeSchemaForGemini_DropsUnknownKeywords(t *testing.T) {
	// Everything not on the Gemini allowlist must be dropped, while supported
	// keywords survive. Covers the denylist keys too (they're not allowlisted).
	input := map[string]any{
		"type":                  "object",
		"$schema":               "https://json-schema.org/draft/2020-12/schema",
		"additionalProperties":  false,
		"$defs":                 map[string]any{"X": map[string]any{"type": "string"}},
		"unevaluatedProperties": false,
		"properties": map[string]any{
			"n": map[string]any{
				"type":             "integer",
				"exclusiveMinimum": float64(0),
				"multipleOf":       float64(2),
				"const":            float64(4),
				"uniqueItems":      true,
				"minimum":          float64(0),
				"description":      "a number",
			},
		},
		"required": []any{"n"},
	}

	got := SanitizeSchemaForGemini(input)

	for _, k := range []string{"$schema", "additionalProperties", "$defs", "unevaluatedProperties"} {
		if _, ok := got[k]; ok {
			t.Errorf("expected top-level %q to be dropped", k)
		}
	}
	n := got["properties"].(map[string]any)["n"].(map[string]any)
	for _, k := range []string{"exclusiveMinimum", "multipleOf", "const", "uniqueItems"} {
		if _, ok := n[k]; ok {
			t.Errorf("expected %q to be dropped from n", k)
		}
	}
	if n["type"] != "integer" || n["minimum"] != float64(0) || n["description"] != "a number" {
		t.Errorf("expected supported keywords preserved, got %v", n)
	}
	if req, _ := got["required"].([]any); len(req) != 1 || req[0] != "n" {
		t.Errorf("expected required [n], got %v", got["required"])
	}
}

func TestSanitizeSchemaForGemini_FormatWhitelist(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"email":  map[string]any{"type": "string", "format": "email"},
			"uri":    map[string]any{"type": "string", "format": "uri"},
			"when":   map[string]any{"type": "string", "format": "date-time"},
			"kind":   map[string]any{"type": "string", "format": "enum", "enum": []any{"a", "b"}},
			"bignum": map[string]any{"type": "integer", "format": "int64"},
			"weird":  map[string]any{"type": "integer", "format": "uint128"},
		},
	}

	got := SanitizeSchemaForGemini(input)
	props := got["properties"].(map[string]any)

	// Unsupported formats dropped, property (and type) retained.
	for _, name := range []string{"email", "uri", "weird"} {
		p := props[name].(map[string]any)
		if _, ok := p["format"]; ok {
			t.Errorf("expected format dropped from %q, got %v", name, p["format"])
		}
		if _, ok := p["type"]; !ok {
			t.Errorf("expected %q to keep its type", name)
		}
	}
	// Supported formats retained.
	if props["when"].(map[string]any)["format"] != "date-time" {
		t.Error("expected date-time format retained")
	}
	if props["kind"].(map[string]any)["format"] != "enum" {
		t.Error("expected enum format retained")
	}
	if props["bignum"].(map[string]any)["format"] != "int64" {
		t.Error("expected int64 format retained")
	}
}

func TestSanitizeSchemaForGemini_TypeArrayToNullable(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":  map[string]any{"type": []any{"string", "null"}},
			"age":   map[string]any{"type": []any{"null", "integer"}},
			"plain": map[string]any{"type": "string"},
		},
	}

	got := SanitizeSchemaForGemini(input)
	props := got["properties"].(map[string]any)

	name := props["name"].(map[string]any)
	if name["type"] != "string" || name["nullable"] != true {
		t.Errorf("expected name type=string nullable=true, got %v", name)
	}
	age := props["age"].(map[string]any)
	if age["type"] != "integer" || age["nullable"] != true {
		t.Errorf("expected age type=integer nullable=true, got %v", age)
	}
	plain := props["plain"].(map[string]any)
	if _, ok := plain["nullable"]; ok {
		t.Errorf("expected plain to have no nullable, got %v", plain)
	}
}

func TestSanitizeSchemaForGemini_OneOfBecomesAnyOf(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"v": map[string]any{
				"oneOf": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "integer", "exclusiveMinimum": float64(0)},
				},
			},
		},
		"required": []any{"v"},
	}

	got := SanitizeSchemaForGemini(input)
	v := got["properties"].(map[string]any)["v"].(map[string]any)

	if _, ok := v["oneOf"]; ok {
		t.Error("expected oneOf to be removed")
	}
	anyOf, ok := v["anyOf"].([]any)
	if !ok || len(anyOf) != 2 {
		t.Fatalf("expected oneOf mapped to anyOf with 2 entries, got %v", v["anyOf"])
	}
	// Nested unsupported keyword inside the branch must also be stripped.
	second := anyOf[1].(map[string]any)
	if _, ok := second["exclusiveMinimum"]; ok {
		t.Error("expected nested exclusiveMinimum stripped inside anyOf branch")
	}
	// Property with anyOf is kept (not pruned).
	if req, _ := got["required"].([]any); len(req) != 1 || req[0] != "v" {
		t.Errorf("expected required [v], got %v", got["required"])
	}
}

func TestSanitizeSchemaForGemini_DropsAllOfAndNot(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"typed": map[string]any{
				"type":  "string",
				"allOf": []any{map[string]any{"minLength": float64(1)}},
				"not":   map[string]any{"const": "x"},
			},
			"onlyAllOf": map[string]any{
				"allOf": []any{map[string]any{"type": "string"}},
			},
		},
		"required": []any{"typed", "onlyAllOf"},
	}

	got := SanitizeSchemaForGemini(input)
	props := got["properties"].(map[string]any)

	typed := props["typed"].(map[string]any)
	if _, ok := typed["allOf"]; ok {
		t.Error("expected allOf dropped")
	}
	if _, ok := typed["not"]; ok {
		t.Error("expected not dropped")
	}
	if typed["type"] != "string" {
		t.Error("expected typed to keep its type")
	}
	// A property that had ONLY allOf becomes empty → pruned from properties and
	// from required.
	if _, ok := props["onlyAllOf"]; ok {
		t.Error("expected onlyAllOf (allOf-only) property to be pruned")
	}
	if req, _ := got["required"].([]any); len(req) != 1 || req[0] != "typed" {
		t.Errorf("expected required [typed], got %v", got["required"])
	}
}

func TestSanitizeSchemaForGemini_RecursesItemsAndNested(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tags": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"id": map[string]any{"type": "integer", "exclusiveMinimum": float64(0)},
					},
				},
			},
		},
	}

	got := SanitizeSchemaForGemini(input)
	items := got["properties"].(map[string]any)["tags"].(map[string]any)["items"].(map[string]any)
	if _, ok := items["additionalProperties"]; ok {
		t.Error("expected additionalProperties stripped in items")
	}
	id := items["properties"].(map[string]any)["id"].(map[string]any)
	if _, ok := id["exclusiveMinimum"]; ok {
		t.Error("expected exclusiveMinimum stripped deep in items.properties")
	}
	if id["type"] != "integer" {
		t.Errorf("expected nested id type=integer, got %v", id["type"])
	}
}

func TestSanitizeSchemaForGemini_DoesNotMutateOriginal(t *testing.T) {
	input := map[string]any{
		"type":    "object",
		"$schema": "x",
		"properties": map[string]any{
			"n": map[string]any{"type": []any{"integer", "null"}, "exclusiveMinimum": float64(0)},
		},
	}
	original := map[string]any{
		"type":    "object",
		"$schema": "x",
		"properties": map[string]any{
			"n": map[string]any{"type": []any{"integer", "null"}, "exclusiveMinimum": float64(0)},
		},
	}

	_ = SanitizeSchemaForGemini(input)

	if !reflect.DeepEqual(input, original) {
		t.Error("SanitizeSchemaForGemini mutated the original input")
	}
}

// TestSanitizeSchemaForGemini_RealKrabbyMCPSchema is a regression test built
// from the ACTUAL tool schema the krabby MCP server emits (generated by the
// modelcontextprotocol go-sdk via google/jsonschema-go). It reproduces the two
// Gemini-incompatible constructs that go-sdk produces and that broke Gemini
// function-calling when the MCP was attached to an agent:
//
//  1. `additionalProperties: false` on the root object.
//  2. `type: ["null","array"]` on every optional []string field.
//
// After sanitization the schema must be Gemini-clean: no additionalProperties,
// and the union type collapsed to a single type + nullable:true.
func TestSanitizeSchemaForGemini_RealKrabbyMCPSchema(t *testing.T) {
	const raw = `{
      "type": "object",
      "properties": {
        "question": {"type": "string", "description": "natural language question or keyword search"},
        "repo": {"type": "string", "description": "repository id"},
        "mode": {"type": "string", "description": "traversal mode"},
        "depth": {"type": "integer", "description": "traversal depth 1-6 (default 3)"},
        "token_budget": {"type": "integer", "description": "max output tokens (default 2000)"},
        "context_filter": {
          "type": ["null", "array"],
          "items": {"type": "string"},
          "description": "optional explicit edge-context filter"
        }
      },
      "required": ["question"],
      "additionalProperties": false
    }`

	var in map[string]any
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	got := SanitizeSchemaForGemini(in)

	if _, ok := got["additionalProperties"]; ok {
		t.Error("expected additionalProperties stripped from krabby schema")
	}

	props := got["properties"].(map[string]any)
	cf := props["context_filter"].(map[string]any)
	if cf["type"] != "array" {
		t.Errorf("expected context_filter.type collapsed to \"array\", got %v", cf["type"])
	}
	if cf["nullable"] != true {
		t.Errorf("expected context_filter.nullable=true, got %v", cf["nullable"])
	}
	if items, ok := cf["items"].(map[string]any); !ok || items["type"] != "string" {
		t.Errorf("expected context_filter.items preserved as {type:string}, got %v", cf["items"])
	}

	// Scalar properties and required list survive untouched.
	if props["depth"].(map[string]any)["type"] != "integer" {
		t.Error("expected depth to remain an integer")
	}
	if req, _ := got["required"].([]any); len(req) != 1 || req[0] != "question" {
		t.Errorf("expected required [question], got %v", got["required"])
	}

	// Final guard: no leftover key anywhere is outside Gemini's vocabulary.
	assertGeminiClean(t, got)
}

// assertGeminiClean walks a sanitized schema and fails if any key is one Gemini
// is known to reject.
func assertGeminiClean(t *testing.T, node map[string]any) {
	t.Helper()
	banned := map[string]struct{}{
		"additionalProperties": {}, "$schema": {}, "$ref": {}, "$defs": {},
		"exclusiveMinimum": {}, "exclusiveMaximum": {}, "multipleOf": {},
		"const": {}, "oneOf": {}, "allOf": {}, "not": {}, "uniqueItems": {},
	}
	for k, v := range node {
		if _, bad := banned[k]; bad {
			t.Errorf("schema still contains Gemini-incompatible key %q", k)
		}
		if k == "type" {
			if _, isArr := v.([]any); isArr {
				t.Errorf("schema still contains array-valued type: %v", v)
			}
		}
		switch child := v.(type) {
		case map[string]any:
			assertGeminiClean(t, child)
		case []any:
			for _, item := range child {
				if m, ok := item.(map[string]any); ok {
					assertGeminiClean(t, m)
				}
			}
		}
	}
}
