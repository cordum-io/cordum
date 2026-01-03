package schema

import (
	"context"
	"encoding/json"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
)

func TestRegistryValidate(t *testing.T) {
	mr := miniredis.RunT(t)
	reg, err := NewRegistry("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	defer reg.Close()

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
			"count": map[string]any{"type": "integer"},
		},
		"required": []any{"name"},
	}
	raw, _ := json.Marshal(schema)
	if err := reg.Register(context.Background(), "test", raw); err != nil {
		t.Fatalf("register: %v", err)
	}

	okPayload := map[string]any{"name": "alpha", "count": 3}
	if err := reg.ValidateID(context.Background(), "test", okPayload); err != nil {
		t.Fatalf("expected validation ok: %v", err)
	}

	badPayload := map[string]any{"count": 3}
	if err := reg.ValidateID(context.Background(), "test", badPayload); err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestValidateMap(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"enabled": map[string]any{"type": "boolean"},
		},
		"required": []any{"enabled"},
	}
	if err := ValidateMap(schema, map[string]any{"enabled": true}); err != nil {
		t.Fatalf("expected schema validation ok: %v", err)
	}
	if err := ValidateMap(schema, map[string]any{"enabled": "nope"}); err == nil {
		t.Fatalf("expected schema validation error")
	}
}
