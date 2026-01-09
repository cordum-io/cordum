package schema

import (
	"encoding/json"
	"testing"
)

func TestValidateSchema(t *testing.T) {
	schema := []byte(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)
	if err := ValidateSchema("test", schema, map[string]any{"name": "ok"}); err != nil {
		t.Fatalf("expected valid schema: %v", err)
	}
	if err := ValidateSchema("test", schema, map[string]any{"nope": "bad"}); err == nil {
		t.Fatalf("expected schema validation error")
	}
}

func TestValidateMapInline(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{"type": "string"},
		},
		"required": []any{"id"},
	}
	if err := ValidateMap(schema, map[string]any{"id": "x"}); err != nil {
		t.Fatalf("expected valid schema: %v", err)
	}
	if err := ValidateMap(schema, map[string]any{}); err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestNormalizeValue(t *testing.T) {
	data := json.RawMessage(`{"k":"v"}`)
	val, err := normalizeValue(data)
	if err != nil {
		t.Fatalf("normalize raw: %v", err)
	}
	m, ok := val.(map[string]any)
	if !ok || m["k"] != "v" {
		t.Fatalf("unexpected normalized value")
	}
	val, err = normalizeValue([]byte(`{"k":"v"}`))
	if err != nil {
		t.Fatalf("normalize bytes: %v", err)
	}
	if _, ok := val.(map[string]any); !ok {
		t.Fatalf("expected map from bytes")
	}
}

func TestValidateSchemaEmpty(t *testing.T) {
	if err := ValidateSchema("test", nil, nil); err == nil {
		t.Fatalf("expected error for empty schema")
	}
	if err := ValidateSchema("test", []byte{}, nil); err == nil {
		t.Fatalf("expected error for empty schema")
	}
}

func TestValidateMapEmpty(t *testing.T) {
	if err := ValidateMap(nil, nil); err == nil {
		t.Fatalf("expected error for empty map schema")
	}
	if err := ValidateMap(map[string]any{}, nil); err == nil {
		t.Fatalf("expected error for empty map schema")
	}
}

func TestNormalizeValueInvalidJSON(t *testing.T) {
	if _, err := normalizeValue(json.RawMessage("{")); err == nil {
		t.Fatalf("expected error for invalid raw json")
	}
	if _, err := normalizeValue([]byte("{")); err == nil {
		t.Fatalf("expected error for invalid byte json")
	}
}

func TestSchemaIDDefault(t *testing.T) {
	if got := schemaID(""); got != "inmemory://schema" {
		t.Fatalf("unexpected schema id: %s", got)
	}
}
