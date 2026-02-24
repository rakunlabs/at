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
