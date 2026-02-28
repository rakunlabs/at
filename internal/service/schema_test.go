package service

import (
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
