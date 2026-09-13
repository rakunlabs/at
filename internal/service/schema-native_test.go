package service

import "testing"

func TestNativeSchemaPreservesReferencesAndKeywordPropertyNames(t *testing.T) {
	schema := map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{"address": map[string]any{"$ref": "#/$defs/Address"}, "const": map[string]any{"type": "string"}},
		"required":   []string{"address", "const"},
		"$defs":      map[string]any{"Address": map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}}},
	}
	out := CopyJSONSchema(schema)
	props := out["properties"].(map[string]any)
	if props["address"].(map[string]any)["$ref"] != "#/$defs/Address" || props["const"] == nil || out["additionalProperties"] != false {
		t.Fatalf("schema semantics lost: %#v", out)
	}
	out["required"].([]string)[0] = "changed"
	delete(props, "const")
	if schema["required"].([]string)[0] != "address" || schema["properties"].(map[string]any)["const"] == nil {
		t.Fatal("schema copy aliases caller")
	}
	gemini := SanitizeSchemaForGemini(schema)
	address := gemini["properties"].(map[string]any)["address"].(map[string]any)
	if address["type"] != "object" || address["properties"].(map[string]any)["city"] == nil {
		t.Fatalf("Gemini reference removed instead of expanded: %#v", address)
	}
}
